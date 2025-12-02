package protocol

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/SkynetNext/game-gateway/internal/buffer"
)

const (
	// ClientMessageHeaderSize is the size of client message header (8 bytes)
	ClientMessageHeaderSize = 8

	// ServerMessageHeaderSize is the size of server message header (16 bytes: Length + Type + ServerID + ObjectID)
	ServerMessageHeaderSize = 4 + 4 + 4 + 8

	// GateMsgHeaderSize is the size of Gate message header (9 bytes: OpCode + SessionID)
	GateMsgHeaderSize = 1 + 8

	// GateMsgHeaderExtendedSize is the extended size with Trace Context (33 bytes: OpCode + SessionID + TraceID + SpanID)
	GateMsgHeaderExtendedSize = 1 + 8 + 16 + 8
)

// Client Message Header Structure (from client-side packMessage/unpackMessage):
//
// The client sends messages with the following header structure (8 bytes, Little Endian):
//
//	Offset  Size    Type      Description                    Endian
//	0-1     2       uint16    length - Message data length   Little Endian
//	                          (excluding header, i.e., only message body size)
//	2-3     2       uint16    messageId - Message ID         Little Endian
//	4-7     4       uint32    serverId - Server ID           Little Endian
//	                          Bit structure (reused for routing, CRC/Seq not used):
//	                          - bit 31: isZip (compression flag, 1=compressed, 0=uncompressed)
//	                          - bit 16-30: worldId (world ID, 15 bits, max 32767 worlds)
//	                          - bit 8-15: serverType (server type ID, 8 bits, max 256 types)
//	                          - bit 0-7: instID (instance ID, 8 bits, max 256 instances)
//
//	                          Format: worldId.serverType.instID (e.g., 1.10.2)
//	                          This matches ServerID structure for routing to backend services.
//
//	                          Extraction examples:
//	                          - isZip: (serverId & 0x80000000) != 0
//	                          - worldId: (ushort)((serverId >> 16) & 0x7FFF)
//	                          - serverType: (byte)((serverId >> 8) & 0xFF)
//	                          - instID: (byte)(serverId & 0xFF)
//
// Full client message structure:
//	[Client Message Header (8 bytes)] + [Message Data (length bytes)]
//
// Gateway sends to backend format (matches original GateServer):
//	[Server Message Header (16 bytes)] + [Gate Message Header (9 bytes)] + [Message Data]
//
// Server Message Header (16 bytes, Little Endian):
//	- Length (4 bytes): Total length of GateMsgHeader + Message Data
//	- Type (4 bytes): Message type (from client MessageID)
//	- ServerID (4 bytes): Set to 0 for client messages
//	- ObjectID (8 bytes): SessionID
//
// Gate Message Header (9 bytes):
//	- OpCode (1 byte): GateMsgOpCode.Trans = 1
//	- SessionID (8 bytes, Little Endian): Session ID

// ClientMessageHeader represents the client message header (8 bytes)
type ClientMessageHeader struct {
	Length    uint16 // Message data length (excluding header)
	MessageID uint16 // Message ID
	ServerID  uint32 // Server ID (isZip + worldId + serverType + instID)
}

// ParseClientMessageHeader parses client message header from reader (8 bytes, Little Endian)
// Optimized: uses io.ReadFull to read all 8 bytes at once, reducing system calls
func ParseClientMessageHeader(r io.Reader) (*ClientMessageHeader, error) {
	// Read all 8 bytes at once
	headerBuf := make([]byte, ClientMessageHeaderSize)
	if _, err := io.ReadFull(r, headerBuf); err != nil {
		return nil, err
	}

	// Parse from buffer (Little Endian)
	header := &ClientMessageHeader{
		Length:    binary.LittleEndian.Uint16(headerBuf[0:2]),
		MessageID: binary.LittleEndian.Uint16(headerBuf[2:4]),
		ServerID:  binary.LittleEndian.Uint32(headerBuf[4:8]),
	}
	return header, nil
}

// WriteClientMessageHeader writes client message header to writer (8 bytes, Little Endian)
// Optimized: constructs header buffer in one allocation and writes in one operation
func WriteClientMessageHeader(w io.Writer, header *ClientMessageHeader) error {
	// Construct 8-byte header buffer
	headerBuf := make([]byte, ClientMessageHeaderSize)
	binary.LittleEndian.PutUint16(headerBuf[0:2], header.Length)
	binary.LittleEndian.PutUint16(headerBuf[2:4], header.MessageID)
	binary.LittleEndian.PutUint32(headerBuf[4:8], header.ServerID)

	// Write header in one operation
	_, err := w.Write(headerBuf)
	return err
}

// ExtractServerIDInfo extracts routing information from ServerID
func ExtractServerIDInfo(serverID uint32) (isZip bool, worldID uint16, serverType uint8, instID uint8) {
	isZip = (serverID & 0x80000000) != 0
	worldID = uint16((serverID >> 16) & 0x7FFF)
	serverType = uint8((serverID >> 8) & 0xFF)
	instID = uint8(serverID & 0xFF)
	return
}

// ServerMessageHeader represents the server message header (16 bytes)
type ServerMessageHeader struct {
	Length   int32  // Data length (excluding header)
	Type     int32  // Message type
	ServerID uint32 // Server ID (set to 0 for client messages)
	ObjectID int64  // Object ID (SessionID)
}

// WriteServerMessageHeader writes server message header to writer (16 bytes, Little Endian)
func WriteServerMessageHeader(w io.Writer, header *ServerMessageHeader) error {
	if err := binary.Write(w, binary.LittleEndian, header.Length); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, header.Type); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, header.ServerID); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, header.ObjectID); err != nil {
		return err
	}
	return nil
}

