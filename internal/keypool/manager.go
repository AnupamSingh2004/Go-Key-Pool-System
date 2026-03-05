package keypool

import (
	"context"
	"sync"
	"time"

	"key-pool-system/internal/config"
	"key-pool-system/internal/db"
	"key-pool-system/internal/util"
	"github.com/rs/zerolog"
)

// Manager owns all pool keys, strategies, and circuit breakers.
// It is the single entry point for getting a key for a request.
type Manager struct {
	mu sync.RWMutex

	keys       []*PoolKey
	strategies map[string]KeySelectionStrategy
	activeStmt string // current strategy name

	hotCfg *config.HotReloadConfig
	dbAdap db.DBAdapter
	logger zerolog.Logger
}

// NewManager creates a key pool manager and loads keys from the database.
func NewManager(
	dbAdap db.DBAdapter,
	hotCfg *config.HotReloadConfig,
	logger zerolog.Logger,
) (*Manager, error) {
	m := &Manager{
		strategies: make(map[string]KeySelectionStrategy),
		hotCfg:     hotCfg,
		dbAdap:     dbAdap,
		logger:     logger.With().Str("component", "keypool").Logger(),
	}

	// Register built-in strategies
	m.strategies["round_robin"] = NewRoundRobinStrategy()
	m.strategies["weighted_round_robin"] = NewWeightedRoundRobinStrategy()

	cfg := hotCfg.Get()
	m.activeStmt = cfg.Strategy

	// Load keys from DB at startup
	if err := m.reloadKeys(); err != nil {
		return nil, err
	}

	return m, nil
}

// GetKey selects an available key from the pool.
//
// It filters for keys that are:
//   - circuit breaker allows requests
//   - under concurrent limit
//   - within rate limits
//
// Returns nil if no key is available.
func (m *Manager) GetKey() *PoolKey {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Build a list of available keys
	var available []*PoolKey
	for _, key := range m.keys {
		if key.circuitBreaker != nil && !key.circuitBreaker.AllowRequest() {
			continue
		}
		available = append(available, key)
	}

	if len(available) == 0 {
		return nil
	}

	// Use current strategy to pick a key
	strategy := m.strategies[m.activeStmt]
	if strategy == nil {
		strategy = m.strategies["round_robin"]
	}

	// Try each key the strategy selects until we find one that can be acquired
	for attempts := 0; attempts < len(available); attempts++ {
		selected := strategy.Select(available)
		if selected == nil {
			return nil
		}

		// Check concurrent limit
		if !selected.TryConcurrentAcquire() {
			// Remove this key from available and try again
			available = removeKey(available, selected)
			if len(available) == 0 {
				return nil
			}
			continue
		}

		// Check rate limits
		if !selected.TryRateLimit() {
			selected.ConcurrentRelease()
			available = removeKey(available, selected)
			if len(available) == 0 {
				return nil
			}
			continue
		}

		return selected
	}

	return nil
}

// ReleaseKey decrements the concurrent count after a request completes.
func (m *Manager) ReleaseKey(key *PoolKey) {
	key.ConcurrentRelease()
}

// MarkSuccess records a successful request for the key's circuit breaker.
func (m *Manager) MarkSuccess(key *PoolKey) {
	if key.circuitBreaker != nil {
		previousState := key.circuitBreaker.State()
		key.circuitBreaker.RecordSuccess()

		if previousState != db.CircuitStateClosed {
			m.logger.Info().
				Str("key_id", key.ID).
				Str("key_name", key.Name).
				Str("from_state", previousState).
				Msg("circuit breaker closed after success")

			m.logKeyEvent(key.ID, db.EventCircuitClosed, "circuit breaker closed after successful request")
		}
	}

	// Update last used time in DB (best effort, don't block on error)
	ctx, cancel := util.DBContext(context.Background(), util.DBTimeoutShort)
	defer cancel()
	_ = m.dbAdap.UpdateAPIKeyLastUsed(ctx, key.ID)
}

// MarkFailed records a failed request for the key's circuit breaker.
func (m *Manager) MarkFailed(key *PoolKey) {
	if key.circuitBreaker == nil {
		return
	}

	previousState := key.circuitBreaker.State()
	key.circuitBreaker.RecordFailure()
	newState := key.circuitBreaker.State()

	if previousState != newState {
		m.logger.Warn().
			Str("key_id", key.ID).
			Str("key_name", key.Name).
			Str("from_state", previousState).
			Str("to_state", newState).
			Int("failure_count", key.circuitBreaker.FailureCount()).
			Msg("circuit breaker state changed")

		eventType := db.EventKeyFailed
		msg := "key failed"
		if newState == db.CircuitStateOpen {
			eventType = db.EventCircuitOpened
			msg = "circuit breaker opened after repeated failures"
		}
		m.logKeyEvent(key.ID, eventType, msg)
	}

	// Persist health state to DB (best effort)
	ctx, cancel := util.DBContext(context.Background(), util.DBTimeoutShort)
	defer cancel()
	isHealthy := newState == db.CircuitStateClosed
	_ = m.dbAdap.UpdateAPIKeyHealth(ctx, key.ID, isHealthy, key.circuitBreaker.FailureCount(), newState)
}

