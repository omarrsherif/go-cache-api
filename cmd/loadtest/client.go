package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/omarrsherif/go-cache-api/internal/metrics"
)

// client is a keep-alive HTTP client sized for hundreds of concurrent
// workers. Bodies are always drained so connections are reused; without that
// the generator exhausts ephemeral ports within seconds.
type client struct {
	base string
	http *http.Client
}

func newClient(base string) *client {
	return &client{
		base: strings.TrimRight(base, "/"),
		http: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        2000,
				MaxIdleConnsPerHost: 2000,
				IdleConnTimeout:     90 * time.Second,
				DisableCompression:  true,
			},
		},
	}
}

// sample is one measured request. It is kept small (24 bytes) because a full
// run records several million of them.
type sample struct {
	dur     time.Duration
	backend time.Duration // server-reported time inside the service call (Server-Timing header)
	status  int32
	kind    uint8
	xcache  uint8
	errk    uint8
}

// parseServerTiming extracts the first dur= value (milliseconds) from a
// Server-Timing header such as "backend;dur=0.412".
func parseServerTiming(h string) time.Duration {
	i := strings.Index(h, "dur=")
	if i < 0 {
		return 0
	}
	v := h[i+4:]
	if j := strings.IndexAny(v, ",; "); j >= 0 {
		v = v[:j]
	}
	ms, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0
	}
	return time.Duration(ms * float64(time.Millisecond))
}

const (
	kindGet uint8 = iota
	kindPut
	kindBatch
)

const (
	xcNone uint8 = iota
	xcHit
	xcMiss
	xcBypass
)

const (
	errNone uint8 = iota
	errTransport
	errTimeout
)

var kindNames = [...]string{"GET", "PUT", "BATCH"}
var xcacheNames = [...]string{"", "HIT", "MISS", "BYPASS"}

func parseXCache(h string) uint8 {
	switch h {
	case "HIT":
		return xcHit
	case "MISS":
		return xcMiss
	case "BYPASS":
		return xcBypass
	}
	return xcNone
}

// response is the subset of an HTTP response the tool looks at.
type response struct {
	status  int
	xcache  string
	backend time.Duration
	body    []byte
}

func (c *client) do(ctx context.Context, method, path string, body []byte, cacheControl string) (response, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return response{}, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cacheControl != "" {
		req.Header.Set("Cache-Control", cacheControl)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return response{}, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return response{status: resp.StatusCode}, err
	}
	return response{
		status:  resp.StatusCode,
		xcache:  resp.Header.Get("X-Cache"),
		backend: parseServerTiming(resp.Header.Get("Server-Timing")),
		body:    b,
	}, nil
}

func (c *client) timed(ctx context.Context, kind uint8, method, path string, body []byte, cacheControl string) sample {
	start := time.Now()
	r, err := c.do(ctx, method, path, body, cacheControl)
	s := sample{dur: time.Since(start), backend: r.backend, status: int32(r.status), kind: kind, xcache: parseXCache(r.xcache)}
	if err != nil {
		s.errk = errTransport
		if isTimeout(err) {
			s.errk = errTimeout
		}
	}
	return s
}

func (c *client) get(ctx context.Context, id int64, bypass bool) sample {
	cc := ""
	if bypass {
		cc = "no-cache, no-store"
	}
	return c.timed(ctx, kindGet, http.MethodGet, fmt.Sprintf("/products/%d", id), nil, cc)
}

func (c *client) put(ctx context.Context, id int64, quantity int) sample {
	body := fmt.Appendf(nil, `{"name":"bench-%d","price":"%d.%02d","quantity":%d}`, id, id%500+1, id%100, quantity)
	return c.timed(ctx, kindPut, http.MethodPut, fmt.Sprintf("/products/%d", id), body, "")
}

func (c *client) batch(ctx context.Context, ids []int64, mode string, workers int, bypass bool) sample {
	b, _ := json.Marshal(map[string][]int64{"ids": ids})
	path := fmt.Sprintf("/products/batch?mode=%s", mode)
	if workers > 0 {
		path += fmt.Sprintf("&workers=%d", workers)
	}
	cc := ""
	if bypass {
		cc = "no-cache, no-store"
	}
	return c.timed(ctx, kindBatch, http.MethodPost, path, b, cc)
}

func (c *client) create(ctx context.Context, name, price string, quantity int) (int64, error) {
	body := fmt.Appendf(nil, `{"name":%q,"price":%q,"quantity":%d}`, name, price, quantity)
	r, err := c.do(ctx, http.MethodPost, "/products", body, "")
	if err != nil {
		return 0, err
	}
	if r.status != http.StatusCreated {
		return 0, fmt.Errorf("create returned %d: %s", r.status, r.body)
	}
	var p struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(r.body, &p); err != nil {
		return 0, err
	}
	return p.ID, nil
}

func (c *client) waitReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		r, err := c.do(ctx, http.MethodGet, "/readyz", nil, "")
		if err == nil && r.status == http.StatusOK {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("API at %s not ready after %v (last: status=%d err=%v)", c.base, timeout, r.status, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

type metricsBody struct {
	Counters metrics.Snapshot `json:"counters"`
	Config   map[string]any   `json:"config"`
}

func (c *client) metrics(ctx context.Context) (metrics.Snapshot, error) {
	r, err := c.do(ctx, http.MethodGet, "/metrics", nil, "")
	if err != nil {
		return metrics.Snapshot{}, err
	}
	if r.status != http.StatusOK {
		return metrics.Snapshot{}, errors.New("metrics returned " + fmt.Sprint(r.status))
	}
	var mb metricsBody
	if err := json.Unmarshal(r.body, &mb); err != nil {
		return metrics.Snapshot{}, err
	}
	return mb.Counters, nil
}

func (c *client) serverConfig(ctx context.Context) (map[string]any, error) {
	r, err := c.do(ctx, http.MethodGet, "/metrics", nil, "")
	if err != nil {
		return nil, err
	}
	var mb metricsBody
	if err := json.Unmarshal(r.body, &mb); err != nil {
		return nil, err
	}
	return mb.Config, nil
}
