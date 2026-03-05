package queue

import (
	"container/heap"
	"errors"
	"sync"

	"github.com/rs/zerolog"
)

var (
	ErrQueueFull   = errors.New("queue is full")
	ErrQueueClosed = errors.New("queue is closed")
)

// Queue is a thread-safe priority queue backed by a heap.
// Workers receive items through the Dequeue channel.
//
// Flow:
//
//	API handler → Enqueue(item) → heap → internal goroutine → notify channel → Worker picks up
type Queue struct {
	mu       sync.Mutex
	pq       priorityQueue
	maxSize  int
	closed   bool
	preempt  bool // if true, high-priority items can bump low-priority ones when full
	notify   chan struct{}
	dequeue  chan *Item
	logger   zerolog.Logger
	stopOnce sync.Once
	done     chan struct{}
}

// NewQueue creates a queue with the given max size and starts the dispatch loop.
//
//	maxSize  — maximum number of items the queue can hold
//	preempt  — when queue is full, a high-priority item can evict the lowest-priority item
//	logger   — structured logger
func NewQueue(maxSize int, preempt bool, logger zerolog.Logger) *Queue {
	q := &Queue{
		pq:      make(priorityQueue, 0),
		maxSize: maxSize,
		preempt: preempt,
		notify:  make(chan struct{}, 1), // buffered so sends don't block
		dequeue: make(chan *Item),       // unbuffered — workers block here
		logger:  logger.With().Str("component", "queue").Logger(),
		done:    make(chan struct{}),
	}
	heap.Init(&q.pq)

	go q.dispatchLoop()

	return q
}

// Enqueue adds an item to the priority queue.
// If the queue is full and preempt is enabled, the lowest-priority item
// is evicted to make room (only if the new item has higher priority).
func (q *Queue) Enqueue(item *Item) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return ErrQueueClosed
	}

	if q.pq.Len() >= q.maxSize {
		if !q.preempt {
			return ErrQueueFull
		}

		// Find the lowest-priority item (highest Priority number, oldest last)
		// and evict it only if the new item has strictly higher priority
		worst := q.findLowestPriority()
		if worst == nil || item.Priority >= worst.Priority {
			// New item is same or lower priority than worst — reject it
			return ErrQueueFull
		}

		q.logger.Warn().
			Str("evicted_request", worst.RequestID).
			Int("evicted_priority", worst.Priority).
			Str("new_request", item.RequestID).
			Int("new_priority", item.Priority).
			Msg("preempting low-priority request")

		heap.Remove(&q.pq, worst.index)
	}

	heap.Push(&q.pq, item)

	// Notify the dispatch loop that there's work
	select {
	case q.notify <- struct{}{}:
	default:
		// Already notified, no need to send again
	}

	return nil
}

// Dequeue returns the channel that workers read from.
// Each item comes out in priority order (highest priority first, FIFO within same priority).
func (q *Queue) Dequeue() <-chan *Item {
	return q.dequeue
}

// Size returns the current number of items in the queue.
func (q *Queue) Size() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.pq.Len()
}

// Close shuts down the queue. No more items can be enqueued.
// The dispatch loop drains remaining items before stopping.
func (q *Queue) Close() {
	q.stopOnce.Do(func() {
		q.mu.Lock()
		q.closed = true
		q.mu.Unlock()

		close(q.done)
	})
}

// dispatchLoop runs in a goroutine. It waits for notifications
// that items have been added, then pops the highest-priority item
// and sends it to the dequeue channel for a worker to pick up.
func (q *Queue) dispatchLoop() {
	for {
		// Wait for either a notification or shutdown
		select {
		case <-q.notify:
		case <-q.done:
			q.drainRemaining()
			close(q.dequeue)
			return
		}

		// Pop and send items until the heap is empty
		for {
			q.mu.Lock()
			if q.pq.Len() == 0 {
				q.mu.Unlock()
				break
			}
			item := heap.Pop(&q.pq).(*Item)
			q.mu.Unlock()

			// Send to worker — this blocks until a worker is ready
			select {
			case q.dequeue <- item:
			case <-q.done:
				q.drainRemaining()
				close(q.dequeue)
				return
			}
		}
	}
}

// drainRemaining sends any leftover items so workers can finish them.
func (q *Queue) drainRemaining() {
	q.mu.Lock()
	defer q.mu.Unlock()

	for q.pq.Len() > 0 {
		item := heap.Pop(&q.pq).(*Item)
		select {
		case q.dequeue <- item:
		default:
			// No worker available, item is lost
			q.logger.Warn().
				Str("request_id", item.RequestID).
				Msg("dropping item during queue shutdown — no worker available")
		}
	}
}

// findLowestPriority scans the heap for the item with the highest Priority number
// (lowest actual priority). Among ties, picks the newest (last created).
// Must be called with q.mu held.
func (q *Queue) findLowestPriority() *Item {
	if q.pq.Len() == 0 {
		return nil
	}

	var worst *Item
	for _, item := range q.pq {
		if worst == nil ||
			item.Priority > worst.Priority ||
			(item.Priority == worst.Priority && item.CreatedAt.After(worst.CreatedAt)) {
			worst = item
		}
	}
	return worst
}
