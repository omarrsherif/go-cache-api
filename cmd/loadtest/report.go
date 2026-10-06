package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type environment struct {
	OS              string         `json:"os"`
	Arch            string         `json:"arch"`
	CPUs            int            `json:"cpus"`
	GoVersion       string         `json:"go_version"`
	Topology        string         `json:"topology"`
	ServerConfig    map[string]any `json:"server_config"`
	Products        int            `json:"products_seeded"`
	TimerResolution string         `json:"timer_resolution"`
	Resources       string         `json:"resource_limits,omitempty"`
}

type report struct {
	Generated   time.Time        `json:"generated"`
	Quick       bool             `json:"quick"`
	Env         environment      `json:"environment"`
	Single      []scenarioResult `json:"single_key,omitempty"`
	Mixed       scenarioResult   `json:"mixed,omitempty"`
	Batch       []scenarioResult `json:"batch,omitempty"`
	BatchCached []scenarioResult `json:"batch_cached,omitempty"`
	Ramp        []scenarioResult `json:"ramp,omitempty"`
	Stampede    scenarioResult   `json:"stampede,omitempty"`
}

func (r *report) write(dir string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	stamp := r.Generated.Format("20060102-150405")
	js, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, err
	}
	md := r.markdown()
	var paths []string
	for name, data := range map[string][]byte{
		stamp + ".json": js, stamp + ".md": []byte(md),
		"latest.json": js, "latest.md": []byte(md),
	} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	slices.Sort(paths)
	return paths, nil
}

