// Command loadtest drives the running API through a fixed set of scenarios
// and writes a Markdown + JSON report to docs/benchmarks/.
//
// Scenarios:
//
//	A  read_cache_hit        single-key GET, warm cache
//	B  read_mysql_only       single-key GET with Cache-Control: no-cache, no-store
//	C  mixed_zipf            90% GET / 10% PUT over a Zipf key distribution
//	D  batch_matrix          POST /products/batch, sequential vs worker pool sizes
//	E  ramp                  cache vs MySQL-only at rising concurrency
//	F  stampede_coalescing   200 clients on one hot key that is invalidated every 100ms
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"
)

type options struct {
	url         string
	products    int
	scenarios   map[string]bool
	quick       bool
	out         string
	seedOnly    bool
	batchCached bool
	concurrency int
	batchConc   int
	topology    string
	resources   string
}

// envOr lets the compose overlay configure the tool without changing flags.
func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func main() {
	var o options
	var scen string
	flag.StringVar(&o.url, "url", envOr("LOADTEST_URL", "http://127.0.0.1:8080"), "base URL of the API")
	flag.IntVar(&o.products, "products", 1000, "products to seed")
	flag.StringVar(&scen, "scenarios", "all", "comma-separated scenario letters (A-F) or all")
	flag.BoolVar(&o.quick, "quick", false, "halve scenario durations")
	flag.StringVar(&o.out, "out", envOr("LOADTEST_OUT", "docs/benchmarks"), "output directory for reports")
	flag.BoolVar(&o.seedOnly, "seed-only", false, "seed products and exit")
	flag.BoolVar(&o.batchCached, "batch-cached", false, "also run the batch matrix on the cached path")
	flag.IntVar(&o.concurrency, "concurrency", 50, "client concurrency for single-key scenarios")
	flag.IntVar(&o.batchConc, "batch-concurrency", 4, "concurrent clients for the batch matrix")
	flag.StringVar(&o.topology, "topology", envOr("LOADTEST_TOPOLOGY", "load generator and API on the host; MySQL and Redis in Docker Desktop"), "description of where each component runs, recorded in the report")
	flag.StringVar(&o.resources, "resources", envOr("LOADTEST_RESOURCES", ""), "description of CPU/memory limits applied to the services, recorded in the report")
	flag.Parse()

	o.scenarios = map[string]bool{}
	for _, s := range strings.Split(strings.ToUpper(scen), ",") {
		o.scenarios[strings.TrimSpace(s)] = true
	}
	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "loadtest:", err)
		os.Exit(1)
	}
}

func (o options) enabled(id string) bool { return o.scenarios["ALL"] || o.scenarios[id] }

func (o options) dur(d time.Duration) time.Duration {
	if o.quick {
		return d / 2
	}
	return d
}

func run(o options) error {
	ctx := context.Background()
	c := newClient(o.url)

	if err := c.waitReady(ctx, 30*time.Second); err != nil {
		return err
	}
	cfg, err := c.serverConfig(ctx)
	if err != nil {
		return fmt.Errorf("read /metrics: %w", err)
	}

	fmt.Printf("seeding %d products...\n", o.products)
	ids, err := seed(ctx, c, o.products)
	if err != nil {
		return err
	}
	if o.seedOnly {
		fmt.Printf("seeded %d products (ids %d..%d)\n", len(ids), ids[0], ids[len(ids)-1])
		return nil
	}

	rep := &report{
		Generated: time.Now(),
		Quick:     o.quick,
		Env: environment{
			OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), GoVersion: runtime.Version(),
			Topology:        o.topology,
			Resources:       o.resources,
			ServerConfig:    cfg,
			Products:        len(ids),
			TimerResolution: probeTimerResolution().String(),
		},
	}

	conc := o.concurrency
	if o.enabled("A") {
		warm(ctx, c, ids)
		rep.Single = append(rep.Single, runClosedLoop(ctx, "read_cache_hit", conc, o.dur(15*time.Second), c, getScenario(ids, false)))
	}
	if o.enabled("B") {
		rep.Single = append(rep.Single, runClosedLoop(ctx, "read_mysql_only", conc, o.dur(15*time.Second), c, getScenario(ids, true)))
	}
	if o.enabled("C") {
		warm(ctx, c, ids)
		rep.Mixed = runClosedLoop(ctx, "mixed_zipf_90r_10w", conc, o.dur(15*time.Second), c, mixedScenario(ids))
	}
	if o.enabled("D") {
		rep.Batch = batchMatrix(ctx, c, ids, o, true)
		if o.batchCached {
			warm(ctx, c, ids)
			rep.BatchCached = batchMatrix(ctx, c, ids, o, false)
		}
	}
	if o.enabled("E") {
		for _, bypass := range []bool{false, true} {
			name := "ramp_cache"
			if bypass {
				name = "ramp_mysql_only"
			} else {
				warm(ctx, c, ids)
			}
			for _, n := range []int{50, 200, 500} {
				rep.Ramp = append(rep.Ramp, runClosedLoop(ctx, name, n, o.dur(10*time.Second), c, getScenario(ids, bypass)))
			}
		}
	}
	if o.enabled("F") {
		rep.Stampede = stampede(ctx, c, ids[0], 200, o.dur(10*time.Second))
	}

	paths, err := rep.write(o.out)
	if err != nil {
		return err
	}
	fmt.Println()
	fmt.Println(rep.markdown())
	fmt.Printf("\nwrote %s\n", strings.Join(paths, ", "))
	return nil
}

// probeTimerResolution estimates the smallest non-zero interval the monotonic
// clock reports, so the report can say whether sub-millisecond percentiles
// are trustworthy on this machine.
func probeTimerResolution() time.Duration {
	best := time.Hour
	for range 2000 {
		a := time.Now()
		var b time.Time
		for {
			b = time.Now()
			if b.After(a) {
				break
			}
		}
		if d := b.Sub(a); d < best {
			best = d
		}
	}
	return best
}
