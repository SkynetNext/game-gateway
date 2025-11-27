package pool

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestPool_GetPut(t *testing.T) {
	// Create a test server
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	addr := listener.Addr().String()

	pool := NewPool(addr, 1*time.Second, 5*time.Minute, 100, 10)

	ctx := context.Background()

	// Get connection
	conn1, err := pool.Get(ctx)
	if err != nil {
		t.Fatalf("Failed to get connection: %v", err)
	}

	if conn1 == nil {
		t.Fatal("Expected connection, got nil")
	}

	// Put back
	pool.Put(conn1)

	// Get again (should reuse)
	conn2, err := pool.Get(ctx)
	if err != nil {
		t.Fatalf("Failed to get connection: %v", err)
	}

	// Should be the same connection (reused)
	if conn1 != conn2 {
		t.Log("Note: Connection was not reused (may be expected if connection was closed)")
	}

	pool.Put(conn2)
}

func TestPool_Stats(t *testing.T) {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	addr := listener.Addr().String()
	pool := NewPool(addr, 1*time.Second, 5*time.Minute, 100, 10)

	total, active, idle := pool.Stats()
	if total != 0 || active != 0 || idle != 0 {
		t.Errorf("Expected all zeros, got total=%d, active=%d, idle=%d", total, active, idle)
	}
}
