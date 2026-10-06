package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/omarrsherif/go-cache-api/internal/cache"
	"github.com/omarrsherif/go-cache-api/internal/models"
	"github.com/omarrsherif/go-cache-api/internal/repo"
)

func TestMissThenHit(t *testing.T) {
	e := newTestEnv(t, 0, Options{})
	id := e.seed(t, 1)[0]
	ctx := context.Background()

	p, st, err := e.svc.Get(ctx, id, ReadOptions{})
	if err != nil || st != CacheMiss || p.ID != id {
		t.Fatalf("first get: %+v %s %v", p, st, err)
	}
	if !e.mr.Exists(cache.ProductKey(id)) || e.mr.TTL(cache.ProductKey(id)) != time.Minute {
		t.Fatal("cache not populated with TTL")
	}
	_, st, err = e.svc.Get(ctx, id, ReadOptions{})
	if err != nil || st != CacheHit {
		t.Fatalf("second get: %s %v", st, err)
	}
	if e.store.calls.Load() != 1 {
		t.Fatalf("store calls = %d, want 1", e.store.calls.Load())
	}
	s := e.m.Snapshot()
	if s.CacheHits != 1 || s.CacheMisses != 1 || s.DBReads != 1 || s.HitRate != 0.5 {
		t.Fatalf("metrics: %+v", s)
	}
}

func TestUpdateInvalidates(t *testing.T) {
	e := newTestEnv(t, 0, Options{})
	id := e.seed(t, 1)[0]
	ctx := context.Background()
	key := cache.ProductKey(id)

	e.svc.Get(ctx, id, ReadOptions{})
	updated, err := e.svc.Update(ctx, models.Product{ID: id, Name: "new", Price: "2.50", Quantity: 9})
	if err != nil || updated.Name != "new" {
		t.Fatalf("Update: %+v %v", updated, err)
	}
	if e.mr.Exists(key) {
		t.Fatal("cache entry survived Update")
	}
	p, st, _ := e.svc.Get(ctx, id, ReadOptions{})
	if st != CacheMiss || p.Name != "new" || p.Quantity != 9 {
		t.Fatalf("after update: %s %+v", st, p)
	}

	if _, err := e.svc.Update(ctx, models.Product{ID: 9999, Name: "x", Price: "1"}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}
}

