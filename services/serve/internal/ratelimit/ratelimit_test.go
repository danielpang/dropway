// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLimiter_AllowsUpToLimitThenBlocks(t *testing.T) {
	l := New(3, time.Minute)
	id := "ip:1.2.3.4"
	for i := 0; i < 3; i++ {
		if r := l.Allow(id); !r.Allowed {
			t.Fatalf("request %d should be allowed", i)
		}
	}
	r := l.Allow(id)
	if r.Allowed {
		t.Fatalf("4th request should be blocked")
	}
	if r.RetryAfterSeconds < 1 {
		t.Errorf("blocked result should set Retry-After, got %d", r.RetryAfterSeconds)
	}
}

func TestLimiter_DisabledFailsOpen(t *testing.T) {
	l := New(0, time.Minute) // disabled
	for i := 0; i < 1000; i++ {
		if r := l.Allow("ip:x"); !r.Allowed {
			t.Fatalf("disabled limiter must always allow")
		}
	}
	// A nil limiter also fails open.
	var nilL *Limiter
	if r := nilL.Allow("ip:x"); !r.Allowed {
		t.Fatalf("nil limiter must always allow")
	}
}

func TestLimiter_PerIdentityIsolation(t *testing.T) {
	l := New(1, time.Minute)
	if r := l.Allow("ip:a"); !r.Allowed {
		t.Fatal("a first request allowed")
	}
	if r := l.Allow("ip:a"); r.Allowed {
		t.Fatal("a second request should block")
	}
	// A different identity has its own fresh window.
	if r := l.Allow("ip:b"); !r.Allowed {
		t.Fatal("b first request should be allowed (separate identity)")
	}
}

func TestIdentity(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://acme.example/", nil)
	if got := Identity(r, "acme.example"); got != "host:acme.example" {
		t.Errorf("no IP header → host identity; got %q", got)
	}
	r.Header.Set("CF-Connecting-IP", "9.9.9.9")
	if got := Identity(r, "acme.example"); got != "ip:9.9.9.9" {
		t.Errorf("CF-Connecting-IP preferred; got %q", got)
	}
	r2 := httptest.NewRequest(http.MethodGet, "http://acme.example/", nil)
	r2.Header.Set("X-Real-IP", "8.8.8.8")
	if got := Identity(r2, "acme.example"); got != "ip:8.8.8.8" {
		t.Errorf("X-Real-IP fallback; got %q", got)
	}
}
