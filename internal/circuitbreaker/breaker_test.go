package circuitbreaker

import (
	"testing"
	"time"
)

func TestBreaker_StateTransitions(t *testing.T) {
	breaker := NewBreaker(3, 100*time.Millisecond)

	// Initially closed
	if breaker.State() != StateClosed {
		t.Errorf("Expected state=Closed, got %v", breaker.State())
	}

	// Record failures to open
	breaker.RecordFailure()
	breaker.RecordFailure()
	if breaker.State() != StateClosed {
		t.Errorf("Expected state=Closed after 2 failures, got %v", breaker.State())
	}

	breaker.RecordFailure()
	if breaker.State() != StateOpen {
		t.Errorf("Expected state=Open after 3 failures, got %v", breaker.State())
	}

	// Wait for timeout
	time.Sleep(150 * time.Millisecond)

	// Should transition to half-open
	if !breaker.Allow() {
		t.Error("Expected Allow() to return true after timeout (half-open)")
	}

	// Record success to close
	breaker.RecordSuccess()
	if breaker.State() != StateClosed {
		t.Errorf("Expected state=Closed after success, got %v", breaker.State())
	}
}

func TestBreaker_OpenState(t *testing.T) {
	breaker := NewBreaker(2, 100*time.Millisecond)

	// Open the breaker
	breaker.RecordFailure()
	breaker.RecordFailure()

	if breaker.Allow() {
		t.Error("Expected Allow() to return false when open")
	}
}
