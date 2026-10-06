// Package handlers exposes the product service over HTTP with JSON bodies.
package handlers

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/omarrsherif/go-cache-api/internal/models"
	"github.com/omarrsherif/go-cache-api/internal/service"
)

// ProductHandler serves the /products routes.
type ProductHandler struct {
	svc *service.ProductService
	log *slog.Logger
}

// NewProductHandler builds the handler.
func NewProductHandler(svc *service.ProductService, log *slog.Logger) *ProductHandler {
	return &ProductHandler{svc: svc, log: log}
}

// Register mounts the routes on mux.
func (h *ProductHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /products", h.create)
	mux.HandleFunc("POST /products/batch", h.batch)
	mux.HandleFunc("GET /products/{id}", h.get)
	mux.HandleFunc("PUT /products/{id}", h.update)
	mux.HandleFunc("DELETE /products/{id}", h.delete)
}

// productInput is the writable subset of a product.
type productInput struct {
	Name     string `json:"name"`
	Price    string `json:"price"`
	Quantity int    `json:"quantity"`
}

func (h *ProductHandler) create(w http.ResponseWriter, r *http.Request) {
	var in productInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, h.log, err)
		return
	}
	p, err := h.svc.Create(r.Context(), models.Product{Name: in.Name, Price: in.Price, Quantity: in.Quantity})
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/products/%d", p.ID))
	writeJSON(w, http.StatusCreated, p)
}

func (h *ProductHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	start := time.Now()
	p, status, err := h.svc.Get(r.Context(), id, readOptions(r))
	setServerTiming(w, start)
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	w.Header().Set("X-Cache", string(status))
	writeJSON(w, http.StatusOK, p)
}

// setServerTiming reports how long the service call took, excluding HTTP
// parsing and encoding, via the standard Server-Timing header.
func setServerTiming(w http.ResponseWriter, start time.Time) {
	w.Header().Set("Server-Timing", fmt.Sprintf("backend;dur=%.3f", float64(time.Since(start).Microseconds())/1000))
}

func (h *ProductHandler) update(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	var in productInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, h.log, err)
		return
	}
	p, err := h.svc.Update(r.Context(), models.Product{ID: id, Name: in.Name, Price: in.Price, Quantity: in.Quantity})
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *ProductHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	if err := h.svc.Delete(r.Context(), id); err != nil {
		writeError(w, r, h.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type batchInput struct {
	IDs []int64 `json:"ids"`
}

// batch handles POST /products/batch?mode=concurrent|sequential&workers=N.
func (h *ProductHandler) batch(w http.ResponseWriter, r *http.Request) {
	var in batchInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, h.log, err)
		return
	}
	opts := service.BatchOptions{Mode: service.BatchConcurrent, Read: readOptions(r)}
	switch mode := r.URL.Query().Get("mode"); mode {
	case "", "concurrent":
	case "sequential":
		opts.Mode = service.BatchSequential
	default:
		writeError(w, r, h.log, fmt.Errorf("%w: mode must be sequential or concurrent", errBadRequest))
		return
	}
	if ws := r.URL.Query().Get("workers"); ws != "" {
		n, err := strconv.Atoi(ws)
		if err != nil || n < 1 {
			writeError(w, r, h.log, fmt.Errorf("%w: workers must be a positive integer", errBadRequest))
			return
		}
		opts.Workers = n
	}
	start := time.Now()
	res, err := h.svc.GetMany(r.Context(), in.IDs, opts)
	setServerTiming(w, start)
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	w.Header().Set("X-Cache", fmt.Sprintf("HIT=%d;MISS=%d;BYPASS=%d", res.Hits, res.Misses, res.Bypassed))
	writeJSON(w, http.StatusOK, res)
}

func parseID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("%w: id must be a positive integer", errBadRequest)
	}
	return id, nil
}

// readOptions maps the request's Cache-Control directives: no-cache skips the
// cache lookup and no-store skips populating it.
func readOptions(r *http.Request) service.ReadOptions {
	var ro service.ReadOptions
	for _, tok := range strings.Split(r.Header.Get("Cache-Control"), ",") {
		switch strings.ToLower(strings.TrimSpace(tok)) {
		case "no-cache":
			ro.NoCache = true
		case "no-store":
			ro.NoStore = true
		}
	}
	return ro
}
