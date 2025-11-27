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
type Manager struct {
	mu       sync.RWMutex
	sessions map[int64]*Session
}

// NewManager creates a new session manager
func NewManager() *Manager {
	return &Manager{
		sessions: make(map[int64]*Session),
	}
}

// Add adds a session
func (m *Manager) Add(session *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[session.SessionID] = session
}

// Get gets a session by session ID
func (m *Manager) Get(sessionID int64) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	session, ok := m.sessions[sessionID]
	return session, ok
}

// Remove removes a session
func (m *Manager) Remove(sessionID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, sessionID)
}

// UpdateLastActive updates the last active time of a session
func (m *Manager) UpdateLastActive(sessionID int64) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if session, ok := m.sessions[sessionID]; ok {
		session.LastActiveAt = time.Now()
	}
}

// Count returns the number of active sessions
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

// CleanupIdle removes idle sessions that exceed the timeout
func (m *Manager) CleanupIdle(idleTimeout time.Duration) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	count := 0

	for sessionID, session := range m.sessions {
		if now.Sub(session.LastActiveAt) > idleTimeout {
			// Close connections
			if session.ClientConn != nil {
				session.ClientConn.Close()
			}
			if session.BackendConn != nil {
				session.BackendConn.Close()
			}
			delete(m.sessions, sessionID)
			count++
		}
	}

	return count
}

// GetAll returns all sessions (for monitoring)
func (m *Manager) GetAll() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sessions := make([]*Session, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	return sessions
}
