package ratelimit

import (
	"sync"
	"testing"
)

func TestLimiter_Allow(t *testing.T) {
	limiter := NewLimiter(10)

	// Test basic allow/release
	if !limiter.Allow() {
		t.Error("Expected Allow() to return true")
	}
	if limiter.Current() != 1 {
		t.Errorf("Expected current=1, got %d", limiter.Current())
	}

	limiter.Release()
	if limiter.Current() != 0 {
		t.Errorf("Expected current=0, got %d", limiter.Current())
	}
}

func TestLimiter_Concurrent(t *testing.T) {
	limiter := NewLimiter(100)
	var wg sync.WaitGroup

	// Test concurrent access
	for i := 0; i < 1000; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if limiter.Allow() {
				defer limiter.Release()
			}
		}()
	}

	wg.Wait()

	// After all releases, current should be 0
	if limiter.Current() != 0 {
		t.Errorf("Expected current=0 after all releases, got %d", limiter.Current())
	}
}

func TestLimiter_MaxConnections(t *testing.T) {
	limiter := NewLimiter(5)

	// Fill up to max
	for i := 0; i < 5; i++ {
		if !limiter.Allow() {
			t.Errorf("Expected Allow() to return true for connection %d", i)
		}
	}

	// Next one should fail
	if limiter.Allow() {
		t.Error("Expected Allow() to return false when at max")
	}
}
