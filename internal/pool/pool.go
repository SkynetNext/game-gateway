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
type Pool struct {
	address        string
	dialTimeout    time.Duration
	idleTimeout    time.Duration
	maxConnections int
	maxPerService  int

	mu          sync.RWMutex
	connections []*Connection
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
		connections:    make([]*Connection, 0),
	}
}

// Get gets a connection from the pool
func (p *Pool) Get(ctx context.Context) (*Connection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Try to reuse idle connection
	for _, conn := range p.connections {
		if conn.IsIdle() && !conn.IsExpired(p.idleTimeout) {
			conn.MarkInUse()
			p.activeCount++
			return conn, nil
		}
	}

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

	p.connections = append(p.connections, connection)
	p.activeCount++

	return connection, nil
}

// Put returns a connection to the pool
func (p *Pool) Put(conn *Connection) {
	p.mu.Lock()
	defer p.mu.Unlock()

	conn.MarkIdle()
	if p.activeCount > 0 {
		p.activeCount--
	}
}

// Remove removes a connection from the pool
func (p *Pool) Remove(conn *Connection) {
	p.mu.Lock()
	defer p.mu.Unlock()

	conn.Close()
	for i, c := range p.connections {
		if c == conn {
			p.connections = append(p.connections[:i], p.connections[i+1:]...)
			if p.activeCount > 0 {
				p.activeCount--
			}
			break
		}
	}
}

// Cleanup removes expired connections
func (p *Pool) Cleanup() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	count := 0
	valid := make([]*Connection, 0, len(p.connections))

	for _, conn := range p.connections {
		if conn.IsExpired(p.idleTimeout) {
			conn.Close()
			count++
			if conn.inUse {
				p.activeCount--
			}
		} else {
			valid = append(valid, conn)
		}
	}

	p.connections = valid
	return count
}

// Stats returns pool statistics
func (p *Pool) Stats() (total, active, idle int) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	total = len(p.connections)
	active = p.activeCount
	idle = total - active
	return
}

// Close closes all connections in the pool
func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, conn := range p.connections {
		conn.Close()
	}
	p.connections = nil
	p.activeCount = 0
	return nil
}
