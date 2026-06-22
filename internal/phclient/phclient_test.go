package phclient

import "testing"

func TestNewDisabledWithoutKey(t *testing.T) {
	c, err := New(Config{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c != nil {
		t.Fatal("want nil client when Key is empty (PostHog disabled)")
	}
}

func TestNewBuildsClientWithKey(t *testing.T) {
	c, err := New(Config{Key: "phc_test", Host: "https://example.invalid", Environment: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("want a client when Key is set")
	}
	_ = c.Close()
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("POSTHOG_KEY", "  phc_x  ") // trimmed
	t.Setenv("POSTHOG_HOST", "https://eu.i.posthog.com")
	t.Setenv("ENVIRONMENT", "staging")
	cfg := ConfigFromEnv()
	if cfg.Key != "phc_x" {
		t.Fatalf("Key: want phc_x, got %q", cfg.Key)
	}
	if cfg.Host != "https://eu.i.posthog.com" {
		t.Fatalf("Host: got %q", cfg.Host)
	}
	if cfg.Environment != "staging" {
		t.Fatalf("Environment: got %q", cfg.Environment)
	}
}
