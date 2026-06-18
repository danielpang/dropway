package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestWriteJSON_NilBody asserts WriteJSON writes the status + content-type but no
// body when v is nil (e.g. a 204-style "headers only" response). The nil branch
// must not panic or emit a "null" body.
func TestWriteJSON_NilBody(t *testing.T) {
	rr := httptest.NewRecorder()
	WriteJSON(rr, http.StatusNoContent, nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content-type = %q", ct)
	}
	if rr.Body.Len() != 0 {
		t.Errorf("nil body should write nothing, got %q", rr.Body.String())
	}
}

// TestWriteError_NilError asserts WriteError defends against a nil error by
// rendering a generic 500 (a caller bug must not panic the handler).
func TestWriteError_NilError(t *testing.T) {
	rr := httptest.NewRecorder()
	WriteError(rr, nil)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	// The body is the opaque internal_error envelope, never a "null" or panic trace.
	if body := rr.Body.String(); !contains(body, "internal_error") {
		t.Errorf("nil-error body = %q, want internal_error envelope", body)
	}
}
