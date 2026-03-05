package keypool

import "sync"

// WeightedRoundRobinStrategy selects keys based on their weight
//
// Keys with higher weight get picked more often.
// Uses a smooth weighted round robin algorithm so selections
// are spread evenly rather than clustered.
//
// Example with keys: A(weight=3), B(weight=1)
//
//	The selection pattern would be: A, A, B, A, A, A, B, A ...
//	(A gets picked ~3x more often than B)
type WeightedRoundRobinStrategy struct {
	mu             sync.Mutex
	currentWeights map[string]int // key ID → current accumulated weight
}

func NewWeightedRoundRobinStrategy() *WeightedRoundRobinStrategy {
	return &WeightedRoundRobinStrategy{
		currentWeights: make(map[string]int),
	}
}

func (w *WeightedRoundRobinStrategy) Name() string {
	return "weighted_round_robin"
}

// Select uses smooth weighted round robin:
//  1. Add each key's configured weight to its current weight
//  2. Pick the key with highest current weight
//  3. Subtract total weight from the picked key's current weight
//
// This produces an evenly distributed pattern rather than bursts.
func (w *WeightedRoundRobinStrategy) Select(keys []*PoolKey) *PoolKey {
	if len(keys) == 0 {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// Calculate total weight and update current weights
	totalWeight := 0
	for _, key := range keys {
		totalWeight += key.Weight
		w.currentWeights[key.ID] += key.Weight
	}

	// Find the key with the highest current weight
	var selected *PoolKey
	maxWeight := -1
	for _, key := range keys {
		if w.currentWeights[key.ID] > maxWeight {
			maxWeight = w.currentWeights[key.ID]
			selected = key
		}
	}

	if selected == nil {
		return nil
	}

	// Subtract total weight from selected key's current weight
	w.currentWeights[selected.ID] -= totalWeight

	return selected
}

func (w *WeightedRoundRobinStrategy) Reset() {
	w.mu.Lock()
	w.currentWeights = make(map[string]int)
	w.mu.Unlock()
}
