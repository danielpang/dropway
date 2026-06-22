package main

import (
	"testing"

	"github.com/posthog/posthog-go"

	"github.com/danielpang/dropway/internal/errtrack"
	"github.com/danielpang/dropway/internal/phclient"
)

// closeCountingClient is a posthog.Client that records Close calls. It embeds the
// interface (nil) so it satisfies posthog.Client; only Close is exercised here.
type closeCountingClient struct {
	posthog.Client
	closes int
}

func (c *closeCountingClient) Close() error { c.closes++; return nil }

// TestWireTelemetry_SharesOneClient is the unit test for main()'s telemetry
// wiring: it asserts the api process builds the posthog client EXACTLY ONCE and
// shares it across both error tracking and product analytics, and that neither
// seam closes the borrowed client — only the owner (main) does.
func TestWireTelemetry_SharesOneClient(t *testing.T) {
	// A PostHog key must be present so errtrack's auto-detect selects "posthog".
	t.Setenv("POSTHOG_KEY", "phc_test")
	t.Setenv("ERROR_TRACKING_PROVIDER", "")

	fake := &closeCountingClient{}
	var builds int
	orig := newPostHogClient
	newPostHogClient = func(cfg phclient.Config) (posthog.Client, error) {
		builds++
		return fake, nil
	}
	t.Cleanup(func() { newPostHogClient = orig })

	client, rep, label, emitter := wireTelemetry("api")
	_ = rep // wrapped into the logger in main; not exercised here

	if builds != 1 {
		t.Fatalf("posthog client built %d times, want exactly 1 (one client per process)", builds)
	}
	if client != fake {
		t.Fatal("wireTelemetry must return the single client it built")
	}
	if label != "posthog" {
		t.Fatalf("provider label = %q, want posthog", label)
	}
	if emitter == nil {
		t.Fatal("analytics emitter must be wired from the shared client")
	}

	// Both seams are pure borrowers (the Reporter and Emitter ports have no Close),
	// so the only thing that can close the client is the owner. Closing it once
	// (as main does) must reach the underlying client exactly once.
	_ = client.Close()
	if fake.closes != 1 {
		t.Fatalf("owner Close count = %d, want exactly 1", fake.closes)
	}
}

// TestWireTelemetry_DisabledWithoutClient verifies the no-PostHog path: a nil
// client yields a Noop reporter and a nil (disabled) emitter, and nothing panics.
func TestWireTelemetry_DisabledWithoutClient(t *testing.T) {
	t.Setenv("POSTHOG_KEY", "")
	t.Setenv("ERROR_TRACKING_PROVIDER", "")

	orig := newPostHogClient
	newPostHogClient = func(cfg phclient.Config) (posthog.Client, error) {
		return nil, nil // no key → disabled
	}
	t.Cleanup(func() { newPostHogClient = orig })

	client, rep, label, emitter := wireTelemetry("api")

	if client != nil {
		t.Fatal("no key → nil client")
	}
	if label != "none" {
		t.Fatalf("provider label = %q, want none", label)
	}
	if emitter != nil {
		t.Fatalf("no client → nil (disabled) emitter, got %#v", emitter)
	}
	if _, ok := rep.(errtrack.Noop); !ok {
		t.Fatalf("want Noop reporter, got %T", rep)
	}
}
