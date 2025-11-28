package protocol

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// BenchmarkWriteServerMessage_Original benchmarks original format (no trace context)
func BenchmarkWriteServerMessage_Original(b *testing.B) {
	w := &bytes.Buffer{}
	messageData := make([]byte, 100)
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Reset()
		WriteServerMessage(w, 1, 12345, messageData)
	}
}

// BenchmarkWriteServerMessage_WithTrace benchmarks extended format (with trace context)
func BenchmarkWriteServerMessage_WithTrace(b *testing.B) {
	w := &bytes.Buffer{}
	messageData := make([]byte, 100)
	traceContext := map[string]string{
		"trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
		"span_id":  "00f067aa0ba902b7",
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Reset()
		WriteServerMessage(w, 1, 12345, messageData, traceContext)
	}
}

// BenchmarkHexDecode benchmarks hex string to bytes conversion
func BenchmarkHexDecode(b *testing.B) {
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	spanID := "00f067aa0ba902b7"
	
	b.Run("hex.DecodeString", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			hex.DecodeString(traceID)
			hex.DecodeString(spanID)
		}
	})
}

