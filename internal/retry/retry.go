package retry

import (
	"context"
	"fmt"
	"time"
)

// RetryConfig represents retry configuration
type RetryConfig struct {
	MaxRetries int
	RetryDelay time.Duration
}

// Do executes a function with retry logic
func Do(ctx context.Context, cfg RetryConfig, fn func() error) error {
	var lastErr error
	for i := 0; i < cfg.MaxRetries; i++ {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		err := fn()
		if err == nil {
			return nil
		}

		lastErr = err

		// Don't retry on last attempt
		if i < cfg.MaxRetries-1 {
			// Exponential backoff: delay * 2^i
			delay := time.Duration(1<<uint(i)) * cfg.RetryDelay
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
	}

	return fmt.Errorf("failed after %d retries: %w", cfg.MaxRetries, lastErr)
}
