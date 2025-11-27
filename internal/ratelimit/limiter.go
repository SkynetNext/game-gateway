package ratelimit

import (
	"sync"
	"sync/atomic"
)

// Limiter limits the number of concurrent connections
type Limiter struct {
	maxConns int64
	current  int64
	mu       sync.Mutex
}

// NewLimiter creates a new rate limiter
func NewLimiter(maxConns int64) *Limiter {
	return &Limiter{
		maxConns: maxConns,
	}
}

// Allow checks if a new connection is allowed
func (l *Limiter) Allow() bool {
	current := atomic.LoadInt64(&l.current)
	if current >= l.maxConns {
		return false
	}
	atomic.AddInt64(&l.current, 1)
	return true
}

// Release releases a connection slot
func (l *Limiter) Release() {
	atomic.AddInt64(&l.current, -1)
}

// Current returns the current number of connections
func (l *Limiter) Current() int64 {
	return atomic.LoadInt64(&l.current)
}

// Max returns the maximum allowed connections
func (l *Limiter) Max() int64 {
	return l.maxConns
}
