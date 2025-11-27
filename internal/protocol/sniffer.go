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
func (s *SniffConn) Sniff() (ProtocolType, []byte, error) {
	// Peek first 4 bytes to detect protocol
	peeked, err := s.br.Peek(4)
	if err != nil {
		return ProtocolTCP, nil, err
	}

	// Check for HTTP methods
	if bytes.HasPrefix(peeked, []byte("GET ")) ||
		bytes.HasPrefix(peeked, []byte("POST")) ||
		bytes.HasPrefix(peeked, []byte("PUT ")) ||
		bytes.HasPrefix(peeked, []byte("HEAD")) ||
		bytes.HasPrefix(peeked, []byte("HTTP")) {
		// Check if it's WebSocket upgrade
		more, err := s.br.Peek(512)
		if err == nil && bytes.Contains(more, []byte("Upgrade: websocket")) {
			return ProtocolWebSocket, more, nil
		}
		return ProtocolHTTP, more, nil
	}

	// Default to TCP (custom game protocol)
	return ProtocolTCP, peeked, nil
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
