package worker

import (
	"context"
	"time"

	"key-pool-system/internal/config"
	"key-pool-system/internal/crypto"
	"key-pool-system/internal/db"
	"key-pool-system/internal/keypool"
	"key-pool-system/internal/queue"
	"key-pool-system/internal/util"
	"github.com/rs/zerolog"
)

// HTTPClient is the interface the worker uses to make API calls.
// This will be implemented by the httpclient package (Step 6).
type HTTPClient interface {
	Do(ctx context.Context, req *HTTPRequest) (*HTTPResponse, error)
}

// HTTPRequest is what the worker sends to the downstream API.
type HTTPRequest struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    []byte
	APIKey  string // decrypted key value
}

// HTTPResponse is what the worker gets back.
type HTTPResponse struct {
	StatusCode int
	Body       []byte
}

// Worker processes one queue item at a time in its own goroutine.
type Worker struct {
	id     int
	queue  *queue.Queue
	pool   *keypool.Manager
	client HTTPClient
	dbAdap db.DBAdapter
	hotCfg *config.HotReloadConfig
	encKey string // encryption key for decrypting API keys
	logger zerolog.Logger
}

// NewWorker creates a worker with all its dependencies.
func NewWorker(
	id int,
	q *queue.Queue,
	pool *keypool.Manager,
	client HTTPClient,
	dbAdap db.DBAdapter,
	hotCfg *config.HotReloadConfig,
	encKey string,
	logger zerolog.Logger,
) *Worker {
	return &Worker{
		id:     id,
		queue:  q,
		pool:   pool,
		client: client,
		dbAdap: dbAdap,
		hotCfg: hotCfg,
		encKey: encKey,
		logger: logger.With().Int("worker_id", id).Logger(),
	}
}

// Start begins the worker loop. It reads from the queue channel
// and processes each item until the context is cancelled.
func (w *Worker) Start(ctx context.Context) {
	w.logger.Debug().Msg("worker started")

	for {
		select {
		case <-ctx.Done():
			w.logger.Debug().Msg("worker stopped")
			return

		case item, ok := <-w.queue.Dequeue():
			if !ok {
				w.logger.Debug().Msg("queue closed, worker stopping")
				return
			}
			w.processItem(ctx, item)
		}
	}
}

// processItem handles a single queue item end to end:
//  1. Load the request from DB
//  2. Get a key from the pool
//  3. Decrypt the key
//  4. Make the HTTP call
//  5. Handle success or failure (retry if needed)
func (w *Worker) processItem(ctx context.Context, item *queue.Item) {
	log := w.logger.With().Str("request_id", item.RequestID).Logger()

	// 1. Load full request from database
	dbCtx, cancel := util.DBContext(ctx, util.DBTimeoutLong)
	defer cancel()

	req, err := w.dbAdap.GetRequest(dbCtx, item.RequestID)
	if err != nil {
		log.Error().Err(err).Msg("failed to load request from database")
		return
	}
	if req == nil {
		log.Warn().Msg("request not found in database, skipping")
		return
	}

	// Skip if already completed or failed permanently
	if req.Status == db.RequestStatusSuccess || req.Status == db.RequestStatusFailed {
		return
	}

	// 2. Get a key from the pool
	key := w.pool.GetKey()
	if key == nil {
		log.Warn().Msg("no key available, re-queuing request")
		w.requeueWithDelay(ctx, item, req)
		return
	}

	// 3. Mark request as processing
	dbCtx2, cancel2 := util.DBContext(ctx, util.DBTimeoutShort)
	defer cancel2()
	_ = w.dbAdap.UpdateRequestStatus(dbCtx2, req.ID, db.RequestStatusProcessing, key.ID, req.Attempts+1)

	// 4. Decrypt the key
	decryptedKey, err := crypto.Decrypt(key.KeyEncrypted, w.encKey)
	if err != nil {
		log.Error().Err(err).Str("key_id", key.ID).Msg("failed to decrypt API key")
		w.pool.ReleaseKey(key)
		w.handleFailure(ctx, item, req, key, "decryption failed: "+err.Error())
		return
	}

	// 5. Build and execute the HTTP request
	httpReq := &HTTPRequest{
		Method: req.Method,
		URL:    req.DestinationURL,
		APIKey: decryptedKey,
	}
	if req.Headers != nil {
		httpReq.Headers = util.ParseJSONMap(*req.Headers)
	}
	if req.Payload != nil {
		httpReq.Body = []byte(*req.Payload)
	}

	resp, err := w.client.Do(ctx, httpReq)

	// Always release the key's concurrent slot
	w.pool.ReleaseKey(key)

	// 6. Handle result
	if err != nil {
		log.Warn().Err(err).Str("key_id", key.ID).Msg("HTTP request failed")
		w.pool.MarkFailed(key)
		w.handleFailure(ctx, item, req, key, err.Error())
		return
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		w.handleSuccess(ctx, req, key, resp)
	} else if resp.StatusCode == 429 || resp.StatusCode >= 500 {
		// Rate limited or server error — retryable
		log.Warn().
			Int("status_code", resp.StatusCode).
			Str("key_id", key.ID).
			Msg("retryable HTTP error")
		w.pool.MarkFailed(key)
		w.handleFailure(ctx, item, req, key, string(resp.Body))
	} else {
		// 4xx (except 429) — permanent failure, don't retry
		log.Warn().
			Int("status_code", resp.StatusCode).
			Str("key_id", key.ID).
			Msg("permanent HTTP error, not retrying")
		w.pool.MarkSuccess(key) // key itself is fine, request is bad
		w.persistResult(ctx, req.ID, db.RequestStatusFailed, resp.StatusCode, string(resp.Body), "")
	}
}

