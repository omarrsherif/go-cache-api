package service

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// These benchmarks isolate service-layer overhead with an in-process Redis
// (miniredis) and a fake store with a fixed simulated latency. They are not a
// substitute for the HTTP load test in cmd/loadtest, which measures the real
// stack; see docs/benchmarks.

const fakeDBLatency = 2 * time.Millisecond

func BenchmarkGet_CacheHit(b *testing.B) {
	e := newTestEnv(b, fakeDBLatency, Options{})
	id := e.seed(b, 1)[0]
	ctx := context.Background()
	e.svc.Get(ctx, id, ReadOptions{}) // warm
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, st, err := e.svc.Get(ctx, id, ReadOptions{}); err != nil || st != CacheHit {
				b.Fatalf("%s %v", st, err)
			}
		}
	})
}

func BenchmarkGet_MySQLOnly(b *testing.B) {
	e := newTestEnv(b, fakeDBLatency, Options{})
	id := e.seed(b, 1)[0]
	ctx := context.Background()
	ro := ReadOptions{NoCache: true, NoStore: true}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, _, err := e.svc.Get(ctx, id, ro); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkGetMany(b *testing.B) {
	e := newTestEnv(b, time.Millisecond, Options{MaxWorkers: 64})
	ids := e.seed(b, 50)
	ctx := context.Background()
	ro := ReadOptions{NoCache: true, NoStore: true}

	b.Run("sequential", func(b *testing.B) {
		for b.Loop() {
			if _, err := e.svc.GetMany(ctx, ids, BatchOptions{Mode: BatchSequential, Read: ro}); err != nil {
				b.Fatal(err)
			}
		}
	})
	for _, w := range []int{4, 8, 16, 32} {
		b.Run(fmt.Sprintf("workers=%d", w), func(b *testing.B) {
			for b.Loop() {
				if _, err := e.svc.GetMany(ctx, ids, BatchOptions{Mode: BatchConcurrent, Workers: w, Read: ro}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
