// Package service implements the product use cases on top of a store (MySQL)
// and a cache (Redis) using the cache-aside pattern.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/omarrsherif/go-cache-api/internal/cache"
	"github.com/omarrsherif/go-cache-api/internal/metrics"
	"github.com/omarrsherif/go-cache-api/internal/models"
	"github.com/omarrsherif/go-cache-api/internal/repo"
	"github.com/omarrsherif/go-cache-api/internal/workerpool"
)

// ProductStore is the source of truth. *repo.ProductRepo satisfies it.
type ProductStore interface {
	Create(ctx context.Context, p models.Product) (int64, error)
	GetByID(ctx context.Context, id int64) (models.Product, error)
	Update(ctx context.Context, p models.Product) error
	Delete(ctx context.Context, id int64) error
}

// ProductCache is the read-through cache. *cache.Cache satisfies it.
type ProductCache interface {
	Get(ctx context.Context, key string, dst any) (bool, error)
	Set(ctx context.Context, key string, v any, ttl time.Duration) error
	Delete(ctx context.Context, keys ...string) error
}

var (
	// ErrInvalidInput wraps validation failures; the message carries the detail.
	ErrInvalidInput = errors.New("invalid input")
	// ErrBatchTooLarge is returned when a batch exceeds Options.MaxBatchIDs.
	ErrBatchTooLarge = errors.New("too many ids")
)

// CacheStatus says where a read was served from.
type CacheStatus string

const (
	CacheHit    CacheStatus = "HIT"
	CacheMiss   CacheStatus = "MISS"
	CacheBypass CacheStatus = "BYPASS"
)

// ReadOptions mirrors the request's Cache-Control directives.
type ReadOptions struct {
	NoCache bool // skip the cache lookup (and coalescing); always read MySQL
	NoStore bool // do not write the result into the cache
}

// BatchMode selects how a batch is executed.
type BatchMode string

const (
	BatchSequential BatchMode = "sequential"
	BatchConcurrent BatchMode = "concurrent"
)

// BatchOptions controls GetMany.
type BatchOptions struct {
	Mode    BatchMode
	Workers int // 0 means Options.DefaultWorkers; capped at Options.MaxWorkers
	Read    ReadOptions
}

// BatchResult is the outcome of GetMany. Products keeps input order.
type BatchResult struct {
	Products []models.Product `json:"products"`
	Missing  []int64          `json:"missing"`
	Hits     int              `json:"cache_hits"`
	Misses   int              `json:"cache_misses"`
	Bypassed int              `json:"cache_bypass"`
	Workers  int              `json:"workers"`
}

// Options tunes the service.
type Options struct {
	TTL            time.Duration // cache entry lifetime
	StoreTimeout   time.Duration // bound on a coalesced MySQL fetch, independent of any one caller
	DefaultWorkers int
	MaxWorkers     int
	MaxBatchIDs    int
}

// ProductService is safe for concurrent use.
type ProductService struct {
	store ProductStore
	cache ProductCache
	m     *metrics.Metrics
	sf    singleflight.Group
	opts  Options
	log   *slog.Logger
}

// New wires a service. cache may be nil to run without caching.
func New(store ProductStore, c ProductCache, m *metrics.Metrics, opts Options, log *slog.Logger) *ProductService {
	if opts.DefaultWorkers <= 0 {
		opts.DefaultWorkers = 8
	}
	if opts.MaxWorkers <= 0 {
		opts.MaxWorkers = 64
	}
	if opts.MaxBatchIDs <= 0 {
		opts.MaxBatchIDs = 100
	}
	if opts.StoreTimeout <= 0 {
		opts.StoreTimeout = 5 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	return &ProductService{store: store, cache: c, m: m, opts: opts, log: log}
}

// Create validates and inserts a product, then returns the stored row.
// New rows are not written to the cache (write-around); the first read fills it.
func (s *ProductService) Create(ctx context.Context, p models.Product) (models.Product, error) {
	if err := validate(p); err != nil {
		return models.Product{}, err
	}
	id, err := s.store.Create(ctx, p)
	if err != nil {
		return models.Product{}, err
	}
	return s.store.GetByID(ctx, id)
}

// Get returns one product using cache-aside:
//
//  1. With NoCache, go straight to the store (no coalescing) and report BYPASS.
//  2. Try the cache. A Redis error counts as a miss and is recorded, never
//     surfaced.
//  3. On a miss, fetch through singleflight so concurrent misses for the same
//     key share one MySQL query. The shared fetch runs under a detached
//     context bounded by StoreTimeout, so one caller disconnecting does not
//     fail the others; each caller still stops waiting when its own context
//     ends.
func (s *ProductService) Get(ctx context.Context, id int64, ro ReadOptions) (models.Product, CacheStatus, error) {
	s.m.Requests.Add(1)

	if ro.NoCache || s.cache == nil {
		s.m.CacheBypass.Add(1)
		p, err := s.fetch(ctx, id, ro)
		return p, CacheBypass, err
	}

	key := cache.ProductKey(id)
	var p models.Product
	found, err := s.cache.Get(ctx, key, &p)
	if err != nil {
		s.m.RedisErrors.Add(1)
		s.log.Debug("cache get failed, falling back to store", "key", key, "err", err)
	}
	if found {
		s.m.CacheHits.Add(1)
		return p, CacheHit, nil
	}
	s.m.CacheMisses.Add(1)

	// no-store callers must not share a flight with callers who expect the
	// result to be cached, so they coalesce under a separate key.
	sfKey := key
	if ro.NoStore {
		sfKey += "|no-store"
	}
	leader := false
	ch := s.sf.DoChan(sfKey, func() (any, error) {
		leader = true
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.opts.StoreTimeout)
		defer cancel()
		return s.fetch(fctx, id, ro)
	})

	select {
	case <-ctx.Done():
		return models.Product{}, CacheMiss, ctx.Err()
	case r := <-ch:
		if !leader {
			s.m.Coalesced.Add(1)
		}
		if r.Err != nil {
			return models.Product{}, CacheMiss, r.Err
		}
		return r.Val.(models.Product), CacheMiss, nil
	}
}

