package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

var (
	host        = flag.String("host", "localhost", "Target host")
	port        = flag.Int("port", 8080, "Target port")
	connections = flag.Int("connections", 100, "Number of concurrent connections")
	duration    = flag.Duration("duration", 30*time.Second, "Test duration")
	rate        = flag.Float64("rate", 10.0, "Messages per second per connection")
	serverType  = flag.Int("server-type", 10, "Server type (10=Account, 15=Version, 200=Game)")
	worldID     = flag.Int("world-id", 0, "World ID (for Game server)")
	messageSize = flag.Int("message-size", 8, "Message size in bytes")
	timeout     = flag.Duration("timeout", 5*time.Second, "Connection timeout")
	verbose     = flag.Bool("verbose", false, "Verbose output")
)

type Stats struct {
	TotalConnections int64
	SuccessfulConns  int64
	FailedConns      int64
	TotalMessages    int64
	SuccessfulMsgs   int64
	FailedMsgs       int64
	TotalBytes       int64
	MinLatency       time.Duration
	MaxLatency       time.Duration
	TotalLatency     time.Duration
	LatencyCount     int64
	ConnErrors       int64
	ReadErrors       int64
	WriteErrors      int64
}

var stats Stats

func main() {
	flag.Parse()

	fmt.Printf("=== Game Gateway Load Test ===\n")
	fmt.Printf("Target: %s:%d\n", *host, *port)
	fmt.Printf("Connections: %d\n", *connections)
	fmt.Printf("Duration: %v\n", *duration)
	fmt.Printf("Rate: %.2f msg/s per connection\n", *rate)
	fmt.Printf("Server Type: %d, World ID: %d\n", *serverType, *worldID)
	fmt.Printf("\n")

	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()

	// Start stats reporter
	statsDone := make(chan struct{})
	go reportStats(ctx, statsDone)

	// Start load test
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, *connections)

	startTime := time.Now()
	for {
		select {
		case <-ctx.Done():
			goto done
		default:
			select {
			case semaphore <- struct{}{}:
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer func() { <-semaphore }()
					runConnection(ctx)
				}()
			default:
				time.Sleep(10 * time.Millisecond)
			}
		}
	}

done:
	wg.Wait()
	elapsed := time.Since(startTime)

	// Final report
	<-statsDone
	printFinalReport(elapsed)
}

func runConnection(ctx context.Context) {
	atomic.AddInt64(&stats.TotalConnections, 1)

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", *host, *port), *timeout)
	if err != nil {
		atomic.AddInt64(&stats.FailedConns, 1)
		atomic.AddInt64(&stats.ConnErrors, 1)
		if *verbose {
			fmt.Printf("❌ Connection failed: %v\n", err)
		}
		return
	}
	defer conn.Close()

	atomic.AddInt64(&stats.SuccessfulConns, 1)

	// Set read deadline
	conn.SetReadDeadline(time.Now().Add(*timeout))

	// Calculate message interval
	interval := time.Duration(float64(time.Second) / *rate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := sendMessage(conn); err != nil {
				if *verbose {
					fmt.Printf("❌ Send message failed: %v\n", err)
				}
				return
			}
		}
	}
}

func sendMessage(conn net.Conn) error {
	start := time.Now()

	// Build message header (8 bytes: <HHI)
	// H: length (2 bytes, little endian)
	// H: type (2 bytes, little endian)
	// I: server_id (4 bytes, little endian)
	// server_id = (worldID << 16) | (serverType << 8) | instID
	instID := 0
	serverID := uint32((*worldID << 16) | (*serverType << 8) | instID)

	header := make([]byte, 8)
	binary.LittleEndian.PutUint16(header[0:2], uint16(*messageSize))
	binary.LittleEndian.PutUint16(header[2:4], 1) // message type
	binary.LittleEndian.PutUint32(header[4:8], serverID)

	// Send header
	if _, err := conn.Write(header); err != nil {
		atomic.AddInt64(&stats.WriteErrors, 1)
		atomic.AddInt64(&stats.FailedMsgs, 1)
		return err
	}

	atomic.AddInt64(&stats.TotalMessages, 1)
	atomic.AddInt64(&stats.TotalBytes, int64(len(header)))

	// Try to read response (non-blocking)
	conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil && err != io.EOF {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			// Timeout is OK, server might not respond
		} else {
			atomic.AddInt64(&stats.ReadErrors, 1)
		}
	} else if n > 0 {
		atomic.AddInt64(&stats.TotalBytes, int64(n))
	}

	latency := time.Since(start)
	atomic.AddInt64(&stats.SuccessfulMsgs, 1)
	atomic.AddInt64(&stats.LatencyCount, 1)

	// Update latency stats
	for {
		oldMin := atomic.LoadInt64((*int64)(&stats.MinLatency))
		if oldMin == 0 || latency < time.Duration(oldMin) {
			if atomic.CompareAndSwapInt64((*int64)(&stats.MinLatency), oldMin, int64(latency)) {
				break
			}
		} else {
			break
		}
	}

	for {
		oldMax := atomic.LoadInt64((*int64)(&stats.MaxLatency))
		if latency > time.Duration(oldMax) {
			if atomic.CompareAndSwapInt64((*int64)(&stats.MaxLatency), oldMax, int64(latency)) {
				break
			}
		} else {
			break
		}
	}

	// Accumulate total latency (approximate)
	atomic.AddInt64((*int64)(&stats.TotalLatency), int64(latency))

	return nil
}

