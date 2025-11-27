package main

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/SkynetNext/game-gateway/internal/pool"
	"github.com/SkynetNext/game-gateway/internal/ratelimit"
	"github.com/SkynetNext/game-gateway/internal/session"
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

func BenchmarkLimiter_Allow(b *testing.B) {
	limiter := ratelimit.NewLimiter(1000)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if limiter.Allow() {
				limiter.Release()
			}
		}
	})
}

func BenchmarkSessionManager_AddGet(b *testing.B) {
	mgr := session.NewManager()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := int64(0)
		for pb.Next() {
			sess := &session.Session{
				SessionID:    i,
				ServiceType:  "test",
				CreatedAt:    time.Now(),
				LastActiveAt: time.Now(),
				State:        session.SessionStateConnected,
			}
			mgr.Add(sess)
			mgr.Get(sess.SessionID)
			mgr.Remove(sess.SessionID)
			i++
		}
	})
}

func BenchmarkSessionManager_Get(b *testing.B) {
	mgr := session.NewManager()
	// Pre-populate with sessions
	for i := int64(0); i < 1000; i++ {
		sess := &session.Session{
			SessionID:    i,
			ServiceType:  "test",
			CreatedAt:    time.Now(),
			LastActiveAt: time.Now(),
			State:        session.SessionStateConnected,
		}
		mgr.Add(sess)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := int64(0)
		for pb.Next() {
			mgr.Get(i % 1000)
			i++
		}
	})
}
