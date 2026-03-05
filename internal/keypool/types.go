package keypool

import (
	"sync"
	"time"
)

// PoolKey represents an API key loaded into the pool with runtime state
type PoolKey struct {
	ID                 string
	Name               string
	KeyEncrypted       string
	Weight             int
	RateLimitPerMinute int
	RateLimitPerDay    int
	ConcurrentLimit    int

	// Runtime state (managed by the pool, not stored in DB until flush)
	mu                sync.Mutex
	currentConcurrent int
	minuteCounter     int
	dayCounter        int
	minuteResetAt     time.Time
	dayResetAt        time.Time
	circuitBreaker    *CircuitBreaker
}

// TryConcurrentAcquire attempts to increment the concurrent counter.
// Returns true if under the limit, false if at capacity.
func (k *PoolKey) TryConcurrentAcquire() bool {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.currentConcurrent >= k.ConcurrentLimit {
		return false
	}
	k.currentConcurrent++
	return true
}

// ConcurrentRelease decrements the concurrent counter.
func (k *PoolKey) ConcurrentRelease() {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.currentConcurrent > 0 {
		k.currentConcurrent--
	}
}

// TryRateLimit checks if the key is within its rate limits.
// Returns true if allowed, false if rate limited.
func (k *PoolKey) TryRateLimit() bool {
	k.mu.Lock()
	defer k.mu.Unlock()

	now := time.Now().UTC()

	// Reset minute counter if window expired
	if now.After(k.minuteResetAt) {
		k.minuteCounter = 0
		k.minuteResetAt = now.Add(time.Minute)
	}

	// Reset day counter if window expired
	if now.After(k.dayResetAt) {
		k.dayCounter = 0
		k.dayResetAt = now.Add(24 * time.Hour)
	}

	// Check limits
	if k.minuteCounter >= k.RateLimitPerMinute {
		return false
	}
	if k.dayCounter >= k.RateLimitPerDay {
		return false
	}

	k.minuteCounter++
	k.dayCounter++
	return true
}

// GetCurrentConcurrent returns the current concurrent usage count.
func (k *PoolKey) GetCurrentConcurrent() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.currentConcurrent
}
