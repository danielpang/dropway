package analytics

// Tests for the PostHog emitter: disabled-without-key + nil-safety, and that
// Capture enqueues an event that is flushed to the ingest endpoint on Close().
// (The exact wire payload is posthog-go's contract; we assert delivery, and the
// event-shaping is covered where events are built.)

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/posthog/posthog-go"
)

// closeCountingClient is a posthog.Client that records Close calls. It embeds the
// interface (nil) so it satisfies posthog.Client; only Close is exercised here.
type closeCountingClient struct {
	posthog.Client
	closes int
}

func (c *closeCountingClient) Close() error { c.closes++; return nil }

func TestNewPostHogFromClient_BorrowsAndDoesNotClose(t *testing.T) {
	fake := &closeCountingClient{}
	em := NewPostHogFromClient(fake, nil)
	if em == nil {
		t.Fatal("want an emitter for a non-nil client")
	}
	if em.client != fake {
		t.Fatal("emitter must reuse the injected client, not build a new one")
	}
	if em.owns {
		t.Fatal("emitter must BORROW the shared client (owns=false)")
	}
	if err := em.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	if fake.closes != 0 {
		t.Fatalf("borrowed client must never be closed by the emitter, got %d Close calls", fake.closes)
	}
}

func TestNewPostHogFromClient_NilClientDisabled(t *testing.T) {
	if em := NewPostHogFromClient(nil, nil); em != nil {
		t.Fatalf("nil client must yield a nil (disabled) emitter, got %#v", em)
	}
}

func TestNewPostHog_DisabledWithoutKey(t *testing.T) {
	em, err := NewPostHog("", "https://us.i.posthog.com", "production", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if em != nil {
		t.Fatal("empty api key must yield a nil emitter (disabled)")
	}
	// A nil emitter must be safe to use.
	em.Capture(context.Background(), Event{DistinctID: "x", Event: "y"})
	if err := em.Close(); err != nil {
		t.Errorf("nil Close() = %v, want nil", err)
	}
}

func TestPostHog_CaptureFlushesOnClose(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":1}`))
	}))
	defer srv.Close()

	em, err := NewPostHog("phc_test", srv.URL, "production", nil)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if em == nil {
		t.Fatal("expected a non-nil emitter for a configured key")
	}

	em.Capture(context.Background(), Event{
		DistinctID: "org_1",
		Event:      "plan_upgraded",
		Properties: map[string]any{"to_tier": "pro"},
		Groups:     map[string]string{"organization": "org_1"},
	})

	// Close flushes pending events synchronously, so by the time it returns the
	// ingest endpoint must have received at least one batch POST.
	if err := em.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Error("expected at least one POST to the ingest endpoint after Close()")
	}
}
