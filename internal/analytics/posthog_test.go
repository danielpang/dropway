package analytics

// Tests for the PostHog emitter: nil-safety, that it borrows the injected client,
// and that a captured event is flushed to the ingest endpoint when the OWNER
// (composition root) closes the shared client. (The exact wire payload is
// posthog-go's contract; we assert delivery, and event-shaping is covered where
// events are built.)

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/danielpang/dropway/internal/phclient"
)

func TestNewPostHogFromClient_BorrowsInjectedClient(t *testing.T) {
	client, err := phclient.New(phclient.Config{Key: "phc_test"})
	if err != nil {
		t.Fatalf("phclient.New: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	em := NewPostHogFromClient(client, nil)
	if em == nil {
		t.Fatal("want an emitter for a non-nil client")
	}
	if em.client != client {
		t.Fatal("emitter must reuse the injected client, not build a new one")
	}
	// The emitter has no Close: it is a pure borrower, so it cannot close the shared
	// client. Ownership lives with the caller (verified by the t.Cleanup Close).
}

func TestNewPostHogFromClient_NilClientDisabled(t *testing.T) {
	em := NewPostHogFromClient(nil, nil)
	if em != nil {
		t.Fatalf("nil client must yield a nil (disabled) emitter, got %#v", em)
	}
	// A nil emitter must be safe to use as the Emitter interface.
	var e Emitter = em
	_ = e // a nil *PostHog assigned to Emitter is only safe to hold, not to call.
}

func TestPostHog_CaptureFlushesWhenOwnerClosesClient(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":1}`))
	}))
	defer srv.Close()

	// The composition root builds + owns the client; the emitter borrows it.
	client, err := phclient.New(phclient.Config{Key: "phc_test", Host: srv.URL, Environment: "production"})
	if err != nil {
		t.Fatalf("phclient.New: %v", err)
	}
	em := NewPostHogFromClient(client, nil)
	if em == nil {
		t.Fatal("expected a non-nil emitter for a configured client")
	}

	em.Capture(context.Background(), Event{
		DistinctID: "org_1",
		Event:      "plan_upgraded",
		Properties: map[string]any{"to_tier": "pro"},
		Groups:     map[string]string{"organization": "org_1"},
	})

	// The OWNER closing the client flushes pending events synchronously, so the
	// ingest endpoint must have received at least one batch POST by the time it
	// returns.
	if err := client.Close(); err != nil {
		t.Errorf("client.Close(): %v", err)
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Error("expected at least one POST to the ingest endpoint after the owner closed the client")
	}
}