func (r *report) markdown() string {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	w("# go-cache-api benchmark results\n\n")
	w("Generated %s", r.Generated.Format("2006-01-02 15:04:05"))
	if r.Quick {
		w(" (quick mode: scenario durations halved)")
	}
	w("\n\n## Environment\n\n| Item | Value |\n|---|---|\n")
	w("| OS / arch | %s/%s |\n", r.Env.OS, r.Env.Arch)
	w("| CPUs | %d |\n", r.Env.CPUs)
	w("| Go | %s |\n", r.Env.GoVersion)
	w("| Topology | %s |\n", r.Env.Topology)
	w("| Products seeded | %d |\n", r.Env.Products)
	w("| Timer resolution | %s |\n", r.Env.TimerResolution)
	if r.Env.Resources != "" {
		w("| Resource limits | %s |\n", r.Env.Resources)
	}
	keys := make([]string, 0, len(r.Env.ServerConfig))
	for k := range r.Env.ServerConfig {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		w("| server %s | %v |\n", k, r.Env.ServerConfig[k])
	}
	w("\nLatencies are client-observed, measured over a %v window after a %v warm-up. "+
		"\"Little mean\" is concurrency / RPS, a timer-independent mean latency.\n", "scenario", warmup)

	if len(r.Single) > 0 {
		w("\n## 1. Single-key reads: Redis cache hit vs MySQL-only\n\n")
		w("End-to-end columns are client-observed. Backend columns come from the API's `Server-Timing` header and cover only the service call (Redis or MySQL), excluding HTTP handling.\n\n")
		w("| Scenario | Conc | Requests | RPS | p50 ms | p95 ms | p99 ms | max ms | Backend p50 ms | Backend p99 ms | Errors | X-Cache |\n|---|---|---|---|---|---|---|---|---|---|---|---|\n")
		for _, s := range r.Single {
			w("| %s | %d | %d | %.0f | %.2f | %.2f | %.2f | %.2f | %.3f | %.3f | %d | %s |\n",
				s.Name, s.Concurrency, s.Requests, s.RPS, s.Latency.P50, s.Latency.P95, s.Latency.P99, s.Latency.Max, s.Backend.P50, s.Backend.P99, s.Errors, s.xcacheSummary())
		}
		if hit, db := find(r.Single, "read_cache_hit"), find(r.Single, "read_mysql_only"); hit != nil && db != nil && hit.RPS > 0 && db.Latency.P50 > 0 {
			w("\n**Cache hit vs MySQL-only, end to end:** p50 %.2f ms vs %.2f ms (%.0f%% lower); "+
				"p99 %.2f ms vs %.2f ms (%.0f%% lower); throughput %.0f vs %.0f req/s (%.1fx).\n",
				hit.Latency.P50, db.Latency.P50, (1-hit.Latency.P50/db.Latency.P50)*100,
				hit.Latency.P99, db.Latency.P99, (1-safeDiv(hit.Latency.P99, db.Latency.P99))*100,
				hit.RPS, db.RPS, hit.RPS/db.RPS)
			if hit.Backend.P50 > 0 && db.Backend.P50 > 0 {
				w("\n**Backend only (Redis GET vs MySQL query):** p50 %.3f ms vs %.3f ms (%.1fx faster, %.0f%% lower); p99 %.3f ms vs %.3f ms (%.1fx faster).\n",
					hit.Backend.P50, db.Backend.P50, db.Backend.P50/hit.Backend.P50, (1-hit.Backend.P50/db.Backend.P50)*100,
					hit.Backend.P99, db.Backend.P99, safeDiv(db.Backend.P99, hit.Backend.P99))
			}
		}
	}

	if r.Mixed.Requests > 0 {
		s := r.Mixed
		w("\n## 2. Mixed workload: 90%% reads / 10%% writes, Zipf(1.1) key distribution\n\n")
		w("| Conc | Requests | RPS | GET p50/p99 ms | PUT p50/p99 ms | cache_hits | cache_misses | db_reads | Hit rate |\n|---|---|---|---|---|---|---|---|---|\n")
		g, p := s.ByKind["GET"], s.ByKind["PUT"]
		w("| %d | %d | %.0f | %.2f / %.2f | %.2f / %.2f | %d | %d | %d | %.1f%% |\n",
			s.Concurrency, s.Requests, s.RPS, g.P50, g.P99, p.P50, p.P99,
			s.Metrics.CacheHits, s.Metrics.CacheMisses, s.Metrics.DBReads, s.Metrics.HitRate*100)
		w("\nClient-observed X-Cache: %s. Every PUT invalidates its key, so the next read of that key misses.\n", s.xcacheSummary())
	}

	writeBatch := func(title string, rows []scenarioResult) {
		if len(rows) == 0 {
			return
		}
		w("\n## %s\n\n", title)
		w("| Mode | Workers | Conc | Requests | Batches/s | Items/s | p50 ms | p95 ms | p99 ms | Speedup vs sequential (p50) |\n|---|---|---|---|---|---|---|---|---|---|\n")
		var seqP50 float64
		for _, s := range rows {
			if s.Mode == "sequential" {
				seqP50 = s.Latency.P50
			}
		}
		for _, s := range rows {
			speed := "1.0x"
			if s.Mode != "sequential" && s.Latency.P50 > 0 {
				speed = fmt.Sprintf("%.1fx", seqP50/s.Latency.P50)
			}
			w("| %s | %d | %d | %d | %.1f | %.0f | %.2f | %.2f | %.2f | %s |\n",
				s.Mode, s.Workers, s.Concurrency, s.Requests, s.RPS, s.RPS*50, s.Latency.P50, s.Latency.P95, s.Latency.P99, speed)
		}
	}
	writeBatch("3. Batch of 50 ids: sequential vs bounded worker pool (MySQL-only path)", r.Batch)
	writeBatch("3b. Batch of 50 ids on the cached path", r.BatchCached)

	if len(r.Ramp) > 0 {
		w("\n## 4. Heavy concurrent traffic\n\n")
		w("| Path | Conc | Requests | RPS | p50 ms | p95 ms | p99 ms | Backend p50 ms | Backend p99 ms | Errors | 504s |\n|---|---|---|---|---|---|---|---|---|---|---|\n")
		for _, s := range r.Ramp {
			w("| %s | %d | %d | %.0f | %.2f | %.2f | %.2f | %.3f | %.3f | %d | %d |\n",
				s.Name, s.Concurrency, s.Requests, s.RPS, s.Latency.P50, s.Latency.P95, s.Latency.P99, s.Backend.P50, s.Backend.P99, s.Errors, s.Timeouts)
		}
	}

	if r.Stampede.Requests > 0 {
		s := r.Stampede
		m := s.Metrics
		avoided := 0.0
		if m.CacheMisses > 0 {
			avoided = (1 - float64(m.DBReads)/float64(m.CacheMisses)) * 100
		}
		w("\n## 5. Cache stampede and request coalescing\n\n")
		w("%d clients read one hot key while a writer invalidates it every 100 ms.\n\n", s.Concurrency)
		w("| Requests | RPS | p50 ms | p99 ms | cache_misses | db_reads | coalesced | MySQL reads avoided |\n|---|---|---|---|---|---|---|---|\n")
		w("| %d | %.0f | %.2f | %.2f | %d | %d | %d | %.1f%% |\n",
			s.Requests, s.RPS, s.Latency.P50, s.Latency.P99, m.CacheMisses, m.DBReads, m.Coalesced, avoided)
	}

	w("\n## Notes\n\n")
	w("- Closed-loop load generator: each client sends its next request as soon as the previous one returns.\n")
	w("- The load generator, API, Redis and MySQL all share one machine's CPU, so absolute numbers depend on the hardware; the ratios between scenarios are the point.\n")
	w("- Products are a single table read by primary key, which is MySQL's best case; the cache advantage grows with query cost and network distance to the database.\n")
	w("- Cache misses are not negatively cached; a missing id always reaches MySQL.\n")
	w("- Reproduce with `scripts\\bench.ps1` (Windows) or `make bench`.\n")
	return b.String()
}

func find(rs []scenarioResult, name string) *scenarioResult {
	for i := range rs {
		if rs[i].Name == name {
			return &rs[i]
		}
	}
	return nil
}

func safeDiv(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}
