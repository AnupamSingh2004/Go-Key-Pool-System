package config

import (
	"fmt"
	"strings"
	"time"
)

// Config holds all application configuration loaded from environment variables
type Config struct {
	// Server configuration
	ServerPort            int
	ServerReadTimeout     time.Duration
	ServerWriteTimeout    time.Duration
	ServerIdleTimeout     time.Duration
	ServerShutdownTimeout time.Duration

	// Database configuration
	DBPath          string
	DBMaxOpenConns  int
	DBBusyTimeoutMS int

	// Security configuration
	EncryptionKey string
	AdminToken    string

	// Worker pool configuration
	WorkerCount        int
	WorkerWarmupPeriod time.Duration

	// Queue configuration
	QueueMaxSize                 int
	QueueHighPriorityPreemptsLow bool

	// Key pool configuration
	KeyPoolStrategy            string
	KeyPoolReloadInterval      time.Duration
	KeyPoolHealthCheckInterval time.Duration

	// Circuit breaker configuration
	CircuitBreakerFailureThreshold int
	CircuitBreakerOpenDuration     time.Duration
	CircuitBreakerProbeTimeout     time.Duration

	// Retry configuration
	RetryMaxAttempts int
	RetryBaseDelayMS int
	RetryMaxDelayMS  int
	RetryJitterMaxMS int

	// HTTP client configuration
	HTTPClientTimeout         time.Duration
	HTTPClientMaxIdleConns    int
	HTTPClientIdleConnTimeout time.Duration

	// Load shedding configuration
	LoadShedLevel1Threshold float64
	LoadShedLevel2Threshold float64

	// Config hot reload
	ConfigReloadInterval time.Duration

	// Logging configuration
	LogLevel    string
	LogFormat   string
	LogRequests bool

	// Metrics configuration
	MetricsEnabled bool
	MetricsPath    string
}

