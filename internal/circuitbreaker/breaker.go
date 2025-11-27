package circuitbreaker

import (
	"sync"
	"sync/atomic"
	"time"
)

// State represents circuit breaker state
type State int32

const (
	StateClosed State = iota
	StateOpen
	StateHalfOpen
)

// Breaker implements circuit breaker pattern
type Breaker struct {
	maxFailures int64
	timeout     time.Duration
	mu          sync.RWMutex
	state       int32 // State (atomic)
	failures    int64 // Failure count (atomic)
	lastFailure time.Time
}

// NewBreaker creates a new circuit breaker
func NewBreaker(maxFailures int64, timeout time.Duration) *Breaker {
	return &Breaker{
		maxFailures: maxFailures,
		timeout:     timeout,
		state:       int32(StateClosed),
	}
}

// Allow checks if the circuit breaker allows the request
func (b *Breaker) Allow() bool {
	state := State(atomic.LoadInt32(&b.state))

	switch state {
	case StateClosed:
		return true
	case StateOpen:
		b.mu.RLock()
		lastFailure := b.lastFailure
		b.mu.RUnlock()
		if time.Since(lastFailure) >= b.timeout {
			// Try to transition to half-open
			if atomic.CompareAndSwapInt32(&b.state, int32(StateOpen), int32(StateHalfOpen)) {
				atomic.StoreInt64(&b.failures, 0)
				return true
			}
		}
		return false
	case StateHalfOpen:
		return true
	default:
		return false
	}
}

// RecordSuccess records a successful request
func (b *Breaker) RecordSuccess() {
	state := State(atomic.LoadInt32(&b.state))
	if state == StateHalfOpen {
		// Transition back to closed
		atomic.StoreInt32(&b.state, int32(StateClosed))
		atomic.StoreInt64(&b.failures, 0)
	}
}

// RecordFailure records a failed request
func (b *Breaker) RecordFailure() {
	failures := atomic.AddInt64(&b.failures, 1)
	b.mu.Lock()
	b.lastFailure = time.Now()
	b.mu.Unlock()

	if failures >= b.maxFailures {
		// Transition to open
		atomic.StoreInt32(&b.state, int32(StateOpen))
	}
}

// State returns the current state
func (b *Breaker) State() State {
	return State(atomic.LoadInt32(&b.state))
}
