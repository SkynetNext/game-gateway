package ratelimit

import (
	"sync"
	"time"
)

// IPLimiter limits connections per IP address
type IPLimiter struct {
	maxConnsPerIP int
	rateLimit     int // connections per second per IP

	mu          sync.RWMutex
	ipConns     map[string]int64        // IP -> current connection count
	ipRates     map[string]*rateTracker // IP -> rate tracker
	lastCleanup time.Time
}

type rateTracker struct {
	mu          sync.Mutex
	connections []time.Time // Timestamps of recent connections
	window      time.Duration
}

// NewIPLimiter creates a new IP-based rate limiter
func NewIPLimiter(maxConnsPerIP, rateLimit int) *IPLimiter {
	return &IPLimiter{
		maxConnsPerIP: maxConnsPerIP,
		rateLimit:     rateLimit,
		ipConns:       make(map[string]int64),
		ipRates:       make(map[string]*rateTracker),
		lastCleanup:   time.Now(),
	}
}

// Allow checks if a connection from IP is allowed
func (l *IPLimiter) Allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Cleanup old entries periodically (every 5 minutes)
	if time.Since(l.lastCleanup) > 5*time.Minute {
		l.cleanup()
		l.lastCleanup = time.Now()
	}

	// Check connection count limit
	current := l.ipConns[ip]
	if current >= int64(l.maxConnsPerIP) {
		return false
	}

	// Check rate limit
	rate := l.getOrCreateRateTracker(ip)
	rate.mu.Lock()
	defer rate.mu.Unlock()

	now := time.Now()
	// Remove old entries outside the window
	cutoff := now.Add(-1 * time.Second)
	valid := 0
	for _, ts := range rate.connections {
		if ts.After(cutoff) {
			rate.connections[valid] = ts
			valid++
		}
	}
	rate.connections = rate.connections[:valid]

	// Check if rate limit exceeded
	if len(rate.connections) >= l.rateLimit {
		return false
	}

	// Allow connection
	rate.connections = append(rate.connections, now)
	l.ipConns[ip]++
	return true
}

// Release releases a connection slot for an IP
func (l *IPLimiter) Release(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if count, ok := l.ipConns[ip]; ok && count > 0 {
		l.ipConns[ip] = count - 1
		if l.ipConns[ip] == 0 {
			delete(l.ipConns, ip)
		}
	}
}

// getOrCreateRateTracker gets or creates a rate tracker for an IP
func (l *IPLimiter) getOrCreateRateTracker(ip string) *rateTracker {
	rate, ok := l.ipRates[ip]
	if !ok {
		rate = &rateTracker{
			connections: make([]time.Time, 0, l.rateLimit),
			window:      time.Second,
		}
		l.ipRates[ip] = rate
	}
	return rate
}

// cleanup removes old entries from the limiter
func (l *IPLimiter) cleanup() {
	// Remove IPs with zero connections
	for ip, count := range l.ipConns {
		if count == 0 {
			delete(l.ipConns, ip)
			delete(l.ipRates, ip)
		}
	}
}

// GetStats returns statistics for an IP
func (l *IPLimiter) GetStats(ip string) (connCount int64, rateCount int) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	connCount = l.ipConns[ip]
	if rate, ok := l.ipRates[ip]; ok {
		rate.mu.Lock()
		rateCount = len(rate.connections)
		rate.mu.Unlock()
	}
	return
}