// fetch reads from the store and, unless NoStore, populates the cache.
func (s *ProductService) fetch(ctx context.Context, id int64, ro ReadOptions) (models.Product, error) {
	s.m.DBReads.Add(1)
	p, err := s.store.GetByID(ctx, id)
	if err != nil {
		return models.Product{}, err
	}
	if !ro.NoStore && s.cache != nil {
		if err := s.cache.Set(ctx, cache.ProductKey(id), p, s.opts.TTL); err != nil {
			s.m.RedisErrors.Add(1)
			s.log.Debug("cache set failed", "id", id, "err", err)
		}
	}
	return p, nil
}

// GetMany reads several products, concurrently through a bounded worker pool
// unless Mode is sequential. Missing ids are reported, not treated as errors.
// Any other per-item error fails the whole batch.
func (s *ProductService) GetMany(ctx context.Context, ids []int64, bo BatchOptions) (BatchResult, error) {
	if len(ids) > s.opts.MaxBatchIDs {
		return BatchResult{}, fmt.Errorf("%w: at most %d ids per request", ErrBatchTooLarge, s.opts.MaxBatchIDs)
	}
	workers := bo.Workers
	if workers <= 0 {
		workers = s.opts.DefaultWorkers
	}
	workers = min(workers, s.opts.MaxWorkers)
	if bo.Mode == BatchSequential {
		workers = 1
	}

	type item struct {
		p      models.Product
		status CacheStatus
	}
	results, err := workerpool.Map(ctx, ids, workers, func(ctx context.Context, id int64) (item, error) {
		p, st, err := s.Get(ctx, id, bo.Read)
		return item{p: p, status: st}, err
	})
	if err != nil {
		return BatchResult{}, err
	}

	out := BatchResult{Products: make([]models.Product, 0, len(ids)), Missing: []int64{}, Workers: workers}
	for i, r := range results {
		if errors.Is(r.Err, repo.ErrNotFound) {
			out.Missing = append(out.Missing, ids[i])
			continue
		}
		if r.Err != nil {
			return BatchResult{}, r.Err
		}
		out.Products = append(out.Products, r.Value.p)
		switch r.Value.status {
		case CacheHit:
			out.Hits++
		case CacheMiss:
			out.Misses++
		case CacheBypass:
			out.Bypassed++
		}
	}
	return out, nil
}

// Update validates and writes the product, invalidates its cache entry, and
// returns the fresh row. The write happens before the invalidation so a
// reader can never re-cache a value older than the one just written for
// longer than the TTL.
func (s *ProductService) Update(ctx context.Context, p models.Product) (models.Product, error) {
	if err := validate(p); err != nil {
		return models.Product{}, err
	}
	if err := s.store.Update(ctx, p); err != nil {
		return models.Product{}, err
	}
	s.invalidate(ctx, p.ID)
	return s.store.GetByID(ctx, p.ID)
}

// Delete removes the product and its cache entry.
func (s *ProductService) Delete(ctx context.Context, id int64) error {
	if err := s.store.Delete(ctx, id); err != nil {
		return err
	}
	s.invalidate(ctx, id)
	return nil
}

// invalidate deletes the cache entry after a successful write. It runs on a
// detached context: once MySQL has committed, a cancelled request must not
// leave a stale entry in Redis for the rest of the TTL.
func (s *ProductService) invalidate(ctx context.Context, id int64) {
	if s.cache == nil {
		return
	}
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.opts.StoreTimeout)
	defer cancel()
	if err := s.cache.Delete(dctx, cache.ProductKey(id)); err != nil {
		s.m.RedisErrors.Add(1)
		s.log.Warn("cache invalidation failed; entry expires with TTL", "id", id, "err", err)
	}
}

var priceRE = regexp.MustCompile(`^\d{1,8}(\.\d{1,2})?$`)

func validate(p models.Product) error {
	name := strings.TrimSpace(p.Name)
	switch {
	case name == "":
		return fmt.Errorf("%w: name is required", ErrInvalidInput)
	case len(name) > 255:
		return fmt.Errorf("%w: name must be at most 255 characters", ErrInvalidInput)
	case !priceRE.MatchString(p.Price):
		return fmt.Errorf("%w: price must be a decimal with at most 2 fraction digits, e.g. \"19.99\"", ErrInvalidInput)
	case p.Quantity < 0:
		return fmt.Errorf("%w: quantity must not be negative", ErrInvalidInput)
	}
	return nil
}
