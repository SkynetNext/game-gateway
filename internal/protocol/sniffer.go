package protocol

import (
	"bufio"
	"bytes"
	"net"
	"time"
)

// SniffConn wraps a connection with protocol sniffing capability
type SniffConn struct {
	Conn net.Conn
	br   *bufio.Reader
}

// NewSniffConn creates a new SniffConn
func NewSniffConn(conn net.Conn) *SniffConn {
	return &SniffConn{
		Conn: conn,
		br:   bufio.NewReader(conn),
	}
}

// Sniff detects the protocol type by peeking at the first few bytes
// Sets a short read deadline to prevent hanging on slow/malicious connections
// Following unified-access-gateway pattern: 500ms timeout for protocol detection
func (s *SniffConn) Sniff() (ProtocolType, []byte, error) {
	// Set a short read deadline (500ms) to prevent blocking
	// If data is available, Peek will return immediately
	// If not, we'll timeout quickly instead of waiting for the connection's long deadline
	s.Conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	defer s.Conn.SetReadDeadline(time.Time{}) // Clear deadline after sniffing

	// Peek first 4 bytes to detect protocol
	peeked, err := s.br.Peek(4)
	if err != nil {
		// If timeout, return TCP as default (data may arrive later)
		// This prevents blocking on slow clients
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return ProtocolTCP, nil, nil // Return nil error for timeout (expected for slow clients)
		}
		return ProtocolTCP, nil, err
	}

	// Check for HTTP methods
	if bytes.HasPrefix(peeked, []byte("GET ")) ||
		bytes.HasPrefix(peeked, []byte("POST")) ||
		bytes.HasPrefix(peeked, []byte("PUT ")) ||
		bytes.HasPrefix(peeked, []byte("HEAD")) ||
		bytes.HasPrefix(peeked, []byte("HTTP")) {
		// Peek a bit more to inspect headers (best-effort)
		more, _ := s.br.Peek(512)
		lowerMore := bytes.ToLower(more)
		// Check for WebSocket upgrade: look for both "upgrade" and "websocket" keywords
		// This is more robust than exact string matching
		if bytes.Contains(lowerMore, []byte("upgrade")) && bytes.Contains(lowerMore, []byte("websocket")) {
			return ProtocolWebSocket, more, nil
		}
		return ProtocolHTTP, more, nil
	}

	// Default to TCP (custom game protocol)
	return ProtocolTCP, peeked, nil
}

// Reader exposes the underlying buffered reader (used for HTTP/WebSocket parsing).
func (s *SniffConn) Reader() *bufio.Reader {
	return s.br
}

// Read implements io.Reader
func (s *SniffConn) Read(p []byte) (n int, err error) {
	return s.br.Read(p)
}

// Write implements io.Writer
func (s *SniffConn) Write(p []byte) (n int, err error) {
	return s.Conn.Write(p)
}

// Close closes the connection
func (s *SniffConn) Close() error {
	return s.Conn.Close()
}

// RemoteAddr returns the remote address
func (s *SniffConn) RemoteAddr() net.Addr {
	return s.Conn.RemoteAddr()
}

// LocalAddr returns the local address
func (s *SniffConn) LocalAddr() net.Addr {
	return s.Conn.LocalAddr()
}

// SetDeadline sets the read and write deadlines
func (s *SniffConn) SetDeadline(t time.Time) error {
	return s.Conn.SetDeadline(t)
}

// SetReadDeadline sets the read deadline
func (s *SniffConn) SetReadDeadline(t time.Time) error {
	return s.Conn.SetReadDeadline(t)
}

// SetWriteDeadline sets the write deadline
func (s *SniffConn) SetWriteDeadline(t time.Time) error {
	return s.Conn.SetWriteDeadline(t)
}
