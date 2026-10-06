package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/omarrsherif/go-cache-api/internal/cache"
	"github.com/omarrsherif/go-cache-api/internal/metrics"
	"github.com/omarrsherif/go-cache-api/internal/models"
	"github.com/omarrsherif/go-cache-api/internal/repo"
	"github.com/omarrsherif/go-cache-api/internal/service"
)

// memStore is a minimal in-memory ProductStore with optional latency.
type memStore struct {
	mu      sync.Mutex
	rows    map[int64]models.Product
	next    int64
	latency time.Duration
	peak    atomic.Int64
	inUse   atomic.Int64
}

func (s *memStore) wait(ctx context.Context) error {
	n := s.inUse.Add(1)
	defer s.inUse.Add(-1)
	if n > s.peak.Load() {
		s.peak.Store(n)
	}
	if s.latency == 0 {
		return ctx.Err()
	}
	select {
	case <-time.After(s.latency):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *memStore) Create(ctx context.Context, p models.Product) (int64, error) {
	if err := s.wait(ctx); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	p.ID = s.next
	p.CreatedAt, p.UpdatedAt = time.Now(), time.Now()
	s.rows[p.ID] = p
	return p.ID, nil
}

func (s *memStore) GetByID(ctx context.Context, id int64) (models.Product, error) {
	if err := s.wait(ctx); err != nil {
		return models.Product{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.rows[id]
	if !ok {
		return models.Product{}, repo.ErrNotFound
	}
	return p, nil
}

func (s *memStore) Update(ctx context.Context, p models.Product) error {
	if err := s.wait(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.rows[p.ID]
	if !ok {
		return repo.ErrNotFound
	}
	cur.Name, cur.Price, cur.Quantity = p.Name, p.Price, p.Quantity
	s.rows[p.ID] = cur
	return nil
}

func (s *memStore) Delete(ctx context.Context, id int64) error {
	if err := s.wait(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rows[id]; !ok {
		return repo.ErrNotFound
	}
	delete(s.rows, id)
	return nil
}

type env struct {
	srv   *httptest.Server
	store *memStore
	mr    *miniredis.Miniredis
	m     *metrics.Metrics
	ready *atomic.Bool
}

func newEnv(t *testing.T, latency, timeout time.Duration) env {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mr := miniredis.RunT(t)
	c := cache.New(mr.Addr(), 200*time.Millisecond, 32)
	t.Cleanup(func() { c.Close() })
	store := &memStore{rows: map[int64]models.Product{}, latency: latency}
	m := metrics.New()
	svc := service.New(store, c, m, service.Options{TTL: time.Minute, MaxWorkers: 8, MaxBatchIDs: 100}, log)

	ready := &atomic.Bool{}
	ready.Store(true)
	mux := http.NewServeMux()
	NewProductHandler(svc, log).Register(mux)
	NewHealth(PingFunc(func(context.Context) error { return nil }), PingFunc(c.Ping), ready, m, map[string]any{"ttl": "1m"}).Register(mux)
	mux.HandleFunc("GET /panic", func(http.ResponseWriter, *http.Request) { panic("boom") })

	h := Chain(mux, WithRecover(log), WithLogging(log), WithTimeout(timeout))
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return env{srv: srv, store: store, mr: mr, m: m, ready: ready}
}

func (e env) do(t *testing.T, method, path, body string, headers ...string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func errCode(t *testing.T, b []byte) string {
	t.Helper()
	var body errorBody
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatalf("not an error envelope: %s", b)
	}
	return body.Error.Code
}

func TestCRUD(t *testing.T) {
	e := newEnv(t, 0, 5*time.Second)

	resp, b := e.do(t, "POST", "/products", `{"name":"Widget","price":"19.99","quantity":3}`)
	if resp.StatusCode != 201 {
		t.Fatalf("create: %d %s", resp.StatusCode, b)
	}
	var p models.Product
	json.Unmarshal(b, &p)
	if p.ID == 0 || p.Name != "Widget" || resp.Header.Get("Location") != fmt.Sprintf("/products/%d", p.ID) {
		t.Fatalf("create response: %+v location=%q", p, resp.Header.Get("Location"))
	}
	path := fmt.Sprintf("/products/%d", p.ID)

	resp, _ = e.do(t, "GET", path, "")
	if resp.StatusCode != 200 || resp.Header.Get("X-Cache") != "MISS" {
		t.Fatalf("first get: %d %s", resp.StatusCode, resp.Header.Get("X-Cache"))
	}
	if st := resp.Header.Get("Server-Timing"); !strings.HasPrefix(st, "backend;dur=") {
		t.Fatalf("Server-Timing = %q", st)
	}
	resp, _ = e.do(t, "GET", path, "")
	if resp.Header.Get("X-Cache") != "HIT" {
		t.Fatalf("second get: %s", resp.Header.Get("X-Cache"))
	}
	resp, _ = e.do(t, "GET", path, "", "Cache-Control", "no-cache")
	if resp.Header.Get("X-Cache") != "BYPASS" {
		t.Fatalf("bypass get: %s", resp.Header.Get("X-Cache"))
	}

	resp, b = e.do(t, "PUT", path, `{"name":"Gadget","price":"5.00","quantity":1}`)
	var up models.Product
	json.Unmarshal(b, &up)
	if resp.StatusCode != 200 || up.Name != "Gadget" {
		t.Fatalf("update: %d %s", resp.StatusCode, b)
	}
	resp, _ = e.do(t, "GET", path, "")
	if resp.Header.Get("X-Cache") != "MISS" {
		t.Fatalf("get after update should miss, got %s", resp.Header.Get("X-Cache"))
	}

	resp, _ = e.do(t, "DELETE", path, "")
	if resp.StatusCode != 204 {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	resp, b = e.do(t, "GET", path, "")
	if resp.StatusCode != 404 || errCode(t, b) != "not_found" {
		t.Fatalf("get after delete: %d %s", resp.StatusCode, b)
	}
	resp, b = e.do(t, "DELETE", path, "")
	if resp.StatusCode != 404 || errCode(t, b) != "not_found" {
		t.Fatalf("second delete: %d %s", resp.StatusCode, b)
	}
}

func TestBadRequests(t *testing.T) {
	e := newEnv(t, 0, 5*time.Second)
	cases := []struct {
		name, method, path, body string
		status                   int
		code                     string
	}{
		{"malformed json", "POST", "/products", `{"name":`, 400, "bad_request"},
		{"unknown field", "POST", "/products", `{"name":"a","price":"1","id":5}`, 400, "bad_request"},
		{"empty body", "POST", "/products", ``, 400, "bad_request"},
		{"trailing brace", "POST", "/products", `{"name":"a","price":"1"}}`, 400, "bad_request"},
		{"second value", "POST", "/products", `{"name":"a","price":"1"} {}`, 400, "bad_request"},
		{"validation", "POST", "/products", `{"name":"","price":"1"}`, 400, "validation_error"},
		{"bad price", "PUT", "/products/1", `{"name":"a","price":"1.234"}`, 400, "validation_error"},
		{"non-numeric id", "GET", "/products/abc", ``, 400, "bad_request"},
		{"zero id", "GET", "/products/0", ``, 400, "bad_request"},
		{"missing", "GET", "/products/123456", ``, 404, "not_found"},
		{"bad batch mode", "POST", "/products/batch?mode=fast", `{"ids":[1]}`, 400, "bad_request"},
		{"bad batch workers", "POST", "/products/batch?workers=0", `{"ids":[1]}`, 400, "bad_request"},
		{"too large", "POST", "/products", `{"name":"` + strings.Repeat("x", maxBodyBytes+10) + `","price":"1"}`, 413, "payload_too_large"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, b := e.do(t, tc.method, tc.path, tc.body)
			if resp.StatusCode != tc.status || errCode(t, b) != tc.code {
				t.Fatalf("got %d %s, want %d %s", resp.StatusCode, b, tc.status, tc.code)
			}
		})
	}

	ids := make([]string, 101)
	for i := range ids {
		ids[i] = "1"
	}
	resp, b := e.do(t, "POST", "/products/batch", `{"ids":[`+strings.Join(ids, ",")+`]}`)
	if resp.StatusCode != 400 || errCode(t, b) != "batch_too_large" {
		t.Fatalf("101 ids: %d %s", resp.StatusCode, b)
	}
}

func TestBatch(t *testing.T) {
	e := newEnv(t, time.Millisecond, 5*time.Second)
	var ids []int64
	for i := range 5 {
		_, b := e.do(t, "POST", "/products", fmt.Sprintf(`{"name":"p%d","price":"1.00","quantity":%d}`, i, i))
		var p models.Product
		json.Unmarshal(b, &p)
		ids = append(ids, p.ID)
	}
	e.do(t, "GET", fmt.Sprintf("/products/%d", ids[0]), "") // warm one

	body := fmt.Sprintf(`{"ids":[%d,9999,%d,%d]}`, ids[0], ids[1], ids[2])
	for _, q := range []string{"", "?mode=sequential", "?mode=concurrent&workers=2", "?workers=9999"} {
		e.store.peak.Store(0)
		resp, b := e.do(t, "POST", "/products/batch"+q, body)
		if resp.StatusCode != 200 {
			t.Fatalf("%q: %d %s", q, resp.StatusCode, b)
		}
		var res service.BatchResult
		json.Unmarshal(b, &res)
		if len(res.Products) != 3 || len(res.Missing) != 1 || res.Missing[0] != 9999 || res.Products[1].ID != ids[1] {
			t.Fatalf("%q: %+v", q, res)
		}
		if q == "?workers=9999" && (res.Workers != 8 || e.store.peak.Load() > 8) {
			t.Fatalf("workers not capped: %d peak=%d", res.Workers, e.store.peak.Load())
		}
		if q == "?mode=sequential" && res.Workers != 1 {
			t.Fatalf("sequential workers = %d", res.Workers)
		}
		if !strings.HasPrefix(resp.Header.Get("X-Cache"), "HIT=") {
			t.Fatalf("X-Cache = %q", resp.Header.Get("X-Cache"))
		}
	}

	resp, b := e.do(t, "POST", "/products/batch", body, "Cache-Control", "no-cache, no-store")
	var res service.BatchResult
	json.Unmarshal(b, &res)
	if resp.StatusCode != 200 || res.Bypassed != 3 || resp.Header.Get("X-Cache") != "HIT=0;MISS=0;BYPASS=3" {
		t.Fatalf("bypass batch: %d %+v %s", resp.StatusCode, res, resp.Header.Get("X-Cache"))
	}
}

func TestTimeout(t *testing.T) {
	e := newEnv(t, 300*time.Millisecond, 50*time.Millisecond)
	// Create bypasses the request timeout only via the store latency; use a
	// direct store insert instead so setup does not time out.
	id, _ := e.store.Create(context.Background(), models.Product{Name: "slow", Price: "1"})
	resp, b := e.do(t, "GET", fmt.Sprintf("/products/%d", id), "", "Cache-Control", "no-cache")
	if resp.StatusCode != 504 || errCode(t, b) != "timeout" {
		t.Fatalf("got %d %s", resp.StatusCode, b)
	}
}

func TestHealthAndMetrics(t *testing.T) {
	e := newEnv(t, 0, 5*time.Second)
	resp, _ := e.do(t, "GET", "/healthz", "")
	if resp.StatusCode != 200 {
		t.Fatalf("healthz: %d", resp.StatusCode)
	}
	resp, b := e.do(t, "GET", "/readyz", "")
	if resp.StatusCode != 200 || !bytes.Contains(b, []byte(`"redis":"ok"`)) {
		t.Fatalf("readyz: %d %s", resp.StatusCode, b)
	}
	e.mr.Close()
	resp, b = e.do(t, "GET", "/readyz", "")
	if resp.StatusCode != 200 || !bytes.Contains(b, []byte(`"redis":"down"`)) {
		t.Fatalf("readyz with redis down: %d %s", resp.StatusCode, b)
	}
	e.ready.Store(false)
	resp, _ = e.do(t, "GET", "/readyz", "")
	if resp.StatusCode != 503 {
		t.Fatalf("readyz while shutting down: %d", resp.StatusCode)
	}

	resp, b = e.do(t, "GET", "/metrics", "")
	var out struct {
		Counters metrics.Snapshot `json:"counters"`
		Config   map[string]any   `json:"config"`
	}
	if err := json.Unmarshal(b, &out); err != nil || resp.StatusCode != 200 || out.Config["ttl"] != "1m" {
		t.Fatalf("metrics: %d %s %v", resp.StatusCode, b, err)
	}
}

func TestPanicRecovered(t *testing.T) {
	e := newEnv(t, 0, 5*time.Second)
	resp, b := e.do(t, "GET", "/panic", "")
	if resp.StatusCode != 500 || errCode(t, b) != "internal_error" {
		t.Fatalf("got %d %s", resp.StatusCode, b)
	}
}

func TestReadOptions(t *testing.T) {
	cases := map[string]service.ReadOptions{
		"":                      {},
		"no-cache":              {NoCache: true},
		"No-Store":              {NoStore: true},
		"max-age=0, no-cache":   {NoCache: true},
		"no-cache , no-store":   {NoCache: true, NoStore: true},
		"private, must-revalid": {},
	}
	for header, want := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Cache-Control", header)
		if got := readOptions(r); got != want {
			t.Errorf("%q: got %+v want %+v", header, got, want)
		}
	}
}

func TestMapErrorUnknownIs500(t *testing.T) {
	status, code, _ := mapError(errors.New("db exploded"))
	if status != 500 || code != "internal_error" {
		t.Fatalf("got %d %s", status, code)
	}
}
