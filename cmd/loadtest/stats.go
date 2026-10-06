package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/omarrsherif/go-cache-api/internal/metrics"
)

// latency holds the percentiles of one sample set, in milliseconds.
type latency struct {
	Count int     `json:"count"`
	Mean  float64 `json:"mean_ms"`
	P50   float64 `json:"p50_ms"`
	P90   float64 `json:"p90_ms"`
	P95   float64 `json:"p95_ms"`
	P99   float64 `json:"p99_ms"`
	Max   float64 `json:"max_ms"`
}

// scenarioResult is the full outcome of one closed-loop run.
type scenarioResult struct {
	Name        string             `json:"name"`
	Concurrency int                `json:"concurrency"`
	Duration    string             `json:"duration"`
	Requests    int                `json:"requests"`
	RPS         float64            `json:"rps"`
	Latency     latency            `json:"latency"`
	Backend     latency            `json:"backend_latency"` // from Server-Timing: time inside the service call
	ByKind      map[string]latency `json:"by_kind,omitempty"`
	LittleMean  float64            `json:"little_mean_ms"` // concurrency / RPS, timer independent
	Errors      int                `json:"errors"`
	Timeouts    int                `json:"timeouts_504"`
	XCache      map[string]int     `json:"x_cache"`
	Metrics     metrics.Snapshot   `json:"server_metrics_delta"`
	Mode        string             `json:"mode,omitempty"`
	Workers     int                `json:"workers,omitempty"`
}

func ms(d time.Duration) float64 { return float64(d.Nanoseconds()) / 1e6 }

func percentiles(durs []time.Duration) latency {
	if len(durs) == 0 {
		return latency{}
	}
	slices.Sort(durs)
	var total time.Duration
	for _, d := range durs {
		total += d
	}
	at := func(p float64) float64 {
		i := int(p/100*float64(len(durs))+0.5) - 1
		if i < 0 {
			i = 0
		}
		if i >= len(durs) {
			i = len(durs) - 1
		}
		return ms(durs[i])
	}
	return latency{
		Count: len(durs),
		Mean:  ms(total) / float64(len(durs)),
		P50:   at(50), P90: at(90), P95: at(95), P99: at(99),
		Max: ms(durs[len(durs)-1]),
	}
}

func summarize(name string, concurrency int, duration time.Duration, samples []sample) scenarioResult {
	r := scenarioResult{
		Name: name, Concurrency: concurrency, Duration: duration.String(),
		Requests: len(samples), XCache: map[string]int{}, ByKind: map[string]latency{},
	}
	all := make([]time.Duration, 0, len(samples))
	backend := make([]time.Duration, 0, len(samples))
	byKind := map[string][]time.Duration{}
	for _, s := range samples {
		switch {
		case s.errk != errNone:
			r.Errors++
			if s.errk == errTimeout {
				r.Timeouts++
			}
			continue
		case s.status == 504:
			r.Timeouts++
			r.Errors++
			continue
		case s.status >= 400:
			r.Errors++
			continue
		}
		all = append(all, s.dur)
		if s.backend > 0 {
			backend = append(backend, s.backend)
		}
		kind := kindNames[s.kind]
		byKind[kind] = append(byKind[kind], s.dur)
		if s.xcache != xcNone {
			r.XCache[xcacheNames[s.xcache]]++
		}
	}
	r.Latency = percentiles(all)
	r.Backend = percentiles(backend)
	if len(byKind) > 1 {
		for k, v := range byKind {
			r.ByKind[k] = percentiles(v)
		}
	}
	r.RPS = float64(len(all)) / duration.Seconds()
	if r.RPS > 0 {
		r.LittleMean = float64(concurrency) / r.RPS * 1000
	}
	return r
}

func isTimeout(err error) bool {
	var ne interface{ Timeout() bool }
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return errors.Is(err, os.ErrDeadlineExceeded)
}

func (r scenarioResult) hitPct() float64 {
	total := 0
	for _, n := range r.XCache {
		total += n
	}
	if total == 0 {
		return 0
	}
	return float64(r.XCache["HIT"]) / float64(total) * 100
}

func (r scenarioResult) xcacheSummary() string {
	if len(r.XCache) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(r.XCache))
	for k := range r.XCache {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	total := 0
	for _, n := range r.XCache {
		total += n
	}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %.0f%%", k, float64(r.XCache[k])/float64(total)*100))
	}
	return strings.Join(parts, ", ")
}

func (r scenarioResult) oneLine() string {
	return fmt.Sprintf("%d req, %.0f rps, p50 %.2f ms, p95 %.2f ms, p99 %.2f ms, errors %d, %s",
		r.Requests, r.RPS, r.Latency.P50, r.Latency.P95, r.Latency.P99, r.Errors, r.xcacheSummary())
}
