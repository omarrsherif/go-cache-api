package workerpool

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func seq(n int) []int {
	s := make([]int, n)
	for i := range s {
		s[i] = i
	}
	return s
}

func TestOrderPreserved(t *testing.T) {
	for _, workers := range []int{0, 1, 4, 100} {
		res, err := Map(context.Background(), seq(50), workers, func(_ context.Context, i int) (int, error) {
			time.Sleep(time.Duration(50-i) * 10 * time.Microsecond) // later items finish first
			return i * 2, nil
		})
		if err != nil {
			t.Fatalf("workers=%d: %v", workers, err)
		}
		for i, r := range res {
			if r.Err != nil || r.Value != i*2 {
				t.Fatalf("workers=%d: result[%d] = %+v", workers, i, r)
			}
		}
	}
}

func TestBoundedConcurrency(t *testing.T) {
	const workers = 8
	var inFlight, peak atomic.Int64
	_, err := Map(context.Background(), seq(200), workers, func(_ context.Context, i int) (int, error) {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond)
		inFlight.Add(-1)
		return i, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if p := peak.Load(); p > workers || p < 2 {
		t.Fatalf("peak concurrency = %d, want 2..%d", p, workers)
	}
}

func TestInlineWhenSingleWorker(t *testing.T) {
	var inFlight, peak atomic.Int64
	Map(context.Background(), seq(20), 1, func(_ context.Context, i int) (int, error) {
		n := inFlight.Add(1)
		if n > peak.Load() {
			peak.Store(n)
		}
		time.Sleep(time.Millisecond)
		inFlight.Add(-1)
		return i, nil
	})
	if peak.Load() != 1 {
		t.Fatalf("peak = %d, want 1", peak.Load())
	}
}

func TestErrorsIsolated(t *testing.T) {
	boom := errors.New("boom")
	res, err := Map(context.Background(), seq(10), 4, func(_ context.Context, i int) (int, error) {
		if i%3 == 0 {
			return 0, boom
		}
		return i, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range res {
		wantErr := i%3 == 0
		if (r.Err != nil) != wantErr {
			t.Fatalf("result[%d] err = %v", i, r.Err)
		}
	}
}

func TestEmptyInput(t *testing.T) {
	res, err := Map(context.Background(), []int{}, 8, func(_ context.Context, i int) (int, error) { return i, nil })
	if err != nil || len(res) != 0 {
		t.Fatalf("got %v, %v", res, err)
	}
}

func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var started atomic.Int64
	start := time.Now()
	res, err := Map(ctx, seq(100), 4, func(ctx context.Context, i int) (int, error) {
		if started.Add(1) == 10 {
			cancel()
		}
		select {
		case <-time.After(20 * time.Millisecond):
			return i, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want Canceled", err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("Map did not return promptly after cancellation")
	}
	cancelled := 0
	for _, r := range res {
		if errors.Is(r.Err, context.Canceled) {
			cancelled++
		}
	}
	if cancelled < 80 {
		t.Fatalf("only %d of 100 items were cancelled", cancelled)
	}
}
