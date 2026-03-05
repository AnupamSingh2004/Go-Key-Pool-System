package keypool

// KeySelectionStrategy defines how the next key is picked from the pool
//
// Two implementations exist:
//   - RoundRobinStrategy: picks keys in order, cycling through them
//   - WeightedRoundRobinStrategy: picks keys based on their weight
//     (higher weight = picked more often)
type KeySelectionStrategy interface {
	// Name returns the strategy identifier
	Name() string

	// Select picks the next key from the available keys
	// Returns nil if no keys are available
	Select(keys []*PoolKey) *PoolKey

	// Reset re-initializes internal state (called when key list changes)
	Reset()
}
