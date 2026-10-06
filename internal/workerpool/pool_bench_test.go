package workerpool

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// BenchmarkMap simulates 50 items that each take 1ms of I/O and shows how
// wall time falls as the pool grows.
func BenchmarkMap(b *testing.B) {
	items := seq(50)
	fn := func(_ context.Context, i int) (int, error) {
		time.Sleep(time.Millisecond)
		return i, nil
	}
	for _, workers := range []int{1, 4, 8, 16, 32} {
		b.Run(fmt.Sprintf("items=50/workers=%d", workers), func(b *testing.B) {
			for b.Loop() {
				if _, err := Map(context.Background(), items, workers, fn); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