func TestDeleteInvalidates(t *testing.T) {
	e := newTestEnv(t, 0, Options{})
	id := e.seed(t, 1)[0]
	ctx := context.Background()

	e.svc.Get(ctx, id, ReadOptions{})
	if err := e.svc.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if e.mr.Exists(cache.ProductKey(id)) {
		t.Fatal("cache entry survived Delete")
	}
	if _, _, err := e.svc.Get(ctx, id, ReadOptions{}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("get after delete: %v", err)
	}
	if err := e.svc.Delete(ctx, id); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestBypassAndNoStore(t *testing.T) {
	e := newTestEnv(t, 0, Options{})
	id := e.seed(t, 1)[0]
	ctx := context.Background()
	key := cache.ProductKey(id)

	// no-cache + no-store: pure store read, cache untouched
	_, st, err := e.svc.Get(ctx, id, ReadOptions{NoCache: true, NoStore: true})
	if err != nil || st != CacheBypass || e.mr.Exists(key) {
		t.Fatalf("no-store: %s %v exists=%v", st, err, e.mr.Exists(key))
	}
	// no-cache alone: store read but cache populated
	_, st, _ = e.svc.Get(ctx, id, ReadOptions{NoCache: true})
	if st != CacheBypass || !e.mr.Exists(key) {
		t.Fatalf("no-cache: %s exists=%v", st, e.mr.Exists(key))
	}
	// cached now, but no-cache still goes to the store
	e.store.calls.Store(0)
	_, st, _ = e.svc.Get(ctx, id, ReadOptions{NoCache: true})
	if st != CacheBypass || e.store.calls.Load() != 1 {
		t.Fatalf("no-cache with warm cache: %s calls=%d", st, e.store.calls.Load())
	}
	if e.m.Snapshot().CacheBypass != 3 {
		t.Fatalf("bypass counter = %d", e.m.Snapshot().CacheBypass)
	}
}

func TestInvalidateSurvivesCancelledRequest(t *testing.T) {
	e := newTestEnv(t, 0, Options{})
	id := e.seed(t, 1)[0]
	e.svc.Get(context.Background(), id, ReadOptions{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the client is already gone; the store write is simulated as committed
	_ = e.store.Update(context.Background(), models.Product{ID: id, Name: "new", Price: "1"})
	e.svc.invalidate(ctx, id)
	if e.mr.Exists(cache.ProductKey(id)) {
		t.Fatal("cache entry survived invalidation on a cancelled context")
	}
}

func TestNoStoreDoesNotPoisonCoalescedReaders(t *testing.T) {
	e := newTestEnv(t, 50*time.Millisecond, Options{})
	id := e.seed(t, 1)[0]
	var wg sync.WaitGroup
	wg.Go(func() { e.svc.Get(context.Background(), id, ReadOptions{NoStore: true}) })
	time.Sleep(5 * time.Millisecond)
	wg.Go(func() { e.svc.Get(context.Background(), id, ReadOptions{}) })
	wg.Wait()
	if !e.mr.Exists(cache.ProductKey(id)) {
		t.Fatal("a no-store leader prevented a normal reader from populating the cache")
	}
}

func TestRedisDownFallsBack(t *testing.T) {
	e := newTestEnv(t, 0, Options{})
	id := e.seed(t, 1)[0]
	ctx := context.Background()
	e.mr.Close()

	p, st, err := e.svc.Get(ctx, id, ReadOptions{})
	if err != nil || st != CacheMiss || p.ID != id {
		t.Fatalf("get with redis down: %+v %s %v", p, st, err)
	}
	if _, err := e.svc.Update(ctx, models.Product{ID: id, Name: "n", Price: "1", Quantity: 1}); err != nil {
		t.Fatalf("update with redis down: %v", err)
	}
	if e.m.Snapshot().RedisErrors < 2 {
		t.Fatalf("redis errors = %d, want >= 2", e.m.Snapshot().RedisErrors)
	}
}

func TestCoalescing(t *testing.T) {
	e := newTestEnv(t, 50*time.Millisecond, Options{})
	id := e.seed(t, 1)[0]
	const n = 100

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Go(func() {
			<-start
			_, _, errs[i] = e.svc.Get(context.Background(), id, ReadOptions{})
		})
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	s := e.m.Snapshot()
	if e.store.calls.Load() != 1 || s.DBReads != 1 {
		t.Fatalf("store calls = %d, db_reads = %d, want 1", e.store.calls.Load(), s.DBReads)
	}
	if s.CacheMisses != n || s.Coalesced != n-1 {
		t.Fatalf("misses = %d coalesced = %d, want %d and %d", s.CacheMisses, s.Coalesced, n, n-1)
	}
}

func TestCoalescingLeaderCancelDoesNotFailFollowers(t *testing.T) {
	e := newTestEnv(t, 200*time.Millisecond, Options{})
	id := e.seed(t, 1)[0]

	leaderCtx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	var leaderErr, followerErr error
	var followerProduct models.Product
	wg.Go(func() { _, _, leaderErr = e.svc.Get(leaderCtx, id, ReadOptions{}) })
	time.Sleep(10 * time.Millisecond)
	wg.Go(func() { followerProduct, _, followerErr = e.svc.Get(context.Background(), id, ReadOptions{}) })
	time.Sleep(10 * time.Millisecond)

	cancel()
	wg.Wait()

	if !errors.Is(leaderErr, context.Canceled) {
		t.Fatalf("leader err = %v, want Canceled", leaderErr)
	}
	if followerErr != nil || followerProduct.ID != id {
		t.Fatalf("follower: %+v %v", followerProduct, followerErr)
	}
	if e.store.calls.Load() != 1 {
		t.Fatalf("store calls = %d, want 1", e.store.calls.Load())
	}
}

func TestLeaderReturnsPromptlyOnCancel(t *testing.T) {
	e := newTestEnv(t, 500*time.Millisecond, Options{})
	id := e.seed(t, 1)[0]
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := e.svc.Get(ctx, id, ReadOptions{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatal("Get waited for the store instead of honouring the caller's deadline")
	}
}

func TestStoreTimeout(t *testing.T) {
	e := newTestEnv(t, 500*time.Millisecond, Options{StoreTimeout: 30 * time.Millisecond})
	id := e.seed(t, 1)[0]
	_, _, err := e.svc.Get(context.Background(), id, ReadOptions{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
}

func TestGetMany(t *testing.T) {
	e := newTestEnv(t, time.Millisecond, Options{DefaultWorkers: 4, MaxWorkers: 8, MaxBatchIDs: 10})
	ids := e.seed(t, 5)
	ctx := context.Background()

	e.svc.Get(ctx, ids[0], ReadOptions{}) // warm one
	req := []int64{ids[0], 9999, ids[1], ids[2], 8888}

	seq, err := e.svc.GetMany(ctx, req, BatchOptions{Mode: BatchSequential})
	if err != nil {
		t.Fatal(err)
	}
	if len(seq.Products) != 3 || seq.Products[0].ID != ids[0] || seq.Products[2].ID != ids[2] {
		t.Fatalf("sequential products: %+v", seq.Products)
	}
	if len(seq.Missing) != 2 || seq.Missing[0] != 9999 || seq.Missing[1] != 8888 {
		t.Fatalf("missing: %v", seq.Missing)
	}
	if seq.Hits != 1 || seq.Misses != 2 || seq.Workers != 1 {
		t.Fatalf("counts: %+v", seq)
	}

	e.mr.FlushAll()
	e.store.peak.Store(0)
	conc, err := e.svc.GetMany(ctx, req, BatchOptions{Mode: BatchConcurrent, Workers: 100})
	if err != nil {
		t.Fatal(err)
	}
	if conc.Workers != 8 || e.store.peak.Load() > 8 {
		t.Fatalf("workers not capped: workers=%d peak=%d", conc.Workers, e.store.peak.Load())
	}
	if len(conc.Products) != 3 || len(conc.Missing) != 2 || conc.Products[1].ID != ids[1] {
		t.Fatalf("concurrent result differs: %+v", conc)
	}

	if _, err := e.svc.GetMany(ctx, make([]int64, 11), BatchOptions{}); !errors.Is(err, ErrBatchTooLarge) {
		t.Fatalf("too large: %v", err)
	}

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := e.svc.GetMany(cctx, req, BatchOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled batch: %v", err)
	}

	empty, err := e.svc.GetMany(ctx, nil, BatchOptions{})
	if err != nil || len(empty.Products) != 0 || len(empty.Missing) != 0 {
		t.Fatalf("empty batch: %+v %v", empty, err)
	}
}

func TestValidation(t *testing.T) {
	e := newTestEnv(t, 0, Options{})
	cases := map[string]models.Product{
		"empty name":        {Name: "  ", Price: "1.00"},
		"long name":         {Name: string(make([]byte, 256)), Price: "1.00"},
		"price 3 decimals":  {Name: "a", Price: "19.999"},
		"price text":        {Name: "a", Price: "cheap"},
		"negative quantity": {Name: "a", Price: "1.00", Quantity: -1},
	}
	for name, p := range cases {
		if _, err := e.svc.Create(context.Background(), p); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := e.svc.Create(context.Background(), models.Product{Name: "ok", Price: "5"}); err != nil {
		t.Errorf("valid product rejected: %v", err)
	}
}

func TestNilCache(t *testing.T) {
	store := newFakeStore(0)
	svc := New(store, nil, newTestEnv(t, 0, Options{}).m, Options{}, nil)
	p, err := svc.Create(context.Background(), models.Product{Name: "a", Price: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, st, err := svc.Get(context.Background(), p.ID, ReadOptions{}); err != nil || st != CacheBypass {
		t.Fatalf("nil cache get: %s %v", st, err)
	}
	if _, err := svc.Update(context.Background(), models.Product{ID: p.ID, Name: "b", Price: "1"}); err != nil {
		t.Fatal(err)
	}
}