// handleSuccess persists the successful result and marks the key healthy.
func (w *Worker) handleSuccess(ctx context.Context, req *db.Request, key *keypool.PoolKey, resp *HTTPResponse) {
	w.pool.MarkSuccess(key)
	w.persistResult(ctx, req.ID, db.RequestStatusSuccess, resp.StatusCode, string(resp.Body), "")
}

// handleFailure decides whether to retry or permanently fail the request.
func (w *Worker) handleFailure(ctx context.Context, item *queue.Item, req *db.Request, key *keypool.PoolKey, errMsg string) {
	attempts := req.Attempts + 1

	if ShouldRetry(attempts, w.hotCfg) {
		w.logger.Info().
			Str("request_id", req.ID).
			Int("attempt", attempts).
			Msg("scheduling retry")

		delay := CalculateBackoff(attempts-1, w.hotCfg)
		w.delayedEnqueue(ctx, item, req.ID, delay)
	} else {
		// Max retries exhausted — mark as permanently failed
		w.logger.Warn().
			Str("request_id", req.ID).
			Int("attempts", attempts).
			Msg("request permanently failed after max retries")

		w.persistResult(ctx, req.ID, db.RequestStatusFailed, 0, "", errMsg)
	}
}

// persistResult writes the final result to the database (best effort).
func (w *Worker) persistResult(ctx context.Context, requestID, status string, responseStatus int, responseBody, lastError string) {
	dbCtx, cancel := util.DBContext(ctx, util.DBTimeoutShort)
	defer cancel()

	_ = w.dbAdap.UpdateRequestResult(
		dbCtx, requestID, status,
		responseStatus, responseBody, lastError,
		time.Now().UTC(),
	)
}

// requeueWithDelay re-queues a request after a short delay when no key is available.
func (w *Worker) requeueWithDelay(ctx context.Context, item *queue.Item, req *db.Request) {
	delay := CalculateBackoff(0, w.hotCfg)
	w.delayedEnqueue(ctx, item, req.ID, delay)
}

// delayedEnqueue waits for the given delay then puts the item back in the queue.
// Runs in a separate goroutine so the worker isn't blocked.
func (w *Worker) delayedEnqueue(ctx context.Context, item *queue.Item, requestID string, delay time.Duration) {
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()

		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if err := w.queue.Enqueue(item); err != nil {
				w.logger.Error().Err(err).
					Str("request_id", requestID).
					Msg("failed to re-queue request")
			}
		}
	}()
}
