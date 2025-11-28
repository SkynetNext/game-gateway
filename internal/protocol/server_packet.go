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

// ReadGateMsgHeader reads Gate message header
// Reads 9 bytes (original format) or 33 bytes (extended format with Trace Context)
// The caller should know the expected size from ServerMessageHeader.Length
func ReadGateMsgHeader(r io.Reader) (*GateMsgHeader, error) {
	// Read first 9 bytes (original format)
	headerBuf := make([]byte, GateMsgHeaderSize)
	if _, err := io.ReadFull(r, headerBuf); err != nil {
		return nil, err
	}

	header := &GateMsgHeader{
		OpCode:    headerBuf[0],
		SessionID: int64(binary.LittleEndian.Uint64(headerBuf[1:9])),
	}

	return header, nil
}

// ReadGateMsgHeaderExtended reads Gate message header with Trace Context (33 bytes)
func ReadGateMsgHeaderExtended(r io.Reader) (*GateMsgHeader, error) {
	// Read all 33 bytes at once
	headerBuf := make([]byte, GateMsgHeaderExtendedSize)
	if _, err := io.ReadFull(r, headerBuf); err != nil {
		return nil, err
	}

	header := &GateMsgHeader{
		OpCode:    headerBuf[0],
		SessionID: int64(binary.LittleEndian.Uint64(headerBuf[1:9])),
	}
	copy(header.TraceID[:], headerBuf[9:25])
	copy(header.SpanID[:], headerBuf[25:33])

	return header, nil
}

