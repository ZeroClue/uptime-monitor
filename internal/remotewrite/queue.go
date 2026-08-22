package remotewrite

import (
	"sync"
	"sync/atomic"
)

// queuedSample is one sample awaiting export, enriched with the host name
// so the flush path can label series without touching storage.
type queuedSample struct {
	HostName    string
	Metric      string
	Value       float64
	TimestampMs int64
	Collector   string
}

// Queue is a bounded FIFO that drops its oldest entries on overflow, so a
// slow or down remote endpoint can never grow memory unboundedly.
type Queue struct {
	mu      sync.Mutex
	items   []queuedSample
	cap     int
	dropped atomic.Int64
}

func NewQueue(capacity int) *Queue {
	return &Queue{items: make([]queuedSample, 0, capacity), cap: capacity}
}

func (q *Queue) Push(s queuedSample) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) >= q.cap {
		// Drop the oldest sample; the freshest state wins.
		copy(q.items, q.items[1:])
		q.items[len(q.items)-1] = s
		q.dropped.Add(1)
		return
	}
	q.items = append(q.items, s)
}

func (q *Queue) PopBatch(max int) []queuedSample {
	q.mu.Lock()
	defer q.mu.Unlock()
	if max > len(q.items) {
		max = len(q.items)
	}
	batch := make([]queuedSample, max)
	copy(batch, q.items[:max])
	rest := copy(q.items, q.items[max:])
	q.items = q.items[:rest]
	return batch
}

func (q *Queue) Depth() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

func (q *Queue) DroppedTotal() int64 { return q.dropped.Load() }
