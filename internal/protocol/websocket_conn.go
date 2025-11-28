package protocol

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

const (
	wsOpcodeContinuation = 0x0
	wsOpcodeText         = 0x1
	wsOpcodeBinary       = 0x2
	wsOpcodeClose        = 0x8
	wsOpcodePing         = 0x9
	wsOpcodePong         = 0xA
)

var (
	// ErrWebSocketFrameMask indicates that a client frame was not masked (violates RFC6455).
	ErrWebSocketFrameMask = errors.New("websocket frame not masked")
	// ErrWebSocketFrameType indicates that the opcode is not supported.
	ErrWebSocketFrameType = errors.New("unsupported websocket frame type")
)

// WebSocketConn adapts a WebSocket stream into a net.Conn compatible interface.
// It decodes binary frames and exposes the payload as a raw byte stream so the
// rest of the gateway can continue to treat the session like a TCP connection.
type WebSocketConn struct {
	conn          net.Conn
	reader        *bufio.Reader
	maxPayload    int
	readMu        sync.Mutex
	writeMu       sync.Mutex
	pending       []byte
	fragmentBuf   []byte
	closeOnce     sync.Once
	controlClosed bool
}

// NewWebSocketConn creates a new adapter. The provided reader must be the same
// buffered reader that was used for the handshake so buffered bytes are not lost.
func NewWebSocketConn(conn net.Conn, reader *bufio.Reader, maxPayload int) *WebSocketConn {
	return &WebSocketConn{
		conn:       conn,
		reader:     reader,
		maxPayload: maxPayload,
	}
}

// Read returns the next chunk of raw payload data (decoded from binary frames).
func (w *WebSocketConn) Read(p []byte) (int, error) {
	w.readMu.Lock()
	defer w.readMu.Unlock()

	for len(w.pending) == 0 {
		payload, err := w.nextDataPayload()
		if err != nil {
			return 0, err
		}
		w.pending = payload
	}

	n := copy(p, w.pending)
	w.pending = w.pending[n:]
	return n, nil
}

// Write sends data to the client by wrapping it in a binary frame.
func (w *WebSocketConn) Write(p []byte) (int, error) {
	if err := w.writeFrame(wsOpcodeBinary, p, true); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Close sends a close control frame (best effort) and closes the underlying connection.
func (w *WebSocketConn) Close() error {
	w.closeOnce.Do(func() {
		_ = w.writeControlFrame(wsOpcodeClose, nil)
	})
	return w.conn.Close()
}

func (w *WebSocketConn) nextDataPayload() ([]byte, error) {
	for {
		frame, err := w.readFrame()
		if err != nil {
			return nil, err
		}

		switch frame.opcode {
		case wsOpcodeBinary:
			if frame.fin {
				return frame.payload, nil
			}
			w.fragmentBuf = append(w.fragmentBuf, frame.payload...)
		case wsOpcodeContinuation:
			if len(w.fragmentBuf) == 0 {
				return nil, ErrWebSocketFrameType
			}
			w.fragmentBuf = append(w.fragmentBuf, frame.payload...)
			if frame.fin {
				payload := w.fragmentBuf
				w.fragmentBuf = nil
				return payload, nil
			}
		case wsOpcodePing:
			_ = w.writeControlFrame(wsOpcodePong, frame.payload)
		case wsOpcodePong:
			// ignore
		case wsOpcodeClose:
			_ = w.writeControlFrame(wsOpcodeClose, frame.payload)
			return nil, io.EOF
		case wsOpcodeText:
			// Text frames are not expected from clients; treat as error.
			return nil, ErrWebSocketFrameType
		default:
			// Ignore unknown opcodes per RFC6455 (close handled above).
		}
	}
}

type wsFrame struct {
	fin     bool
	opcode  byte
	payload []byte
}

func (w *WebSocketConn) readFrame() (*wsFrame, error) {
	h1, err := w.reader.ReadByte()
	if err != nil {
		return nil, err
	}
	h2, err := w.reader.ReadByte()
	if err != nil {
		return nil, err
	}

	fin := (h1 & 0x80) != 0
	opcode := h1 & 0x0F

	masked := (h2 & 0x80) != 0
	if !masked {
		return nil, ErrWebSocketFrameMask
	}

	payloadLen := int(h2 & 0x7F)
	switch payloadLen {
	case 126:
		var lenBuf [2]byte
		if _, err := io.ReadFull(w.reader, lenBuf[:]); err != nil {
			return nil, err
		}
		payloadLen = int(binary.BigEndian.Uint16(lenBuf[:]))
	case 127:
		var lenBuf [8]byte
		if _, err := io.ReadFull(w.reader, lenBuf[:]); err != nil {
			return nil, err
		}
		payloadLen = int(binary.BigEndian.Uint64(lenBuf[:]))
	}

	if w.maxPayload > 0 && payloadLen > w.maxPayload {
		return nil, ErrMessageTooLarge
	}

	var maskKey [4]byte
	if _, err := io.ReadFull(w.reader, maskKey[:]); err != nil {
		return nil, err
	}

	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(w.reader, payload); err != nil {
		return nil, err
	}
	for i := 0; i < payloadLen; i++ {
		payload[i] ^= maskKey[i%4]
	}

	return &wsFrame{
		fin:     fin,
		opcode:  opcode,
		payload: payload,
	}, nil
}

func (w *WebSocketConn) writeFrame(opcode byte, payload []byte, fin bool) error {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()

	header := make([]byte, 0, 10)
	b := opcode
	if fin {
		b |= 0x80
	}
	header = append(header, b)

	payloadLen := len(payload)
	switch {
	case payloadLen <= 125:
		header = append(header, byte(payloadLen))
	case payloadLen <= 65535:
		header = append(header, 126)
		var lenBuf [2]byte
		binary.BigEndian.PutUint16(lenBuf[:], uint16(payloadLen))
		header = append(header, lenBuf[:]...)
	default:
		header = append(header, 127)
		var lenBuf [8]byte
		binary.BigEndian.PutUint64(lenBuf[:], uint64(payloadLen))
		header = append(header, lenBuf[:]...)
	}

	if _, err := w.conn.Write(header); err != nil {
		return err
	}
	if payloadLen > 0 {
		if _, err := w.conn.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

func (w *WebSocketConn) writeControlFrame(opcode byte, payload []byte) error {
	if len(payload) > 125 {
		payload = payload[:125]
	}
	return w.writeFrame(opcode, payload, true)
}

// LocalAddr returns the local network address.
func (w *WebSocketConn) LocalAddr() net.Addr {
	return w.conn.LocalAddr()
}

// RemoteAddr returns the remote network address.
func (w *WebSocketConn) RemoteAddr() net.Addr {
	return w.conn.RemoteAddr()
}

// SetDeadline sets both read and write deadlines.
func (w *WebSocketConn) SetDeadline(t time.Time) error {
	return w.conn.SetDeadline(t)
}

// SetReadDeadline sets the deadline for future Read calls.
func (w *WebSocketConn) SetReadDeadline(t time.Time) error {
	return w.conn.SetReadDeadline(t)
}

// SetWriteDeadline sets the deadline for future Write calls.
func (w *WebSocketConn) SetWriteDeadline(t time.Time) error {
	return w.conn.SetWriteDeadline(t)
}

