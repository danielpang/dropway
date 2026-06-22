package errtrack

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/danielpang/dropway/internal/phclient"
)

func TestFromEnvBorrowsInjectedClient(t *testing.T) {
	// No env key: auto-detect would yield "none". An explicit provider + an injected
	// client proves the reporter reuses (borrows) the caller's client.
	t.Setenv("POSTHOG_KEY", "")
	t.Setenv("ERROR_TRACKING_PROVIDER", "posthog")

	client, err := phclient.New(phclient.Config{Key: "phc_test"})
	if err != nil {
		t.Fatalf("phclient.New: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	rep, label := FromEnv("api", client)
	if label != "posthog" {
		t.Fatalf("label: want posthog, got %q", label)
	}
	pr, ok := rep.(*posthogReporter)
	if !ok {
		t.Fatalf("want *posthogReporter, got %T", rep)
	}
	if pr.client != client {
		t.Fatal("reporter must reuse the injected client, not build a new one")
	}
	// The reporter has no Close: it is a pure borrower. The shared client's lifecycle
	// belongs to the caller (the t.Cleanup Close), so there is no double-close to guard.
}

func TestFromEnvPostHogWithoutClientDegradesToNoop(t *testing.T) {
	// provider=posthog but no client injected ⇒ the posthog provider can't run and
	// FromEnv degrades to Noop rather than failing startup.
	t.Setenv("ERROR_TRACKING_PROVIDER", "posthog")
	rep, label := FromEnv("api", nil)
	if _, ok := rep.(Noop); !ok {
		t.Fatalf("want Noop when no client is injected, got %T", rep)
	}
	if label != "none (posthog init failed)" {
		t.Fatalf("label: got %q", label)
	}
}

func TestNoopIsInert(t *testing.T) {
	var n Noop
	n.CaptureException(context.Background(), errors.New("boom"), map[string]any{"k": "v"})
	base := slog.NewTextHandler(io.Discard, nil)
	if got := n.WrapSlogHandler(base); got != base {
		t.Fatalf("Noop.WrapSlogHandler should return base unchanged")
	}
}

func TestDistinctIDRoundTrip(t *testing.T) {
	ctx := context.Background()
	if got := DistinctID(ctx); got != "" {
		t.Fatalf("empty ctx: want \"\", got %q", got)
	}
	ctx = WithDistinctID(ctx, "")
	if got := DistinctID(ctx); got != "" {
		t.Fatalf("empty id is a no-op: want \"\", got %q", got)
	}
	ctx = WithDistinctID(ctx, "user-123")
	if got := DistinctID(ctx); got != "user-123" {
		t.Fatalf("want user-123, got %q", got)
	}
}

func TestFromEnvSelection(t *testing.T) {
	// One shared client is injected into every case; provider selection is driven by
	// env. The client is only consumed by the "posthog" case (others ignore it).
	client, err := phclient.New(phclient.Config{Key: "phc_test"})
	if err != nil {
		t.Fatalf("phclient.New: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	tests := []struct {
		name      string
		provider  string
		key       string
		wantLabel string
	}{
		{"unset, no key → none", "", "", "none"},
		{"unset, key present → posthog", "", "phc_test", "posthog"},
		{"explicit none", "none", "phc_test", "none"},
		{"explicit off", "off", "phc_test", "none"},
		{"unknown provider → none", "datadog", "phc_test", "none (unknown provider: datadog)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ERROR_TRACKING_PROVIDER", tc.provider)
			t.Setenv("POSTHOG_KEY", tc.key)
			rep, label := FromEnv("api", client)
			if label != tc.wantLabel {
				t.Fatalf("label: want %q, got %q", tc.wantLabel, label)
			}
			if rep == nil {
				t.Fatal("FromEnv must never return nil")
			}
		})
	}
}

func TestRegisterCustomProvider(t *testing.T) {
	var built bool
	Register("fake-test-provider", func(opts Options) (Reporter, error) {
		built = true
		if opts.Service != "serve" {
			t.Errorf("want service serve, got %q", opts.Service)
		}
		return Noop{}, nil
	})
	t.Setenv("ERROR_TRACKING_PROVIDER", "fake-test-provider")
	rep, label := FromEnv("serve", nil)
	_ = rep
	if !built {
		t.Fatal("custom constructor was not invoked")
	}
	if label != "fake-test-provider" {
		t.Fatalf("label: want fake-test-provider, got %q", label)
	}
}

func TestRegisterConstructorErrorFallsBackToNoop(t *testing.T) {
	Register("broken-test-provider", func(Options) (Reporter, error) {
		return nil, errors.New("nope")
	})
	t.Setenv("ERROR_TRACKING_PROVIDER", "broken-test-provider")
	rep, label := FromEnv("api", nil)
	if _, ok := rep.(Noop); !ok {
		t.Fatalf("want Noop fallback, got %T", rep)
	}
	if label != "none (broken-test-provider init failed)" {
		t.Fatalf("unexpected label %q", label)
	}
}

// recordingHandler is a slog.Handler that records what it sees and signals on
// `handled` after each record, so tests can synchronize on async log emission.
type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
	handled chan struct{}
}

func newRecordingHandler() *recordingHandler {
	return &recordingHandler{handled: make(chan struct{}, 16)}
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	h.records = append(h.records, r.Clone())
	h.mu.Unlock()
	select {
	case h.handled <- struct{}{}:
	default:
	}
	return nil
}
func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

func (h *recordingHandler) last() (slog.Record, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.records) == 0 {
		return slog.Record{}, false
	}
	return h.records[len(h.records)-1], true
}

func TestRecovererWrites500AndLogsPanic(t *testing.T) {
	rec := newRecordingHandler()
	old := slog.Default()
	slog.SetDefault(slog.New(rec))
	t.Cleanup(func() { slog.SetDefault(old) })

	h := Recoverer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("kaboom")
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/sites", nil))

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status: want 500, got %d", rr.Code)
	}
	got, ok := rec.last()
	if !ok {
		t.Fatal("Recoverer did not log the panic")
	}
	if got.Level != slog.LevelError {
		t.Fatalf("want Error level, got %v", got.Level)
	}
	var sawStack, sawPanic bool
	got.Attrs(func(a slog.Attr) bool {
		switch a.Key {
		case "stack":
			sawStack = a.Value.String() != ""
		case "panic":
			sawPanic = a.Value.Bool()
		}
		return true
	})
	if !sawStack || !sawPanic {
		t.Fatalf("panic log missing attrs: stack=%v panic=%v", sawStack, sawPanic)
	}
}

func TestRecovererRepanicsOnAbortHandler(t *testing.T) {
	defer func() {
		if r := recover(); r != http.ErrAbortHandler {
			t.Fatalf("want ErrAbortHandler re-panic, got %v", r)
		}
	}()
	h := Recoverer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestSafeGoRecoversPanic(t *testing.T) {
	rec := newRecordingHandler()
	old := slog.Default()
	slog.SetDefault(slog.New(rec))
	t.Cleanup(func() { slog.SetDefault(old) })

	SafeGo(context.Background(), "test-worker", func() {
		panic(errors.New("worker boom"))
	})

	select {
	case <-rec.handled:
	case <-time.After(2 * time.Second):
		t.Fatal("SafeGo did not log the recovered panic")
	}
	got, _ := rec.last()
	if got.Message != "background goroutine panic" {
		t.Fatalf("unexpected log message %q", got.Message)
	}
}
