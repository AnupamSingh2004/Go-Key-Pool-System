package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"key-pool-system/internal/api"
	"key-pool-system/internal/config"
	"key-pool-system/internal/db"
	"key-pool-system/internal/httpclient"
	"key-pool-system/internal/keypool"
	"key-pool-system/internal/metrics"
	"key-pool-system/internal/queue"
	"key-pool-system/internal/util"
	"key-pool-system/internal/worker"

	"github.com/rs/zerolog"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Setup logger
	logger := setupLogger(cfg)
	logger.Info().Msg("starting key-pool-system")

	// Initialize database
	dbAdapter, err := db.NewSQLiteAdapter(cfg.DBPath, cfg.DBMaxOpenConns, cfg.DBBusyTimeoutMS)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to initialize database")
	}
	defer dbAdapter.Close()
	logger.Info().Str("path", cfg.DBPath).Msg("database initialized")

	// Run migrations
	ctx, cancel := util.DBContext(context.Background(), util.DBTimeoutLong)
	if err := db.RunMigrations(ctx, dbAdapter.DB(), "./migrations"); err != nil {
		cancel()
		logger.Fatal().Err(err).Msg("failed to run migrations")
	}
	cancel()
	logger.Info().Msg("migrations completed")

	// Setup hot-reloadable config
	hotCfg := config.NewHotReloadConfig(cfg)

	// Initialize key pool manager
	poolMgr, err := keypool.NewManager(dbAdapter, hotCfg, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to initialize key pool manager")
	}
	logger.Info().Int("pool_size", poolMgr.PoolSize()).Msg("key pool initialized")

	// Initialize queue
	q := queue.NewQueue(cfg.QueueMaxSize, cfg.QueueHighPriorityPreemptsLow, logger)
	logger.Info().Int("max_size", cfg.QueueMaxSize).Msg("queue initialized")

	// Initialize HTTP client
	httpClient := httpclient.NewClient(
		cfg.HTTPClientTimeout,
		cfg.HTTPClientMaxIdleConns,
		cfg.HTTPClientIdleConnTimeout,
	)

	// Initialize metrics
	m := metrics.New()
	m.TotalWorkers.Store(int64(cfg.WorkerCount))

	// Create root context with cancellation for graceful shutdown
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	// Start worker pool
	workerPool := worker.NewPool(
		cfg.WorkerCount, q, poolMgr, httpClient,
		dbAdapter, hotCfg, cfg.EncryptionKey, logger,
	)
	workerPool.Start(rootCtx, cfg.WorkerWarmupPeriod)
	logger.Info().Int("workers", cfg.WorkerCount).Msg("worker pool started")

	// Start key pool reload loop
	go poolMgr.StartReloadLoop(rootCtx, cfg.KeyPoolReloadInterval)

	// Start config hot reload loop
	go hotCfg.StartReloadLoop(rootCtx, dbAdapter, cfg.ConfigReloadInterval, func(msg string, changes []string) {
		logger.Info().Strs("changes", changes).Msg(msg)
	})

	// Setup HTTP server
	srv := &api.Server{
		DB:     dbAdapter,
		Queue:  q,
		Pool:   poolMgr,
		HotCfg: hotCfg,
		Cfg:    cfg,
		Logger: logger,
	}

	handler := api.NewRouter(srv)

	// Register metrics endpoint if enabled
	mux := http.NewServeMux()
	mux.Handle("/", handler)
	if cfg.MetricsEnabled {
		mux.Handle(cfg.MetricsPath, m.Handler())
		logger.Info().Str("path", cfg.MetricsPath).Msg("metrics endpoint enabled")
	}

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.ServerPort),
		Handler:      mux,
		ReadTimeout:  cfg.ServerReadTimeout,
		WriteTimeout: cfg.ServerWriteTimeout,
		IdleTimeout:  cfg.ServerIdleTimeout,
	}

	// Start server in goroutine
	go func() {
		logger.Info().Int("port", cfg.ServerPort).Msg("HTTP server listening")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal().Err(err).Msg("HTTP server error")
		}
	}()

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigChan
	logger.Info().Str("signal", sig.String()).Msg("shutdown signal received")

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ServerShutdownTimeout)
	defer shutdownCancel()

	// Stop accepting new requests
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("HTTP server shutdown error")
	}

	// Cancel root context to stop workers, reload loops, etc.
	rootCancel()

	// Close the queue to drain remaining items
	q.Close()

	logger.Info().Msg("key-pool-system stopped")
}

func setupLogger(cfg *config.Config) zerolog.Logger {
	level, err := zerolog.ParseLevel(cfg.LogLevel)
	if err != nil {
		level = zerolog.InfoLevel
	}

	if cfg.LogFormat == "pretty" {
		return zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout}).
			Level(level).
			With().Timestamp().Logger()
	}

	return zerolog.New(os.Stdout).
		Level(level).
		With().Timestamp().Logger()
}
