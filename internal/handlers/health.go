package handlers

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/omarrsherif/go-cache-api/internal/metrics"
)

// Pinger is anything that can confirm a dependency is reachable.
type Pinger interface {
	PingContext(ctx context.Context) error
}

// PingFunc adapts a plain function to Pinger.
type PingFunc func(ctx context.Context) error

// PingContext calls f.
func (f PingFunc) PingContext(ctx context.Context) error { return f(ctx) }

// Health serves liveness, readiness and metrics.
type Health struct {
	db      Pinger
	cache   Pinger
	ready   *atomic.Bool
	m       *metrics.Metrics
	config  map[string]any
	started time.Time
}

// NewHealth builds the handler. ready is flipped false by the server during
// shutdown so load balancers stop routing to it. config is echoed by
// /metrics so benchmark reports can record the effective settings.
func NewHealth(db, cache Pinger, ready *atomic.Bool, m *metrics.Metrics, config map[string]any) *Health {
	return &Health{db: db, cache: cache, ready: ready, m: m, config: config, started: time.Now()}
}

// Register mounts /healthz, /readyz and /metrics.
func (h *Health) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", h.healthz)
	mux.HandleFunc("GET /readyz", h.readyz)
	mux.HandleFunc("GET /metrics", h.metrics)
}

// healthz reports liveness: the process is up and serving.
func (h *Health) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// readyz reports whether the service can serve traffic. MySQL is required;
// Redis is reported but not required because reads fall back to MySQL.
func (h *Health) readyz(w http.ResponseWriter, r *http.Request) {
	if !h.ready.Load() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "shutting_down"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	body := map[string]string{"status": "ok", "mysql": "ok", "redis": "ok"}
	status := http.StatusOK
	if err := h.db.PingContext(ctx); err != nil {
		body["status"], body["mysql"] = "unavailable", "down"
		status = http.StatusServiceUnavailable
	}
	if h.cache == nil || h.cache.PingContext(ctx) != nil {
		body["redis"] = "down"
	}
	writeJSON(w, status, body)
}

func (h *Health) metrics(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"counters":       h.m.Snapshot(),
		"config":         h.config,
		"uptime_seconds": int64(time.Since(h.started).Seconds()),
	})
}
