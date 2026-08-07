package devlite

import "sync"

// EventQueue is a bounded FIFO. When full it drops the oldest events first
// (better to lose old data than crash the host or grow memory unbounded) —
// the same semantics as the Node and Python SDKs.
type EventQueue struct {
	maxSize      int
	mu           sync.Mutex
	items        []map[string]any
	droppedCount int
}

func newEventQueue(maxSize int) *EventQueue {
	if maxSize <= 0 {
		maxSize = 5000
	}
	return &EventQueue{maxSize: maxSize}
}

func (q *EventQueue) Push(event map[string]any) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) >= q.maxSize {
		q.items = q.items[1:]
		q.droppedCount++
	}
	q.items = append(q.items, event)
}

func (q *EventQueue) Drain(maxBatchSize int) []map[string]any {
	q.mu.Lock()
	defer q.mu.Unlock()
	if maxBatchSize <= 0 {
		maxBatchSize = q.maxSize
	}
	if len(q.items) > maxBatchSize {
		batch := q.items[:maxBatchSize]
		q.items = append([]map[string]any(nil), q.items[maxBatchSize:]...)
		return batch
	}
	batch := q.items
	q.items = nil
	return batch
}

func (q *EventQueue) Size() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

func (q *EventQueue) Dropped() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.droppedCount
}