// GetHealthStatus returns a summary of all keys and their circuit states.
func (m *Manager) GetHealthStatus() []KeyHealth {
	m.mu.RLock()
	defer m.mu.RUnlock()

	statuses := make([]KeyHealth, len(m.keys))
	for i, key := range m.keys {
		state := db.CircuitStateClosed
		failures := 0
		if key.circuitBreaker != nil {
			state = key.circuitBreaker.State()
			failures = key.circuitBreaker.FailureCount()
		}
		statuses[i] = KeyHealth{
			ID:                key.ID,
			Name:              key.Name,
			CircuitState:      state,
			FailureCount:      failures,
			CurrentConcurrent: key.GetCurrentConcurrent(),
			ConcurrentLimit:   key.ConcurrentLimit,
			Weight:            key.Weight,
		}
	}
	return statuses
}

// KeyHealth is a read-only snapshot of a key's health, used by the API layer.
type KeyHealth struct {
	ID                string
	Name              string
	CircuitState      string
	FailureCount      int
	CurrentConcurrent int
	ConcurrentLimit   int
	Weight            int
}

// PoolSize returns the number of keys in the pool.
func (m *Manager) PoolSize() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.keys)
}

// StartReloadLoop periodically reloads keys from the database and checks
// if the strategy has changed via hot reload config.
func (m *Manager) StartReloadLoop(ctx context.Context, reloadInterval time.Duration) {
	go func() {
		ticker := time.NewTicker(reloadInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				m.logger.Info().Msg("key pool reload loop stopped")
				return
			case <-ticker.C:
				if err := m.reloadKeys(); err != nil {
					m.logger.Error().Err(err).Msg("failed to reload keys from database")
				}
				m.updateStrategy()
			}
		}
	}()
}

// reloadKeys reads all keys from the database and refreshes the pool.
// Existing circuit breaker and runtime state is preserved for keys
// that already exist; only new keys get fresh state.
func (m *Manager) reloadKeys() error {
	ctx, cancel := util.DBContext(context.Background(), util.DBTimeoutLong)
	defer cancel()

	dbKeys, err := m.dbAdap.GetAllAPIKeys(ctx)
	if err != nil {
		return err
	}

	cfg := m.hotCfg.Get()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Build a map of existing keys to preserve state
	existing := make(map[string]*PoolKey, len(m.keys))
	for _, key := range m.keys {
		existing[key.ID] = key
	}

	newKeys := make([]*PoolKey, 0, len(dbKeys))
	for _, dbKey := range dbKeys {
		if old, ok := existing[dbKey.ID]; ok {
			// Update DB-sourced fields but keep runtime state
			old.Name = dbKey.Name
			old.KeyEncrypted = dbKey.KeyEncrypted
			old.Weight = dbKey.Weight
			old.RateLimitPerMinute = dbKey.RateLimitPerMinute
			old.RateLimitPerDay = dbKey.RateLimitPerDay
			old.ConcurrentLimit = dbKey.ConcurrentLimit
			newKeys = append(newKeys, old)
		} else {
			// New key — initialize fresh runtime state
			pk := &PoolKey{
				ID:                 dbKey.ID,
				Name:               dbKey.Name,
				KeyEncrypted:       dbKey.KeyEncrypted,
				Weight:             dbKey.Weight,
				RateLimitPerMinute: dbKey.RateLimitPerMinute,
				RateLimitPerDay:    dbKey.RateLimitPerDay,
				ConcurrentLimit:    dbKey.ConcurrentLimit,
				circuitBreaker: NewCircuitBreaker(
					cfg.CircuitBreakerThreshold,
					time.Duration(cfg.CircuitBreakerOpenDuration)*time.Second,
				),
			}
			newKeys = append(newKeys, pk)
		}
	}

	m.keys = newKeys
	m.logger.Debug().Int("key_count", len(m.keys)).Msg("key pool reloaded")
	return nil
}

// updateStrategy checks hot reload config and switches strategy if changed.
func (m *Manager) updateStrategy() {
	cfg := m.hotCfg.Get()

	m.mu.Lock()
	defer m.mu.Unlock()

	if cfg.Strategy != m.activeStmt {
		if _, ok := m.strategies[cfg.Strategy]; ok {
			m.logger.Info().
				Str("from", m.activeStmt).
				Str("to", cfg.Strategy).
				Msg("switching key selection strategy")
			m.activeStmt = cfg.Strategy
			// Reset new strategy state for clean start
			m.strategies[cfg.Strategy].Reset()
		}
	}
}

// logKeyEvent writes a key event to the database (best effort).
func (m *Manager) logKeyEvent(keyID, eventType, message string) {
	ctx, cancel := util.DBContext(context.Background(), util.DBTimeoutShort)
	defer cancel()

	event := &db.KeyEvent{
		KeyID:     keyID,
		EventType: eventType,
		Message:   &message,
	}
	if err := m.dbAdap.CreateKeyEvent(ctx, event); err != nil {
		m.logger.Error().Err(err).
			Str("key_id", keyID).
			Str("event_type", eventType).
			Msg("failed to log key event")
	}
}

// removeKey returns a new slice without the specified key.
func removeKey(keys []*PoolKey, target *PoolKey) []*PoolKey {
	result := make([]*PoolKey, 0, len(keys)-1)
	for _, k := range keys {
		if k != target {
			result = append(result, k)
		}
	}
	return result
}
