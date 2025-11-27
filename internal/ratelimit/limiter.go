package ratelimit

import (
	"sync/atomic"
)

// Limiter limits the number of concurrent connections
type Limiter struct {
	maxConns int64
	current  int64
	// Note: mu removed - using atomic operations for better performance
}

// NewLimiter creates a new rate limiter
func NewLimiter(maxConns int64) *Limiter {
	return &Limiter{
		maxConns: maxConns,
	}
}

// Allow checks if a new connection is allowed
// Fixed: uses atomic CompareAndSwap for thread-safe increment
func (l *Limiter) Allow() bool {
	for {
		current := atomic.LoadInt64(&l.current)
		if current >= l.maxConns {
			return false
		}
		// Atomically increment if current value hasn't changed
		if atomic.CompareAndSwapInt64(&l.current, current, current+1) {
			return true
		}
		// Retry if CAS failed (another goroutine modified the value)
	}
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
