package session

import (
	"net"
	"sync"
	"time"
)

// Session represents a client session
type Session struct {
	// SessionID is a long integer (not string)
	SessionID int64

	// ClientConn is the connection to client
	ClientConn net.Conn

	// BackendConn is the connection to backend service (AccountServer/VersionServer/GameServer)
	BackendConn net.Conn

	// UserID is the user identifier
	UserID int64

	// RealmID is the realm identifier (for Game service)
	RealmID int32

	// ServiceType is the service type from config (e.g., "account", "version", "game")
	ServiceType string

	// WorldID is the world ID from ServerID
	WorldID int32

	// InstID is the instance ID from ServerID
	InstID int32

	// CreatedAt is the session creation time
	CreatedAt time.Time

	// LastActiveAt is the last active time
	LastActiveAt time.Time

	// State is the session state
	State SessionState
}

// SessionState represents session state
type SessionState int

const (
	SessionStateConnecting SessionState = iota
	SessionStateConnected
	SessionStateDisconnecting
	SessionStateClosed
)

// Manager manages client sessions in memory
// Optimized: uses sharded maps to reduce lock contention
type Manager struct {
	shards [16]*SessionShard // 16 shards for better concurrency
}

// SessionShard is a shard of the session map
type SessionShard struct {
	mu       sync.RWMutex
	sessions map[int64]*Session
}

// NewManager creates a new session manager
func NewManager() *Manager {
	m := &Manager{}
	for i := range m.shards {
		m.shards[i] = &SessionShard{
			sessions: make(map[int64]*Session),
		}
	}
	return m
}

// getShard returns the shard for a given session ID
func (m *Manager) getShard(sessionID int64) *SessionShard {
	// Use low 4 bits for shard selection (16 shards)
	return m.shards[sessionID&0xF]
}

// Add adds a session
func (m *Manager) Add(session *Session) {
	shard := m.getShard(session.SessionID)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	shard.sessions[session.SessionID] = session
}

// Get gets a session by session ID
func (m *Manager) Get(sessionID int64) (*Session, bool) {
	shard := m.getShard(sessionID)
	shard.mu.RLock()
	defer shard.mu.RUnlock()
	session, ok := shard.sessions[sessionID]
	return session, ok
}

// Remove removes a session
func (m *Manager) Remove(sessionID int64) {
	shard := m.getShard(sessionID)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	delete(shard.sessions, sessionID)
}

// UpdateLastActive updates the last active time of a session
func (m *Manager) UpdateLastActive(sessionID int64) {
	shard := m.getShard(sessionID)
	shard.mu.RLock()
	defer shard.mu.RUnlock()
	if session, ok := shard.sessions[sessionID]; ok {
		session.LastActiveAt = time.Now()
	}
}

// Count returns the number of active sessions
func (m *Manager) Count() int {
	total := 0
	for _, shard := range m.shards {
		shard.mu.RLock()
		total += len(shard.sessions)
		shard.mu.RUnlock()
	}
	return total
}

// CleanupIdle removes idle sessions that exceed the timeout
func (m *Manager) CleanupIdle(idleTimeout time.Duration) int {
	now := time.Now()
	totalCount := 0

	for _, shard := range m.shards {
		shard.mu.Lock()
		count := 0
		for sessionID, session := range shard.sessions {
			if now.Sub(session.LastActiveAt) > idleTimeout {
				// Close connections
				if session.ClientConn != nil {
					session.ClientConn.Close()
				}
				if session.BackendConn != nil {
					session.BackendConn.Close()
				}
				delete(shard.sessions, sessionID)
				count++
			}
		}
		shard.mu.Unlock()
		totalCount += count
	}

	return totalCount
}

// GetAll returns all sessions (for monitoring)
func (m *Manager) GetAll() []*Session {
	allSessions := make([]*Session, 0)
	for _, shard := range m.shards {
		shard.mu.RLock()
		for _, session := range shard.sessions {
			allSessions = append(allSessions, session)
		}
		shard.mu.RUnlock()
	}
	return allSessions
}
