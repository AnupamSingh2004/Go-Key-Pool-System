package worker

import (
	"math"
	"math/rand"
	"time"

	"key-pool-system/internal/config"
)

// CalculateBackoff returns the delay before the next retry attempt.
// Uses exponential backoff with jitter:
//
//	delay = min(baseDelay * 2^attempt, maxDelay) + random jitter
func CalculateBackoff(attempt int, hotCfg *config.HotReloadConfig) time.Duration {
	cfg := hotCfg.Get()

	base := float64(cfg.RetryBaseDelayMS)
	maxDelay := float64(cfg.RetryMaxDelayMS)

	delay := base * math.Pow(2, float64(attempt))
	if delay > maxDelay {
		delay = maxDelay
	}

	jitter := rand.Float64() * float64(cfg.RetryBaseDelayMS) * 0.5

	return time.Duration(delay+jitter) * time.Millisecond
}

// ShouldRetry returns true if the request has attempts remaining.
func ShouldRetry(currentAttempts int, hotCfg *config.HotReloadConfig) bool {
	cfg := hotCfg.Get()
	return currentAttempts < cfg.RetryMaxAttempts
}
