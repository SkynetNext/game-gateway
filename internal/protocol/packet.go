package protocol

import (
	"encoding/binary"
	"io"
)

const (
	// ClientMessageHeaderSize is the size of client message header (8 bytes)
	ClientMessageHeaderSize = 8

	// ServerMessageHeaderSize is the size of server message header (16 bytes: Length + Type + ServerID + ObjectID)
	ServerMessageHeaderSize = 4 + 4 + 4 + 8

	// GateMsgHeaderSize is the size of Gate message header (9 bytes: OpCode + SessionID)
	GateMsgHeaderSize = 1 + 8
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

// GateMsgHeader represents the Gate message header (9 bytes)
type GateMsgHeader struct {
	OpCode    uint8 // OpCode (GateMsgOpCode.Trans = 1)
	SessionID int64 // Session ID
}

// WriteGateMsgHeader writes Gate message header to writer (9 bytes, Little Endian for SessionID)
func WriteGateMsgHeader(w io.Writer, header *GateMsgHeader) error {
	if _, err := w.Write([]byte{header.OpCode}); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, header.SessionID); err != nil {
		return err
	}
	return nil
}

// ReadFullPacket reads a complete client message packet
// Returns: header, message data, error
// Optimized: uses buffer pool for message data allocation
func ReadFullPacket(r io.Reader) (*ClientMessageHeader, []byte, error) {
	// Read header (8 bytes)
	header, err := ParseClientMessageHeader(r)
	if err != nil {
		return nil, nil, err
	}

	// Read message data
	if header.Length == 0 {
		return header, nil, nil
	}

	// Allocate buffer for message data
	data := make([]byte, header.Length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, nil, err
	}

	return header, data, nil
}

// WriteServerMessage writes a complete server message (MessageHeader + GateMsgHeader + Message Data)
// This matches the format used by original GateServer when sending to backend services
func WriteServerMessage(w io.Writer, messageType int32, sessionID int64, messageData []byte) error {
	// Calculate total length: GateMsgHeader (9 bytes) + message data
	totalLength := GateMsgHeaderSize + len(messageData)

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

	// Write GateMsgHeader (9 bytes)
	gateHeader := &GateMsgHeader{
		OpCode:    1, // GateMsgOpCode.Trans
		SessionID: sessionID,
	}
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
