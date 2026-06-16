// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package projection

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/danielpang/dropway/internal/edgerevoke"
)

// TestLocal_Revoke_Idempotent asserts the Local denylist writer records an entry,
// is idempotent under max(min_iat) (a later write wins; an earlier write is a
// no-op), and validates kind/id/value.
func TestLocal_Revoke_Idempotent(t *testing.T) {
	l := NewLocal()
	ctx := context.Background()

	if _, ok := l.GetRevoked(edgerevoke.KindSite, "s1"); ok {
		t.Fatal("fresh writer should have no revocation")
	}

	if err := l.Revoke(ctx, edgerevoke.KindSite, "s1", 1000); err != nil {
		t.Fatal(err)
	}
	v, ok := l.GetRevoked(edgerevoke.KindSite, "s1")
	if !ok || v.MinIAT != 1000 {
		t.Fatalf("after revoke: %+v ok=%v", v, ok)
	}

	// A LATER min_iat tightens (wins).
	if err := l.Revoke(ctx, edgerevoke.KindSite, "s1", 2000); err != nil {
		t.Fatal(err)
	}
	if v, _ := l.GetRevoked(edgerevoke.KindSite, "s1"); v.MinIAT != 2000 {
		t.Fatalf("later write should win: %+v", v)
	}

	// An EARLIER min_iat is a no-op (the denylist only ever tightens).
	if err := l.Revoke(ctx, edgerevoke.KindSite, "s1", 1500); err != nil {
		t.Fatal(err)
	}
	if v, _ := l.GetRevoked(edgerevoke.KindSite, "s1"); v.MinIAT != 2000 {
		t.Fatalf("earlier write must not loosen: %+v", v)
	}

	// Distinct kinds/ids are independent keys.
	if err := l.Revoke(ctx, edgerevoke.KindUser, "s1", 500); err != nil {
		t.Fatal(err)
	}
	if v, _ := l.GetRevoked(edgerevoke.KindUser, "s1"); v.MinIAT != 500 {
		t.Fatalf("user key should be independent of site key: %+v", v)
	}

	// Validation.
	if err := l.Revoke(ctx, edgerevoke.Kind("bogus"), "x", 1); err == nil {
		t.Error("invalid kind should error")
	}
	if err := l.Revoke(ctx, edgerevoke.KindOrg, "", 1); err == nil {
		t.Error("empty id should error")
	}
	if err := l.Revoke(ctx, edgerevoke.KindOrg, "o1", 0); err == nil {
		t.Error("zero min_iat should error")
	}
}

// TestCloudflareKV_Revoke verifies the KV writer GETs the existing denylist value
// for max-idempotency, then PUTs the contract JSON at the revoked:<kind>:<id> key.
func TestCloudflareKV_Revoke(t *testing.T) {
	var mu sync.Mutex
	store := map[string][]byte{}
	var lastPut string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		// Key is the last path segment (…/values/<key>).
		key := r.URL.Path[len("/accounts/a/storage/kv/namespaces/n/values/"):]
		switch r.Method {
		case http.MethodGet:
			b, ok := store[key]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write(b)
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			store[key] = b
			lastPut = key
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"success":true}`))
		}
	}))
	defer srv.Close()

	c := NewCloudflareKV("a", "n", "tok")
	c.BaseURL = srv.URL
	ctx := context.Background()

	if err := c.Revoke(ctx, edgerevoke.KindUser, "u1", 1000); err != nil {
		t.Fatal(err)
	}
	if lastPut != "revoked:user:u1" {
		t.Fatalf("PUT key = %q, want revoked:user:u1", lastPut)
	}
	var got edgerevoke.Value
	_ = json.Unmarshal(store["revoked:user:u1"], &got)
	if got.MinIAT != 1000 {
		t.Fatalf("stored value = %+v", got)
	}

	// Idempotent: a write with an EARLIER min_iat must NOT overwrite (max wins). The
	// writer reads the existing 1000 and skips the PUT.
	lastPut = ""
	if err := c.Revoke(ctx, edgerevoke.KindUser, "u1", 500); err != nil {
		t.Fatal(err)
	}
	if lastPut != "" {
		t.Fatalf("earlier min_iat should be a no-op, but PUT %q", lastPut)
	}
	_ = json.Unmarshal(store["revoked:user:u1"], &got)
	if got.MinIAT != 1000 {
		t.Fatalf("value loosened to %+v", got)
	}

	// A LATER min_iat tightens (writes).
	if err := c.Revoke(ctx, edgerevoke.KindUser, "u1", 2000); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(store["revoked:user:u1"], &got)
	if got.MinIAT != 2000 {
		t.Fatalf("later write should win, got %+v", got)
	}
}