// GateMsgHeader represents the Gate message header
// Original format (9 bytes): OpCode (1) + SessionID (8)
// Extended format (33 bytes): OpCode (1) + SessionID (8) + TraceID (16) + SpanID (8)
// TraceID and SpanID are optional: if TraceID is all zeros, use original format
type GateMsgHeader struct {
	OpCode    uint8    // OpCode (GateMsgOpCode.Trans = 1)
	SessionID int64    // Session ID
	TraceID   [16]byte // Trace ID (16 bytes, W3C TraceID format: 128-bit)
	SpanID    [8]byte  // Span ID (8 bytes, 64-bit)
}

// HasTraceContext returns true if TraceID is not all zeros
func (h *GateMsgHeader) HasTraceContext() bool {
	for _, b := range h.TraceID {
		if b != 0 {
			return true
		}
	}
	return false
}

// WriteGateMsgHeader writes Gate message header to writer
// If TraceID is not all zeros, writes extended format (33 bytes)
// Otherwise, writes original format (9 bytes) for backward compatibility
func WriteGateMsgHeader(w io.Writer, header *GateMsgHeader) error {
	if _, err := w.Write([]byte{header.OpCode}); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, header.SessionID); err != nil {
		return err
	}

	// Write Trace Context if present (extended format)
	if header.HasTraceContext() {
		if _, err := w.Write(header.TraceID[:]); err != nil {
			return err
		}
		if _, err := w.Write(header.SpanID[:]); err != nil {
			return err
		}
	}
	return nil
}

var (
	// ErrMessageTooLarge is returned when message size exceeds maximum allowed
	ErrMessageTooLarge = errors.New("message size exceeds maximum allowed")
)

// ReadFullPacket reads a complete client message packet
// Returns: header, message data, error
// Optimized: uses buffer pool for message data allocation
// Security: validates message size to prevent DoS attacks
func ReadFullPacket(r io.Reader, maxMessageSize int) (*ClientMessageHeader, []byte, error) {
	// Read header (8 bytes)
	header, err := ParseClientMessageHeader(r)
	if err != nil {
		return nil, nil, err
	}

	// Validate message size to prevent DoS
	if maxMessageSize > 0 && int(header.Length) > maxMessageSize {
		return nil, nil, fmt.Errorf("%w: %d bytes (max: %d)", ErrMessageTooLarge, header.Length, maxMessageSize)
	}

	// Read message data
	if header.Length == 0 {
		return header, nil, nil
	}

	// Use buffer pool for message data allocation
	var data []byte
	var pooledBuf []byte
	if int(header.Length) <= 8192 {
		// Use buffer pool for messages <= 8KB
		pooledBuf = buffer.Get()
		data = pooledBuf[:header.Length]
	} else {
		// Allocate new buffer for large messages
		data = make([]byte, header.Length)
	}

	if _, err := io.ReadFull(r, data); err != nil {
		// Return buffer to pool if it was pooled
		if pooledBuf != nil {
			buffer.Put(pooledBuf)
		}
		return nil, nil, err
	}

	// Note: Caller is responsible for returning pooled buffer using buffer.Put()
	// We attach the pooled buffer to the data slice for later cleanup
	// This is a bit of a hack, but necessary for buffer pool management
	return header, data, nil
}

// WriteServerMessage writes a complete server message (MessageHeader + GateMsgHeader + Message Data)
// This matches the format used by original GateServer when sending to backend services
// If traceContext is provided, includes Trace ID and Span ID in extended format
func WriteServerMessage(w io.Writer, messageType int32, sessionID int64, messageData []byte, traceContext ...map[string]string) error {
	// Create GateMsgHeader
	gateHeader := &GateMsgHeader{
		OpCode:    1, // GateMsgOpCode.Trans
		SessionID: sessionID,
	}

	// Extract Trace ID and Span ID from trace context if provided
	// Optimized: use encoding/hex.Decode for better performance than fmt.Sscanf
	if len(traceContext) > 0 && traceContext[0] != nil {
		if traceID, ok := traceContext[0]["trace_id"]; ok && traceID != "" && len(traceID) == 32 {
			// Convert trace ID string to bytes (W3C format: 32 hex chars = 16 bytes)
			// Use hex.Decode which is much faster than fmt.Sscanf in a loop
			if decoded, err := hex.DecodeString(traceID); err == nil && len(decoded) == 16 {
				copy(gateHeader.TraceID[:], decoded)
			}
		}
		if spanID, ok := traceContext[0]["span_id"]; ok && spanID != "" && len(spanID) == 16 {
			// Convert span ID string to bytes (16 hex chars = 8 bytes)
			// Use hex.Decode which is much faster than fmt.Sscanf in a loop
			if decoded, err := hex.DecodeString(spanID); err == nil && len(decoded) == 8 {
				copy(gateHeader.SpanID[:], decoded)
			}
		}
	}

	// Calculate total length based on whether we have trace context
	headerSize := GateMsgHeaderSize
	if gateHeader.HasTraceContext() {
		headerSize = GateMsgHeaderExtendedSize
	}
	totalLength := headerSize + len(messageData)

	// Write MessageHeader (16 bytes)
	serverHeader := &ServerMessageHeader{
		Length:   int32(totalLength),
		Type:     messageType,
		ServerID: 0, // Set to 0 for client messages
		ObjectID: sessionID,
	}
	if err := WriteServerMessageHeader(w, serverHeader); err != nil {
		return err
	}

	// Write GateMsgHeader (9 or 33 bytes depending on trace context)
	if err := WriteGateMsgHeader(w, gateHeader); err != nil {
		return err
	}

	// Write message data
	if len(messageData) > 0 {
		if _, err := w.Write(messageData); err != nil {
			return err
		}
	}

	return nil
}