// Load reads configuration from environment variables and validates required fields
func Load() (*Config, error) {
	cfg := &Config{
		// Server - defaults
		ServerPort:            getEnvAsInt("SERVER_PORT", 8080),
		ServerReadTimeout:     getEnvAsDuration("SERVER_READ_TIMEOUT_SECONDS", 30, time.Second),
		ServerWriteTimeout:    getEnvAsDuration("SERVER_WRITE_TIMEOUT_SECONDS", 30, time.Second),
		ServerIdleTimeout:     getEnvAsDuration("SERVER_IDLE_TIMEOUT_SECONDS", 120, time.Second),
		ServerShutdownTimeout: getEnvAsDuration("SERVER_SHUTDOWN_TIMEOUT_SECONDS", 30, time.Second),

		// Database
		DBPath:          getEnv("DB_PATH", "./data/pool.db"),
		DBMaxOpenConns:  getEnvAsInt("DB_MAX_OPEN_CONNS", 1),
		DBBusyTimeoutMS: getEnvAsInt("DB_BUSY_TIMEOUT_MS", 5000),

		// Security - REQUIRED, no defaults
		EncryptionKey: getEnv("ENCRYPTION_KEY", ""),
		AdminToken:    getEnv("ADMIN_TOKEN", ""),

		// Worker pool
		WorkerCount:        getEnvAsInt("WORKER_COUNT", 10),
		WorkerWarmupPeriod: getEnvAsDuration("WORKER_WARMUP_SECONDS", 30, time.Second),

		// Queue
		QueueMaxSize:                 getEnvAsInt("QUEUE_MAX_SIZE", 1000),
		QueueHighPriorityPreemptsLow: getEnvAsBool("QUEUE_HIGH_PRIORITY_PREEMPTS_LOW", true),

		// Key pool
		KeyPoolStrategy:            getEnv("KEY_POOL_STRATEGY", "round_robin"),
		KeyPoolReloadInterval:      getEnvAsDuration("KEY_POOL_RELOAD_INTERVAL_SECONDS", 60, time.Second),
		KeyPoolHealthCheckInterval: getEnvAsDuration("KEY_POOL_HEALTH_CHECK_INTERVAL_SECONDS", 10, time.Second),

		// Circuit breaker
		CircuitBreakerFailureThreshold: getEnvAsInt("CIRCUIT_BREAKER_FAILURE_THRESHOLD", 3),
		CircuitBreakerOpenDuration:     getEnvAsDuration("CIRCUIT_BREAKER_OPEN_DURATION_SECONDS", 60, time.Second),
		CircuitBreakerProbeTimeout:     getEnvAsDuration("CIRCUIT_BREAKER_PROBE_TIMEOUT_SECONDS", 10, time.Second),

		// Retry
		RetryMaxAttempts: getEnvAsInt("RETRY_MAX_ATTEMPTS", 4),
		RetryBaseDelayMS: getEnvAsInt("RETRY_BASE_DELAY_MS", 1000),
		RetryMaxDelayMS:  getEnvAsInt("RETRY_MAX_DELAY_MS", 30000),
		RetryJitterMaxMS: getEnvAsInt("RETRY_JITTER_MAX_MS", 500),

		// HTTP client
		HTTPClientTimeout:         getEnvAsDuration("HTTP_CLIENT_TIMEOUT_SECONDS", 30, time.Second),
		HTTPClientMaxIdleConns:    getEnvAsInt("HTTP_CLIENT_MAX_IDLE_CONNS", 100),
		HTTPClientIdleConnTimeout: getEnvAsDuration("HTTP_CLIENT_IDLE_CONN_TIMEOUT_SECONDS", 90, time.Second),

		// Load shedding
		LoadShedLevel1Threshold: getEnvAsFloat("LOAD_SHED_LEVEL_1_THRESHOLD", 0.50),
		LoadShedLevel2Threshold: getEnvAsFloat("LOAD_SHED_LEVEL_2_THRESHOLD", 0.25),

		// Config hot reload
		ConfigReloadInterval: getEnvAsDuration("CONFIG_RELOAD_INTERVAL_SECONDS", 30, time.Second),

		// Logging
		LogLevel:    getEnv("LOG_LEVEL", "info"),
		LogFormat:   getEnv("LOG_FORMAT", "json"),
		LogRequests: getEnvAsBool("LOG_REQUESTS", true),

		// Metrics
		MetricsEnabled: getEnvAsBool("METRICS_ENABLED", true),
		MetricsPath:    getEnv("METRICS_PATH", "/metrics"),
	}

	// Validate required fields
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return cfg, nil
}

// validate checks that all required configuration is present and valid
func (c *Config) validate() error {
	// Security validations
	if err := validateEncryptionKey(c.EncryptionKey); err != nil {
		return err
	}
	if err := validateAdminToken(c.AdminToken); err != nil {
		return err
	}

	// Database validation
	if err := validateDBMaxOpenConns(c.DBMaxOpenConns); err != nil {
		return err
	}

	// Strategy validation
	if err := validateStrategy(c.KeyPoolStrategy); err != nil {
		return err
	}

	// Logging validations
	if err := validateLogLevel(c.LogLevel); err != nil {
		return err
	}
	if err := validateLogFormat(c.LogFormat); err != nil {
		return err
	}

	// Positive integer validations
	if err := validatePositiveInt("WORKER_COUNT", c.WorkerCount); err != nil {
		return err
	}
	if err := validatePositiveInt("QUEUE_MAX_SIZE", c.QueueMaxSize); err != nil {
		return err
	}
	if err := validatePositiveInt("RETRY_MAX_ATTEMPTS", c.RetryMaxAttempts); err != nil {
		return err
	}

	return nil
}

// IsDebug returns true if log level is debug
func (c *Config) IsDebug() bool {
	return strings.ToLower(c.LogLevel) == "debug"
}
