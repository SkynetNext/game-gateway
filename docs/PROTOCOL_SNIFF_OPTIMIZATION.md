# Protocol Detection Optimization: Goroutine Scheduling and I/O Blocking

## Problem Description

A ~3 second delay was observed during WebSocket connection establishment. Log analysis revealed the delay occurred in the `Sniff()` protocol detection phase:

```
DEBUG:before Sniff()  - timestamp: 1764458807173573002
DEBUG:after Sniff()   - timestamp: 1764458837174323071
Time difference: ~3 seconds
```

## Root Cause

### 1. Goroutine Scheduling and I/O Blocking

Go's goroutine scheduler uses the GMP model. While it supports preemptive scheduling, **I/O blocking operations put goroutines into a waiting state**:

- `bufio.Reader.Peek()` is a blocking I/O operation
- If the client doesn't send data immediately after connection, `Peek()` blocks waiting
- A large number of goroutines waiting for I/O affects scheduler efficiency

### 2. Long Deadline Causes Extended Blocking

**Problematic Code:**
```go
// Set 30-second deadline in acceptLoop
conn.SetReadDeadline(time.Now().Add(30 * time.Second))

// Sniff() has no own deadline, relies on 30-second timeout
func (s *SniffConn) Sniff() (ProtocolType, []byte, error) {
    peeked, err := s.br.Peek(4)  // Blocks up to 30 seconds if data not available
    // ...
}
```

**Issues:**
- Protocol detection is a lightweight operation and shouldn't wait long
- If client delays sending data, `Peek()` blocks until 30-second timeout
- This causes goroutines to hold resources for extended periods, impacting concurrency

## Solution

Following `unified-access-gateway`'s implementation, set a **short deadline (500ms)** inside `Sniff()`:

```go
func (s *SniffConn) Sniff() (ProtocolType, []byte, error) {
    // Set short deadline to avoid long blocking
    s.Conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
    defer s.Conn.SetReadDeadline(time.Time{}) // Clear deadline
    
    peeked, err := s.br.Peek(4)
    if err != nil {
        // Return default protocol on timeout, don't block
        if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
            return ProtocolTCP, nil, nil
        }
        return ProtocolTCP, nil, err
    }
    // ... protocol detection logic
}
```

## Results

- **Data available**: `Peek()` returns immediately, no delay
- **Data unavailable**: Times out after 500ms, returns default protocol, doesn't block
- **Goroutine scheduling**: Avoids many goroutines waiting for I/O, improves concurrency

## Industry Best Practices

This is the **standard pattern** for protocol detection in Go gateways:

1. **Short timeout**: Use 500ms-1s timeout for protocol detection
2. **Fast failure**: Return default protocol on timeout, don't block subsequent processing
3. **Resource cleanup**: Clear deadline promptly to avoid affecting subsequent I/O operations

Reference implementations:
- `unified-access-gateway`: 500ms timeout
- Other Go gateway projects: Typically use 500ms-2s short timeout

## Summary

This issue is fundamentally a **balance between goroutine scheduling and I/O blocking**. By setting appropriate short timeouts, we can:
- Avoid long goroutine blocking
- Improve system concurrency performance
- Respond quickly to client requests

This is a common optimization point in Go network programming, applicable to all scenarios requiring protocol detection or initial data reading.

