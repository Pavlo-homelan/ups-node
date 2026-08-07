package webapp

import (
	"sync"
	"time"
)

type attemptWindow struct {
	mu      sync.Mutex
	entries map[string]attemptEntry
	now     func() time.Time
}

type attemptEntry struct {
	start time.Time
	count int
}

func newAttemptWindow() *attemptWindow {
	return &attemptWindow{entries: map[string]attemptEntry{}, now: time.Now}
}

func (limiter *attemptWindow) Allow(key string, maximum int, window time.Duration) bool {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := limiter.now()
	entry := limiter.entries[key]
	if entry.start.IsZero() || now.Sub(entry.start) >= window {
		limiter.entries[key] = attemptEntry{start: now, count: 1}
		return true
	}
	if entry.count >= maximum {
		return false
	}
	entry.count++
	limiter.entries[key] = entry
	if len(limiter.entries) > 1024 {
		for candidate, value := range limiter.entries {
			if now.Sub(value.start) >= window {
				delete(limiter.entries, candidate)
			}
		}
	}
	return true
}

type streamLimiter struct {
	mu        sync.Mutex
	total     int
	perClient map[string]int
	maxTotal  int
	maxClient int
}

func newStreamLimiter(maxTotal int, maxClient int) *streamLimiter {
	return &streamLimiter{perClient: map[string]int{}, maxTotal: maxTotal, maxClient: maxClient}
}

func (limiter *streamLimiter) Acquire(client string) (func(), bool) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.total >= limiter.maxTotal || limiter.perClient[client] >= limiter.maxClient {
		return nil, false
	}
	limiter.total++
	limiter.perClient[client]++
	return func() {
		limiter.mu.Lock()
		defer limiter.mu.Unlock()
		limiter.total--
		limiter.perClient[client]--
		if limiter.perClient[client] == 0 {
			delete(limiter.perClient, client)
		}
	}, true
}
