package main

import (
	"testing"
	"time"
)

func TestParseServerTiming(t *testing.T) {
	cases := map[string]time.Duration{
		"backend;dur=0.412":          412 * time.Microsecond,
		"backend;dur=12.5, db;dur=3": 12500 * time.Microsecond,
		"cache;desc=\"hit\";dur=0.1": 100 * time.Microsecond,
		"":                           0,
		"backend":                    0,
		"backend;dur=abc":            0,
	}
	for in, want := range cases {
		if got := parseServerTiming(in); got != want {
			t.Errorf("%q: got %v want %v", in, got, want)
		}
	}
}

func TestPercentiles(t *testing.T) {
	durs := make([]time.Duration, 100)
	for i := range durs {
		durs[i] = time.Duration(100-i) * time.Millisecond // unsorted on purpose
	}
	l := percentiles(durs)
	if l.Count != 100 || l.P50 != 50 || l.P99 != 99 || l.Max != 100 || l.Mean != 50.5 {
		t.Fatalf("unexpected percentiles: %+v", l)
	}
	if z := percentiles(nil); z.Count != 0 || z.P50 != 0 {
		t.Fatalf("empty input: %+v", z)
	}
}

func TestSummarizeCountsErrorsAndXCache(t *testing.T) {
	samples := []sample{
		{dur: time.Millisecond, backend: 200 * time.Microsecond, status: 200, xcache: xcHit},
		{dur: 2 * time.Millisecond, backend: 900 * time.Microsecond, status: 200, xcache: xcMiss},
		{dur: 3 * time.Millisecond, status: 504},
		{dur: 4 * time.Millisecond, status: 500},
		{dur: 5 * time.Millisecond, errk: errTimeout},
	}
	r := summarize("t", 10, time.Second, samples)
	if r.Requests != 5 || r.Errors != 3 || r.Timeouts != 2 {
		t.Fatalf("counts: %+v", r)
	}
	if r.XCache["HIT"] != 1 || r.XCache["MISS"] != 1 || r.Latency.Count != 2 || r.Backend.Count != 2 {
		t.Fatalf("tallies: %+v", r)
	}
	if r.RPS != 2 || r.LittleMean != 5000 {
		t.Fatalf("rps=%v little=%v", r.RPS, r.LittleMean)
	}
}
