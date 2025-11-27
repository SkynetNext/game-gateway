package protocol

import (
	"encoding/binary"
	"io"
)

// ReadServerMessageHeader reads server message header (16 bytes) in one call
// Optimized: uses io.ReadFull to read all 16 bytes at once
func ReadServerMessageHeader(r io.Reader) (*ServerMessageHeader, error) {
	// Read all 16 bytes at once
	headerBuf := make([]byte, ServerMessageHeaderSize)
	if _, err := io.ReadFull(r, headerBuf); err != nil {
		return nil, err
	}

	// Parse from buffer (Little Endian)
	header := &ServerMessageHeader{
		Length:   int32(binary.LittleEndian.Uint32(headerBuf[0:4])),
		Type:     int32(binary.LittleEndian.Uint32(headerBuf[4:8])),
		ServerID: binary.LittleEndian.Uint32(headerBuf[8:12]),
		ObjectID: int64(binary.LittleEndian.Uint64(headerBuf[12:20])),
	}
	return header, nil
}

// ReadGateMsgHeader reads Gate message header (9 bytes) in one call
// Optimized: uses io.ReadFull to read all 9 bytes at once
func ReadGateMsgHeader(r io.Reader) (*GateMsgHeader, error) {
	// Read all 9 bytes at once
	headerBuf := make([]byte, GateMsgHeaderSize)
	if _, err := io.ReadFull(r, headerBuf); err != nil {
		return nil, err
	}

	// Parse from buffer (Little Endian for SessionID)
	header := &GateMsgHeader{
		OpCode:    headerBuf[0],
		SessionID: int64(binary.LittleEndian.Uint64(headerBuf[1:9])),
	}
	return header, nil
}

