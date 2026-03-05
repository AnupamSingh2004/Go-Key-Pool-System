package queue

// priorityQueue implements container/heap.Interface for Item pointers.
//
// Ordering rules:
//  1. Lower Priority number wins (1=High beats 3=Low)
//  2. If same priority, older CreatedAt wins (FIFO within tier)
//
// This is a min-heap: the "smallest" item (highest priority, oldest) is at index 0.
type priorityQueue []*Item

func (pq priorityQueue) Len() int { return len(pq) }

// Less defines ordering: lower priority number wins, then older timestamp wins.
func (pq priorityQueue) Less(i, j int) bool {
	if pq[i].Priority != pq[j].Priority {
		return pq[i].Priority < pq[j].Priority
	}
	return pq[i].CreatedAt.Before(pq[j].CreatedAt)
}

func (pq priorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

// Push adds an item to the heap. Called by heap.Push, not directly.
func (pq *priorityQueue) Push(x any) {
	item := x.(*Item)
	item.index = len(*pq)
	*pq = append(*pq, item)
}

// Pop removes and returns the highest-priority item. Called by heap.Pop, not directly.
func (pq *priorityQueue) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil  // avoid memory leak
	item.index = -1 // mark as removed
	*pq = old[:n-1]
	return item
}
