package service

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/omarrsherif/go-cache-api/internal/cache"
	"github.com/omarrsherif/go-cache-api/internal/metrics"
	"github.com/omarrsherif/go-cache-api/internal/models"
	"github.com/omarrsherif/go-cache-api/internal/repo"
)

// fakeStore is an in-memory ProductStore with optional simulated latency,
// call counting, and peak-concurrency tracking.
type fakeStore struct {
	mu       sync.Mutex
	rows     map[int64]models.Product
	nextID   int64
	latency  time.Duration
	calls    atomic.Int64
	inFlight atomic.Int64
	peak     atomic.Int64
}

func newFakeStore(latency time.Duration) *fakeStore {
	return &fakeStore{rows: map[int64]models.Product{}, latency: latency}
}

func (f *fakeStore) wait(ctx context.Context) error {
	f.calls.Add(1)
	n := f.inFlight.Add(1)
	defer f.inFlight.Add(-1)
	for {
		p := f.peak.Load()
		if n <= p || f.peak.CompareAndSwap(p, n) {
			break
		}
	}
	if f.latency == 0 {
		return ctx.Err()
	}
	select {
	case <-time.After(f.latency):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *fakeStore) Create(ctx context.Context, p models.Product) (int64, error) {
	if err := f.wait(ctx); err != nil {
		return 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	p.ID = f.nextID
	p.CreatedAt = time.Now()
	p.UpdatedAt = p.CreatedAt
	f.rows[p.ID] = p
	return p.ID, nil
}

func (f *fakeStore) GetByID(ctx context.Context, id int64) (models.Product, error) {
	if err := f.wait(ctx); err != nil {
		return models.Product{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.rows[id]
	if !ok {
		return models.Product{}, repo.ErrNotFound
	}
	return p, nil
}

func (f *fakeStore) Update(ctx context.Context, p models.Product) error {
	if err := f.wait(ctx); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.rows[p.ID]
	if !ok {
		return repo.ErrNotFound
	}
	cur.Name, cur.Price, cur.Quantity = p.Name, p.Price, p.Quantity
	cur.UpdatedAt = time.Now()
	f.rows[p.ID] = cur
	return nil
}

func (f *fakeStore) Delete(ctx context.Context, id int64) error {
	if err := f.wait(ctx); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.rows[id]; !ok {
		return repo.ErrNotFound
	}
	delete(f.rows, id)
	return nil
}

type testEnv struct {
	svc   *ProductService
	store *fakeStore
	mr    *miniredis.Miniredis
	m     *metrics.Metrics
}

func newTestEnv(t testing.TB, latency time.Duration, opts Options) testEnv {
	t.Helper()
	mr := miniredis.RunT(t)
	c := cache.New(mr.Addr(), 200*time.Millisecond, 64)
	t.Cleanup(func() { c.Close() })
	store := newFakeStore(latency)
	m := metrics.New()
	if opts.TTL == 0 {
		opts.TTL = time.Minute
	}
	svc := New(store, c, m, opts, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return testEnv{svc: svc, store: store, mr: mr, m: m}
}

func (e testEnv) seed(t testing.TB, n int) []int64 {
	t.Helper()
	ids := make([]int64, 0, n)
	for i := range n {
		p, err := e.svc.Create(context.Background(), models.Product{Name: "p", Price: "1.00", Quantity: i})
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
		ids = append(ids, p.ID)
	}
	e.store.calls.Store(0)
	e.store.peak.Store(0)
	return ids
}
