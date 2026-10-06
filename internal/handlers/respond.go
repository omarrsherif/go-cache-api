package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/omarrsherif/go-cache-api/internal/repo"
	"github.com/omarrsherif/go-cache-api/internal/service"
)

// errBadRequest marks handler-level input problems (malformed JSON, bad path
// or query values). Wrap it with the detail.
var errBadRequest = errors.New("bad request")

const maxBodyBytes = 1 << 20

// statusClientClosedRequest is the nginx convention for a client that went
// away before the response was written.
const statusClientClosedRequest = 499

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// writeError maps err to a status code and a JSON envelope. Internal errors
// are logged with detail and reported to the client generically.
func writeError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	status, code, msg := mapError(err)
	if status == http.StatusInternalServerError {
		log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	}
	if status == statusClientClosedRequest {
		w.WriteHeader(status) // nobody is listening; skip the body
		return
	}
	var body errorBody
	body.Error.Code = code
	body.Error.Message = msg
	writeJSON(w, status, body)
}

func mapError(err error) (status int, code, msg string) {
	var maxBytes *http.MaxBytesError
	switch {
	case errors.Is(err, service.ErrInvalidInput):
		return http.StatusBadRequest, "validation_error", err.Error()
	case errors.Is(err, service.ErrBatchTooLarge):
		return http.StatusBadRequest, "batch_too_large", err.Error()
	case errors.Is(err, errBadRequest):
		return http.StatusBadRequest, "bad_request", err.Error()
	case errors.As(err, &maxBytes):
		return http.StatusRequestEntityTooLarge, "payload_too_large", "request body too large"
	case errors.Is(err, repo.ErrNotFound):
		return http.StatusNotFound, "not_found", "product not found"
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, "timeout", "request timed out"
	case errors.Is(err, context.Canceled):
		return statusClientClosedRequest, "client_closed_request", "client closed request"
	default:
		return http.StatusInternalServerError, "internal_error", "internal server error"
	}
}

// decodeJSON reads a JSON body into dst, rejecting unknown fields, trailing
// data, and bodies over 1 MB.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxBytes *http.MaxBytesError
		if errors.As(err, &maxBytes) {
			return err
		}
		return fmt.Errorf("%w: invalid JSON body: %s", errBadRequest, trimErr(err))
	}
	// Exactly one JSON value is allowed; anything after it (including a stray
	// closing brace) is rejected.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: unexpected data after JSON body", errBadRequest)
	}
	return nil
}

func trimErr(err error) string {
	if errors.Is(err, io.EOF) {
		return "empty body"
	}
	return err.Error()
}
