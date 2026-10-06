package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/omarrsherif/go-cache-api/internal/metrics"
)

const warmup = 2 * time.Second

// worker is per-goroutine state: its own RNG so there is no shared lock.
type worker struct {
	rng  *rand.Rand
	idx  int
	zipf *rand.Zipf // built lazily by the mixed scenario
}

// scenarioFn issues one request and returns its sample.
type scenarioFn func(ctx context.Context, c *client, w *worker) sample

// runClosedLoop runs concurrency goroutines, each issuing requests back to
// back until the duration elapses. Samples from the first warmup seconds are
// discarded. Server-side metrics are diffed around the measured window.
func runClosedLoop(ctx context.Context, name string, concurrency int, duration time.Duration, c *client, fn scenarioFn) scenarioResult {
	fmt.Printf("running %-22s concurrency=%-4d duration=%v\n", name, concurrency, duration)

	perWorker := make([][]sample, concurrency)
	var wg sync.WaitGroup
	start := time.Now()
	measureFrom := start.Add(warmup)
	deadline := start.Add(warmup + duration)

	var before metrics.Snapshot
	var beforeOnce sync.Once
	snap := func() {
		s, err := c.metrics(ctx)
		if err == nil {
			before = s
		}
	}

	for i := range concurrency {
		wg.Go(func() {
			w := &worker{rng: rand.New(rand.NewPCG(uint64(i)+1, uint64(time.Now().UnixNano()))), idx: i}
			buf := make([]sample, 0, 4096)
			for {
				now := time.Now()
				if now.After(deadline) {
					break
				}
				s := fn(ctx, c, w)
				if now.After(measureFrom) {
					beforeOnce.Do(snap)
					buf = append(buf, s)
				}
			}
			perWorker[i] = buf
		})
	}
	wg.Wait()
	after, _ := c.metrics(ctx)
	beforeOnce.Do(func() {}) // if no samples were taken, before stays zero

	var all []sample
	for _, b := range perWorker {
		all = append(all, b...)
	}
	res := summarize(name, concurrency, duration, all)
	res.Metrics = after.Sub(before)
	fmt.Printf("  -> %s\n", res.oneLine())
	return res
}

// getScenario reads uniformly random seeded ids.
func getScenario(ids []int64, bypass bool) scenarioFn {
	return func(ctx context.Context, c *client, w *worker) sample {
		return c.get(ctx, ids[w.rng.IntN(len(ids))], bypass)
	}
}

// mixedScenario is 90% reads and 10% writes over a Zipf distribution
// (s=1.1), so a small set of hot keys receives most traffic, like a real
// catalogue. Writes invalidate, so the hit rate settles below 100%.
func mixedScenario(ids []int64) scenarioFn {
	return func(ctx context.Context, c *client, w *worker) sample {
		if w.zipf == nil {
			w.zipf = rand.NewZipf(w.rng, 1.1, 1, uint64(len(ids)-1))
		}
		id := ids[w.zipf.Uint64()]
		if w.rng.IntN(10) == 0 {
			return c.put(ctx, id, w.rng.IntN(1000))
		}
		return c.get(ctx, id, false)
	}
}

// batchMatrix runs POST /products/batch with 50 random ids per request for
// sequential mode and several pool sizes.
func batchMatrix(ctx context.Context, c *client, ids []int64, o options, bypass bool) []scenarioResult {
	const batchSize = 50
	conc := o.batchConc
	pick := func(w *worker) []int64 {
		out := make([]int64, batchSize)
		for i := range out {
			out[i] = ids[w.rng.IntN(len(ids))]
		}
		return out
	}
	var results []scenarioResult
	rows := []struct {
		mode    string
		workers int
	}{{"sequential", 0}, {"concurrent", 1}, {"concurrent", 4}, {"concurrent", 8}, {"concurrent", 16}, {"concurrent", 32}}
	for _, row := range rows {
		name := "batch_sequential"
		if row.mode == "concurrent" {
			name = fmt.Sprintf("batch_workers_%d", row.workers)
		}
		if !bypass {
			name += "_cached"
		}
		r := runClosedLoop(ctx, name, conc, o.dur(10*time.Second), c, func(ctx context.Context, c *client, w *worker) sample {
			return c.batch(ctx, pick(w), row.mode, row.workers, bypass)
		})
		r.Workers = row.workers
		if row.mode == "sequential" {
			r.Workers = 1
		}
		r.Mode = row.mode
		results = append(results, r)
	}
	return results
}

// stampede hammers one key with many clients while a writer invalidates it
// every 100ms. Each invalidation causes a burst of simultaneous misses; the
// metrics show how many of those reached MySQL.
func stampede(ctx context.Context, c *client, id int64, concurrency int, duration time.Duration) scenarioResult {
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		q := 0
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				q++
				c.put(ctx, id, q)
			}
		}
	})
	res := runClosedLoop(ctx, "stampede_coalescing", concurrency, duration, c, func(ctx context.Context, c *client, _ *worker) sample {
		return c.get(ctx, id, false)
	})
	close(stop)
	wg.Wait()
	return res
}

// warm reads every id once so the cache holds the whole data set.
func warm(ctx context.Context, c *client, ids []int64) {
	fmt.Printf("warming cache with %d keys...\n", len(ids))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 32)
	for _, id := range ids {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			c.get(ctx, id, false)
		})
	}
	wg.Wait()
}

// seed creates n products and returns their ids.
func seed(ctx context.Context, c *client, n int) ([]int64, error) {
	ids := make([]int64, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for i := range n {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			ids[i], errs[i] = c.create(ctx, fmt.Sprintf("bench-%d", i), fmt.Sprintf("%d.%02d", i%500+1, i%100), i%50)
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, fmt.Errorf("seed: %w", err)
		}
	}
	return ids, nil
}
