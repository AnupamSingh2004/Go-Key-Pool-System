package queue

import "time"

// Item wraps a request ID with metadata needed for priority ordering.
// The actual request data lives in the database - the queue only tracks
// what needs to be processed and in what order.
type Item struct {
	RequestID string
	Priority  int
	CreatedAt time.Time
	index     int
}
