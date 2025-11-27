package main

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/SkynetNext/game-gateway/internal/pool"
)

func BenchmarkPool_Get(b *testing.B) {
	// Create a test server
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		b.Fatal(err)
	}
	defer listener.Close()

	addr := listener.Addr().String()
	p := pool.NewPool(addr, 1*time.Second, 5*time.Minute, 1000, 100)

	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			conn, err := p.Get(ctx)
			if err == nil {
				p.Put(conn)
			}
		}
	})
}

func BenchmarkSessionManager_AddGet(b *testing.B) {
	// This would require importing session package
	// For now, just a placeholder
	b.Skip("Requires session package implementation")
}