func reportStats(ctx context.Context, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			printStats()
		}
	}
}

func printStats() {
	totalConns := atomic.LoadInt64(&stats.TotalConnections)
	successConns := atomic.LoadInt64(&stats.SuccessfulConns)
	failedConns := atomic.LoadInt64(&stats.FailedConns)
	successMsgs := atomic.LoadInt64(&stats.SuccessfulMsgs)
	failedMsgs := atomic.LoadInt64(&stats.FailedMsgs)
	totalBytes := atomic.LoadInt64(&stats.TotalBytes)

	fmt.Printf("\r[Stats] Conns: %d/%d (failed: %d) | Msgs: %d (failed: %d) | Bytes: %d",
		successConns, totalConns, failedConns, successMsgs, failedMsgs, totalBytes)
}

func printFinalReport(elapsed time.Duration) {
	fmt.Printf("\n\n=== Final Report ===\n")
	fmt.Printf("Duration: %v\n", elapsed)

	totalConns := atomic.LoadInt64(&stats.TotalConnections)
	successConns := atomic.LoadInt64(&stats.SuccessfulConns)
	failedConns := atomic.LoadInt64(&stats.FailedConns)
	successMsgs := atomic.LoadInt64(&stats.SuccessfulMsgs)
	failedMsgs := atomic.LoadInt64(&stats.FailedMsgs)
	totalBytes := atomic.LoadInt64(&stats.TotalBytes)
	latencyCount := atomic.LoadInt64(&stats.LatencyCount)
	totalMsgs := atomic.LoadInt64(&stats.TotalMessages)

	fmt.Printf("\n--- Connections ---\n")
	fmt.Printf("Total: %d\n", totalConns)
	fmt.Printf("Successful: %d (%.2f%%)\n", successConns, float64(successConns)/float64(totalConns)*100)
	fmt.Printf("Failed: %d (%.2f%%)\n", failedConns, float64(failedConns)/float64(totalConns)*100)

	fmt.Printf("\n--- Messages ---\n")
	fmt.Printf("Total: %d\n", totalMsgs)
	fmt.Printf("Successful: %d (%.2f%%)\n", successMsgs, float64(successMsgs)/float64(totalMsgs)*100)
	fmt.Printf("Failed: %d (%.2f%%)\n", failedMsgs, float64(failedMsgs)/float64(totalMsgs)*100)
	fmt.Printf("Throughput: %.2f msg/s\n", float64(successMsgs)/elapsed.Seconds())

	fmt.Printf("\n--- Latency ---\n")
	if latencyCount > 0 {
		minLatency := time.Duration(atomic.LoadInt64((*int64)(&stats.MinLatency)))
		maxLatency := time.Duration(atomic.LoadInt64((*int64)(&stats.MaxLatency)))
		avgLatency := time.Duration(atomic.LoadInt64((*int64)(&stats.TotalLatency)) / latencyCount)

		fmt.Printf("Min: %v\n", minLatency)
		fmt.Printf("Max: %v\n", maxLatency)
		fmt.Printf("Avg: %v\n", avgLatency)
	}

	fmt.Printf("\n--- Throughput ---\n")
	fmt.Printf("Total Bytes: %d (%.2f MB)\n", totalBytes, float64(totalBytes)/1024/1024)
	fmt.Printf("Throughput: %.2f MB/s\n", float64(totalBytes)/1024/1024/elapsed.Seconds())

	fmt.Printf("\n--- Errors ---\n")
	fmt.Printf("Connection Errors: %d\n", atomic.LoadInt64(&stats.ConnErrors))
	fmt.Printf("Read Errors: %d\n", atomic.LoadInt64(&stats.ReadErrors))
	fmt.Printf("Write Errors: %d\n", atomic.LoadInt64(&stats.WriteErrors))

	// Exit code
	if failedConns > totalConns/10 || failedMsgs > totalMsgs/10 {
		fmt.Printf("\n❌ Test failed: too many errors\n")
		os.Exit(1)
	} else {
		fmt.Printf("\n✅ Test completed successfully\n")
		os.Exit(0)
	}
}
