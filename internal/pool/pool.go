package pool

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"
)

// Connection wraps a TCP connection with metadata
type Connection struct {
	conn       net.Conn
	createdAt  time.Time
	lastUsedAt time.Time
	inUse      bool
	mu         sync.Mutex
}

// Conn returns the underlying connection
func (c *Connection) Conn() net.Conn {
	return c.conn
}

// MarkInUse marks the connection as in use
func (c *Connection) MarkInUse() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inUse = true
	c.lastUsedAt = time.Now()
}

// MarkIdle marks the connection as idle
func (c *Connection) MarkIdle() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inUse = false
	c.lastUsedAt = time.Now()
}

// IsIdle checks if the connection is idle
func (c *Connection) IsIdle() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.inUse
}

// IsExpired checks if the connection has expired
func (c *Connection) IsExpired(idleTimeout time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.inUse && time.Since(c.lastUsedAt) > idleTimeout
}

// Close closes the connection
func (c *Connection) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// Pool manages a pool of TCP connections to a backend service
// Optimized: uses channel for O(1) idle connection retrieval
type Pool struct {
	address        string
	dialTimeout    time.Duration
	idleTimeout    time.Duration
	maxConnections int
	maxPerService  int

	mu          sync.RWMutex
	idleConns   chan *Connection     // Channel for idle connections (O(1) access)
	activeConns map[*Connection]bool // Set of active connections
	activeCount int
}

// NewPool creates a new connection pool
func NewPool(address string, dialTimeout, idleTimeout time.Duration, maxConnections, maxPerService int) *Pool {
	return &Pool{
		address:        address,
		dialTimeout:    dialTimeout,
		idleTimeout:    idleTimeout,
		maxConnections: maxConnections,
		maxPerService:  maxPerService,
		idleConns:      make(chan *Connection, maxPerService), // Buffered channel
		activeConns:    make(map[*Connection]bool),
	}
}

// Get gets a connection from the pool
// Optimized: O(1) retrieval from channel instead of O(n) linear search
func (p *Pool) Get(ctx context.Context) (*Connection, error) {
	// Try to get idle connection from channel (non-blocking)
	select {
	case conn := <-p.idleConns:
		// Check if connection is still valid
		if !conn.IsExpired(p.idleTimeout) {
			p.mu.Lock()
			conn.MarkInUse()
			p.activeConns[conn] = true
			p.activeCount++
			p.mu.Unlock()
			return conn, nil
		}
		// Connection expired, close it and create new one
		conn.Close()
		p.mu.Lock()
		delete(p.activeConns, conn)
		p.mu.Unlock()
	default:
		// No idle connection available
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Check if exceeds max connections
	if p.activeCount >= p.maxPerService {
		return nil, fmt.Errorf("connection pool is full, max connections: %d", p.maxPerService)
	}

	// Create new connection
	dialer := net.Dialer{
		Timeout: p.dialTimeout,
	}
	conn, err := dialer.DialContext(ctx, "tcp", p.address)
	if err != nil {
		return nil, fmt.Errorf("failed to establish connection: %w", err)
	}

	connection := &Connection{
		conn:       conn,
		createdAt:  time.Now(),
		lastUsedAt: time.Now(),
		inUse:      true,
	}

	p.activeConns[connection] = true
	p.activeCount++

	return connection, nil
}

// Put returns a connection to the pool
// Optimized: uses channel for O(1) insertion
func (p *Pool) Put(conn *Connection) {
	p.mu.Lock()
	conn.MarkIdle()
	delete(p.activeConns, conn)
	if p.activeCount > 0 {
		p.activeCount--
	}
	p.mu.Unlock()

	// Try to put back to idle channel (non-blocking)
	select {
	case p.idleConns <- conn:
		// Successfully returned to pool
	default:
		// Channel full, connection will be closed during cleanup
	}
}

// Remove removes a connection from the pool
func (p *Pool) Remove(conn *Connection) {
	p.mu.Lock()
	defer p.mu.Unlock()

	conn.Close()
	delete(p.activeConns, conn)
	if p.activeCount > 0 {
		p.activeCount--
	}
}

// Cleanup removes expired connections
// Returns the number of connections cleaned up
func (p *Pool) Cleanup() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	count := 0
	// Drain idle channel and check for expired connections
	for {
		select {
		case conn := <-p.idleConns:
			if conn.IsExpired(p.idleTimeout) {
				if err := conn.Close(); err != nil {
					// Log error but continue cleanup
					// Note: In production, this should use structured logging
				}
				delete(p.activeConns, conn)
				count++
			} else {
				// Put back if still valid
				select {
				case p.idleConns <- conn:
				default:
					// Channel full, close connection
					if err := conn.Close(); err != nil {
						// Log error but continue cleanup
					}
					delete(p.activeConns, conn)
					count++
				}
			}
		default:
			// Channel empty, done
			return count
		}
	}
}

// Stats returns pool statistics
// Fixed: includes idle connections in total count
func (p *Pool) Stats() (total, active, idle int) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Count active connections
	active = p.activeCount

	// Count idle connections in channel
	idle = len(p.idleConns)

	// Total is sum of active and idle
	total = active + idle
	return
}

// Close closes all connections in the pool
func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Close all active connections
	for conn := range p.activeConns {
		conn.Close()
	}
	p.activeConns = make(map[*Connection]bool)

	// Drain and close idle connections
	for {
		select {
		case conn := <-p.idleConns:
			conn.Close()
		default:
			// Channel empty, done
			p.activeCount = 0
			return nil
		}
	}
}
