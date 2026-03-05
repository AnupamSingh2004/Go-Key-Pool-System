package config

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// DBReader interface for reading system_config from database
// This prevents circular dependencies - the db package will implement this
type DBReader interface {
	GetSystemConfig(ctx context.Context) (map[string]string, error)
}

// HotReloadConfig holds runtime-configurable values that can be updated without restart
type HotReloadConfig struct {
	mu sync.RWMutex

	WorkerCount                int
	QueueMaxSize               int
	Strategy                   string
	CircuitBreakerThreshold    int
	CircuitBreakerOpenDuration int // seconds
	RetryMaxAttempts           int
	RetryBaseDelayMS           int
	RetryMaxDelayMS            int
	LoadShedLevel1Threshold    float64
	LoadShedLevel2Threshold    float64
}

// NewHotReloadConfig creates a new hot reload config initialized from static config
func NewHotReloadConfig(staticCfg *Config) *HotReloadConfig {
	return &HotReloadConfig{
		WorkerCount:                staticCfg.WorkerCount,
		QueueMaxSize:               staticCfg.QueueMaxSize,
		Strategy:                   staticCfg.KeyPoolStrategy,
		CircuitBreakerThreshold:    staticCfg.CircuitBreakerFailureThreshold,
		CircuitBreakerOpenDuration: int(staticCfg.CircuitBreakerOpenDuration.Seconds()),
		RetryMaxAttempts:           staticCfg.RetryMaxAttempts,
		RetryBaseDelayMS:           staticCfg.RetryBaseDelayMS,
		RetryMaxDelayMS:            staticCfg.RetryMaxDelayMS,
		LoadShedLevel1Threshold:    staticCfg.LoadShedLevel1Threshold,
		LoadShedLevel2Threshold:    staticCfg.LoadShedLevel2Threshold,
	}
}

// Get returns a copy of the current hot reload config (thread-safe read)
func (h *HotReloadConfig) Get() HotReloadConfig {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return HotReloadConfig{
		WorkerCount:                h.WorkerCount,
		QueueMaxSize:               h.QueueMaxSize,
		Strategy:                   h.Strategy,
		CircuitBreakerThreshold:    h.CircuitBreakerThreshold,
		CircuitBreakerOpenDuration: h.CircuitBreakerOpenDuration,
		RetryMaxAttempts:           h.RetryMaxAttempts,
		RetryBaseDelayMS:           h.RetryBaseDelayMS,
		RetryMaxDelayMS:            h.RetryMaxDelayMS,
		LoadShedLevel1Threshold:    h.LoadShedLevel1Threshold,
		LoadShedLevel2Threshold:    h.LoadShedLevel2Threshold,
	}
}

// Update applies new values from database (thread-safe write)
func (h *HotReloadConfig) Update(dbConfig map[string]string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()

	var changes []string

	// Update integer fields
	changes = append(changes, h.updateInt(dbConfig, "worker_count", &h.WorkerCount)...)
	changes = append(changes, h.updateInt(dbConfig, "queue_max_size", &h.QueueMaxSize)...)
	changes = append(changes, h.updateInt(dbConfig, "circuit_breaker_threshold", &h.CircuitBreakerThreshold)...)
	changes = append(changes, h.updateInt(dbConfig, "circuit_breaker_open_duration_seconds", &h.CircuitBreakerOpenDuration)...)
	changes = append(changes, h.updateInt(dbConfig, "retry_max_attempts", &h.RetryMaxAttempts)...)
	changes = append(changes, h.updateInt(dbConfig, "retry_base_delay_ms", &h.RetryBaseDelayMS)...)
	changes = append(changes, h.updateInt(dbConfig, "retry_max_delay_ms", &h.RetryMaxDelayMS)...)

	// Update strategy (with validation)
	changes = append(changes, h.updateStrategy(dbConfig)...)

	// Update float fields
	changes = append(changes, h.updateFloat(dbConfig, "load_shed_level1", &h.LoadShedLevel1Threshold)...)
	changes = append(changes, h.updateFloat(dbConfig, "load_shed_level2", &h.LoadShedLevel2Threshold)...)

	return changes
}

// updateInt updates an integer field if the new value is valid
func (h *HotReloadConfig) updateInt(dbConfig map[string]string, key string, field *int) []string {
	val, ok := dbConfig[key]
	if !ok {
		return nil
	}

	newVal, err := strconv.Atoi(val)
	if err != nil || newVal <= 0 || newVal == *field {
		return nil
	}

	oldVal := *field
	*field = newVal
	return []string{fmt.Sprintf("%s: %d -> %d", key, oldVal, newVal)}
}

// updateFloat updates a float field if the new value is valid
func (h *HotReloadConfig) updateFloat(dbConfig map[string]string, key string, field *float64) []string {
	val, ok := dbConfig[key]
	if !ok {
		return nil
	}

	newVal, err := strconv.ParseFloat(val, 64)
	if err != nil || newVal < 0 || newVal > 1 || newVal == *field {
		return nil
	}

	oldVal := *field
	*field = newVal
	return []string{fmt.Sprintf("%s: %.2f -> %.2f", key, oldVal, newVal)}
}

// updateStrategy updates the strategy field with validation
func (h *HotReloadConfig) updateStrategy(dbConfig map[string]string) []string {
	val, ok := dbConfig["strategy"]
	if !ok {
		return nil
	}

	// Validate strategy
	if !ValidStrategies[val] || val == h.Strategy {
		return nil
	}

	oldVal := h.Strategy
	h.Strategy = val
	return []string{fmt.Sprintf("strategy: %s -> %s", oldVal, val)}
}

// StartReloadLoop continuously reads config from DB and applies updates
// This runs in a goroutine and stops when ctx is cancelled
func (h *HotReloadConfig) StartReloadLoop(ctx context.Context, db DBReader, interval time.Duration, logger func(msg string, changes []string)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	logger("config hot reload loop started", nil)

	for {
		select {
		case <-ctx.Done():
			logger("config hot reload loop stopped", nil)
			return

		case <-ticker.C:
			dbConfig, err := db.GetSystemConfig(ctx)
			if err != nil {
				// Log error but continue - don't crash the reload loop
				logger("failed to read system_config from DB: "+err.Error(), nil)
				continue
			}

			changes := h.Update(dbConfig)
			if len(changes) > 0 {
				logger("config hot reload applied changes", changes)
			}
		}
	}
}
