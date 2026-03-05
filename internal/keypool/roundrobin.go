package keypool

import "sync"

// RoundRobinStrategy selects keys in a simple rotating order
//
// Example with 3 keys [A, B, C]:
//
//	Call 1 → A
//	Call 2 → B
//	Call 3 → C
//	Call 4 → A (wraps around)
type RoundRobinStrategy struct {
	mu      sync.Mutex
	counter uint64
}

func NewRoundRobinStrategy() *RoundRobinStrategy {
	return &RoundRobinStrategy{}
}

func (r *RoundRobinStrategy) Name() string {
	return "round_robin"
}

func (r *RoundRobinStrategy) Select(keys []*PoolKey) *PoolKey {
	if len(keys) == 0 {
		return nil
	}

	r.mu.Lock()
	idx := r.counter % uint64(len(keys))
	r.counter++
	r.mu.Unlock()

	return keys[idx]
}

func (r *RoundRobinStrategy) Reset() {
	r.mu.Lock()
	r.counter = 0
	r.mu.Unlock()
}
