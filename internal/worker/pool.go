package worker

import (
	"context"
	"sync"
	"time"

	"key-pool-system/internal/config"
	"key-pool-system/internal/db"
	"key-pool-system/internal/keypool"
	"key-pool-system/internal/queue"
	"github.com/rs/zerolog"
)

// Pool manages a group of workers that process queue items concurrently.
type Pool struct {
	workers []*Worker
	wg      sync.WaitGroup
	logger  zerolog.Logger
}

// NewPool creates a pool of workers. Workers are created but not started.
func NewPool(
	count int,
	q *queue.Queue,
	keyPool *keypool.Manager,
	client HTTPClient,
	dbAdap db.DBAdapter,
	hotCfg *config.HotReloadConfig,
	encKey string,
	logger zerolog.Logger,
) *Pool {
	p := &Pool{
		workers: make([]*Worker, count),
		logger:  logger.With().Str("component", "worker_pool").Logger(),
	}

	for i := 0; i < count; i++ {
		p.workers[i] = NewWorker(i, q, keyPool, client, dbAdap, hotCfg, encKey, logger)
	}

	return p
}

// Start launches all workers with a warmup delay between each.
// Workers start one by one with a small delay to avoid a thundering herd
// of database reads and HTTP connections at startup.
//
// warmupPeriod is the total time to spread worker startups over.
// For example, 10 workers with 30s warmup = one worker every 3 seconds.
func (p *Pool) Start(ctx context.Context, warmupPeriod time.Duration) {
	delay := time.Duration(0)
	if len(p.workers) > 1 {
		delay = warmupPeriod / time.Duration(len(p.workers))
	}

	for i, w := range p.workers {
		p.wg.Add(1)
		go func(w *Worker) {
			defer p.wg.Done()
			w.Start(ctx)
		}(w)

		if i < len(p.workers)-1 && delay > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}
	}

	p.logger.Info().
		Int("worker_count", len(p.workers)).
		Dur("warmup_period", warmupPeriod).
		Msg("all workers started")
}

// Wait blocks until all workers have finished.
func (p *Pool) Wait() {
	p.wg.Wait()
	p.logger.Info().Msg("all workers stopped")
}
