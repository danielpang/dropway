// Package httpx holds small, dependency-light HTTP helpers shared across the Go
// services: a JSON response writer and a single error renderer that maps domain
// errors to the right status code + envelope.
//
// The most important mapping is quota.ExceededError → HTTP 402 with the
// ExceededError JSON body, so the dashboard can open the Stripe subscription
// modal (or the contact-sales CTA) and the CLI can print the upgrade URL
// (docs/ARCHITECTURE.md §9).
package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/danielpang/shipped/internal/quota"
)

// ErrorBody is the standard error envelope returned for non-quota failures.
// Quota failures are rendered as the quota.ExceededError shape directly (it has
// its own JSON tags), so the dashboard/CLI can read limit/current/max/etc.
type ErrorBody struct {
	Error   string `json:"error"`             // stable, machine-readable code, e.g. "unauthorized"
	Message string `json:"message,omitempty"` // human-readable detail
}

// WriteJSON writes v as JSON with the given status code and the correct
// content-type. Encode failures are logged, not surfaced, because the status
// line is already committed by the time we discover them.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("httpx: encode response", "err", err)
	}
}

// WriteError renders err to the response. The mapping is:
//
//	quota.ExceededError      → 402 Payment Required, body = the ExceededError JSON
//	ErrUnauthorized          → 401 Unauthorized
//	ErrForbidden             → 403 Forbidden
//	ErrNotFound              → 404 Not Found
//	ErrConflict              → 409 Conflict
//	ErrBadRequest            → 400 Bad Request
//	anything else            → 500 Internal Server Error (detail withheld)
//
// The concrete error string is only echoed for the explicit sentinels above;
// unknown errors are logged server-side and surfaced as a generic 500 so we
// never leak internals to a caller.
func WriteError(w http.ResponseWriter, err error) {
	if err == nil {
		WriteJSON(w, http.StatusInternalServerError, ErrorBody{Error: "internal_error"})
		return
	}

	// Quota cap crossed → 402 with the rich ExceededError body verbatim.
	if ex, ok := quota.AsExceeded(err); ok {
		WriteJSON(w, http.StatusPaymentRequired, ex)
		return
	}

	switch {
	case errors.Is(err, ErrUnauthorized):
		WriteJSON(w, http.StatusUnauthorized, ErrorBody{Error: "unauthorized", Message: err.Error()})
	case errors.Is(err, ErrForbidden):
		WriteJSON(w, http.StatusForbidden, ErrorBody{Error: "forbidden", Message: err.Error()})
	case errors.Is(err, ErrNotFound):
		WriteJSON(w, http.StatusNotFound, ErrorBody{Error: "not_found", Message: err.Error()})
	case errors.Is(err, ErrConflict):
		WriteJSON(w, http.StatusConflict, ErrorBody{Error: "conflict", Message: err.Error()})
	case errors.Is(err, ErrBadRequest):
		WriteJSON(w, http.StatusBadRequest, ErrorBody{Error: "bad_request", Message: err.Error()})
	default:
		// Unknown/unexpected: log it, return an opaque 500.
		slog.Error("httpx: unhandled error", "err", err)
		WriteJSON(w, http.StatusInternalServerError, ErrorBody{Error: "internal_error"})
	}
}

// Sentinel errors handlers can wrap (with %w) to drive WriteError's status
// mapping without importing net/http constants everywhere.
var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrBadRequest   = errors.New("bad request")
)
