// Package metrics holds process-wide counters for cache behaviour. They are
// plain atomics so they cost nothing on the hot path and are served as JSON
// by GET /metrics.
package metrics

import "sync/atomic"

// Metrics is the live counter set. Fields are incremented directly.
type Metrics struct {
	Requests    atomic.Int64 // single-product reads (including batch items)
	CacheHits   atomic.Int64
	CacheMisses atomic.Int64
	CacheBypass atomic.Int64 // reads that skipped the cache via Cache-Control: no-cache
	Coalesced   atomic.Int64 // misses that waited on another in-flight fetch instead of querying MySQL
	DBReads     atomic.Int64 // GetByID calls that reached MySQL
	RedisErrors atomic.Int64 // Redis operations that failed or timed out (request fell back to MySQL)
}

// Snapshot is a point-in-time copy of the counters.
type Snapshot struct {
	Requests    int64   `json:"requests"`
	CacheHits   int64   `json:"cache_hits"`
	CacheMisses int64   `json:"cache_misses"`
	CacheBypass int64   `json:"cache_bypass"`
	Coalesced   int64   `json:"coalesced"`
	DBReads     int64   `json:"db_reads"`
	RedisErrors int64   `json:"redis_errors"`
	HitRate     float64 `json:"hit_rate"` // hits / (hits + misses); bypassed reads are excluded
}

// New returns a zeroed counter set.
func New() *Metrics { return &Metrics{} }

// Snapshot copies the counters and computes the hit rate.
func (m *Metrics) Snapshot() Snapshot {
	s := Snapshot{
		Requests:    m.Requests.Load(),
		CacheHits:   m.CacheHits.Load(),
		CacheMisses: m.CacheMisses.Load(),
		CacheBypass: m.CacheBypass.Load(),
		Coalesced:   m.Coalesced.Load(),
		DBReads:     m.DBReads.Load(),
		RedisErrors: m.RedisErrors.Load(),
	}
	s.HitRate = hitRate(s.CacheHits, s.CacheMisses)
	return s
}

// Sub returns the delta between s and an earlier snapshot, with the hit rate
// recomputed over the delta. The load tester uses this per scenario.
func (s Snapshot) Sub(prev Snapshot) Snapshot {
	d := Snapshot{
		Requests:    s.Requests - prev.Requests,
		CacheHits:   s.CacheHits - prev.CacheHits,
		CacheMisses: s.CacheMisses - prev.CacheMisses,
		CacheBypass: s.CacheBypass - prev.CacheBypass,
		Coalesced:   s.Coalesced - prev.Coalesced,
		DBReads:     s.DBReads - prev.DBReads,
		RedisErrors: s.RedisErrors - prev.RedisErrors,
	}
	d.HitRate = hitRate(d.CacheHits, d.CacheMisses)
	return d
}

func hitRate(hits, misses int64) float64 {
	if hits+misses == 0 {
		return 0
	}
	return float64(hits) / float64(hits+misses)
}
