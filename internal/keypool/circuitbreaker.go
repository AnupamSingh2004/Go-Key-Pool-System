package keypool

import (
	"sync"
	"time"

	"key-pool-system/internal/db"
)

// CircuitBreaker tracks failures for a single API key and controls
// whether that key should be used for requests.
//
// State machine:
//
//	CLOSED  →  (failures >= threshold)  →  OPEN
//	OPEN    →  (openDuration elapsed)   →  HALF_OPEN
//	HALF_OPEN → (probe succeeds)        →  CLOSED
//	HALF_OPEN → (probe fails)           →  OPEN
type CircuitBreaker struct {
	mu sync.Mutex

	state        string
	failureCount int
	lastFailedAt time.Time

	// Configurable thresholds
	failureThreshold int
	openDuration     time.Duration
}

func NewCircuitBreaker(failureThreshold int, openDuration time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:            db.CircuitStateClosed,
		failureThreshold: failureThreshold,
		openDuration:     openDuration,
	}
}

// AllowRequest decides if this key should accept a new request.
//
//	CLOSED    → always allow
//	OPEN      → allow only if openDuration has passed (transition to HALF_OPEN)
//	HALF_OPEN → allow (this is the probe request)
func (cb *CircuitBreaker) AllowRequest() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case db.CircuitStateClosed:
		return true

	case db.CircuitStateOpen:
		if time.Since(cb.lastFailedAt) >= cb.openDuration {
			cb.state = db.CircuitStateHalfOpen
			return true
		}
		return false

	case db.CircuitStateHalfOpen:
		return true

	default:
		return false
	}
}

// RecordSuccess resets the circuit breaker back to closed state.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount = 0
	cb.state = db.CircuitStateClosed
}

// RecordFailure increments the failure count and opens the circuit
// if the threshold is reached.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount++
	cb.lastFailedAt = time.Now().UTC()

	if cb.failureCount >= cb.failureThreshold {
		cb.state = db.CircuitStateOpen
	}
}

// State returns the current circuit state.
func (cb *CircuitBreaker) State() string {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// FailureCount returns the current failure count.
func (cb *CircuitBreaker) FailureCount() int {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.failureCount
}

// Reset puts the circuit breaker back to closed with zero failures.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.state = db.CircuitStateClosed
	cb.failureCount = 0
	cb.lastFailedAt = time.Time{}
}
