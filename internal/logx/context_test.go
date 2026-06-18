package logx

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	chimw "github.com/go-chi/chi/v5/middleware"
)

// TestFromContext_DefaultWhenAbsent asserts FromContext never returns nil: an
// empty context yields slog.Default() so callers don't have to nil-check.
func TestFromContext_DefaultWhenAbsent(t *testing.T) {
	if got := FromContext(context.Background()); got == nil {
		t.Fatal("FromContext on an empty context returned nil")
	}
	if got := FromContext(context.Background()); got != slog.Default() {
		t.Errorf("FromContext default = %p, want slog.Default() %p", got, slog.Default())
	}
}

// TestWithLogger_RoundTrip asserts a logger stashed with WithLogger is returned by
// FromContext (the request-scoped logger thread).
func TestWithLogger_RoundTrip(t *testing.T) {
	l := slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil))
	ctx := WithLogger(context.Background(), l)
	if got := FromContext(ctx); got != l {
		t.Errorf("FromContext = %p, want the installed logger %p", got, l)
	}
}

// TestFromContext_NilValueFallsBack asserts that a nil logger stored under the key
// still falls back to the default (the `&& l != nil` guard).
func TestFromContext_NilValueFallsBack(t *testing.T) {
	var nilLogger *slog.Logger
	ctx := context.WithValue(context.Background(), ctxKey{}, nilLogger)
	if got := FromContext(ctx); got != slog.Default() {
		t.Errorf("a nil stored logger should fall back to default, got %p", got)
	}
}

// TestMiddleware_RecordsStatusAndBytes drives the statusRecorder: a handler that
// writes a non-200 status and a body must have BOTH captured in the structured
// access-log line (status + byte count), proving WriteHeader/Write are recorded.
func TestMiddleware_RecordsStatusAndBytes(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, nil))

	body := "not found here"
	final := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(body))
	})
	h := chimw.RequestID(Middleware(base)(final))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/missing", nil))

	// The response itself carries the handler's status + body (statusRecorder passes through).
	if rr.Code != http.StatusNotFound {
		t.Fatalf("response status = %d, want 404", rr.Code)
	}
	if rr.Body.String() != body {
		t.Fatalf("response body = %q, want %q", rr.Body.String(), body)
	}

	// The structured log line records the captured status + byte count.
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line not JSON: %v\n%s", err, buf.String())
	}
	if line["status"] != float64(http.StatusNotFound) {
		t.Errorf("logged status = %v, want 404", line["status"])
	}
	if line["bytes"] != float64(len(body)) {
		t.Errorf("logged bytes = %v, want %d", line["bytes"], len(body))
	}
	if line["method"] != http.MethodGet || line["path"] != "/missing" {
		t.Errorf("logged method/path = %v %v", line["method"], line["path"])
	}
}

// TestMiddleware_DefaultStatus200 asserts a handler that writes a body WITHOUT an
// explicit WriteHeader is logged as 200 (the statusRecorder's default) with the
// byte count.
func TestMiddleware_DefaultStatus200(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, nil))
	final := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	h := chimw.RequestID(Middleware(base)(final))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/", nil))

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line not JSON: %v", err)
	}
	if line["status"] != float64(http.StatusOK) {
		t.Errorf("default logged status = %v, want 200", line["status"])
	}
	if line["bytes"] != float64(2) {
		t.Errorf("logged bytes = %v, want 2", line["bytes"])
	}
}

// TestRequestIDFromContext_Empty asserts the helper returns "" when chi's RequestID
// middleware hasn't run (no id installed).
func TestRequestIDFromContext_Empty(t *testing.T) {
	if got := RequestIDFromContext(context.Background()); got != "" {
		t.Errorf("RequestIDFromContext on bare context = %q, want empty", got)
	}
}

// TestSanitizeRequestID_NoHeaderPassthrough asserts the sanitizer is a no-op when
// no inbound X-Request-Id is present (chi will then generate one downstream).
func TestSanitizeRequestID_NoHeaderPassthrough(t *testing.T) {
	var sawHeader string
	h := SanitizeRequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		sawHeader = r.Header.Get(RequestIDHeader)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if sawHeader != "" {
		t.Errorf("no inbound header should remain absent, got %q", sawHeader)
	}
}

// TestSanitizeRequestID_KeepsValidLeavesUntouched asserts a well-formed inbound id
// is left on the request header (the sanitizer only strips forgeable values).
func TestSanitizeRequestID_KeepsValidLeavesUntouched(t *testing.T) {
	const good = "edge-trace-OK_1.2"
	var saw string
	h := SanitizeRequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		saw = r.Header.Get(RequestIDHeader)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(RequestIDHeader, good)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if saw != good {
		t.Errorf("valid inbound id was altered: got %q, want %q", saw, good)
	}
	if strings.ContainsAny(saw, "\r\n") {
		t.Error("valid id should never contain CR/LF")
	}
}
