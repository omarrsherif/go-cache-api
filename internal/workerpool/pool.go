// Package workerpool runs a function over a slice with a bounded number of
// goroutines.
package workerpool

import (
	"context"
	"sync"
)

// Result is the outcome for one input item.
type Result[R any] struct {
	Value R
	Err   error
}

// Map applies fn to every item using at most workers goroutines and returns
// the results in input order.
//
// workers <= 1 runs everything inline on the calling goroutine, which gives
// an honest sequential baseline with no goroutine overhead. Per-item errors
// are recorded in Result.Err and do not stop the other items. When ctx is
// cancelled, items that have not started get ctx.Err() and Map returns
// ctx.Err() after in-flight items finish (fn receives the cancelled ctx, so
// they should finish quickly).
func Map[T, R any](ctx context.Context, items []T, workers int, fn func(context.Context, T) (R, error)) ([]Result[R], error) {
	results := make([]Result[R], len(items))

	if workers <= 1 {
		for i, it := range items {
			if err := ctx.Err(); err != nil {
				results[i].Err = err
				continue
			}
			v, err := fn(ctx, it)
			results[i] = Result[R]{Value: v, Err: err}
		}
		return results, ctx.Err()
	}

	workers = min(workers, len(items))
	jobs := make(chan int) // unbuffered so cancellation is noticed before the next item is handed out
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for i := range jobs {
				v, err := fn(ctx, items[i])
				results[i] = Result[R]{Value: v, Err: err} // each index is written by exactly one goroutine
			}
		})
	}

	for i := range items {
		select {
		case jobs <- i:
		case <-ctx.Done():
			for j := i; j < len(items); j++ {
				results[j].Err = ctx.Err()
			}
			close(jobs)
			wg.Wait()
			return results, ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()
	return results, ctx.Err()
}
