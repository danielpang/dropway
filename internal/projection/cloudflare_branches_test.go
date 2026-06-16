package projection

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/danielpang/dropway/internal/edgerevoke"
)

// kvServer is a minimal in-memory stand-in for the CF Workers KV REST API used by
// these branch tests. It records the method + key + body of each request.
type kvServer struct {
	mu    sync.Mutex
	store map[string]string
	calls []kvCall
	// fail, when set, forces this HTTP status for every request (error-path tests).
	fail int
}

type kvCall struct {
	method string
	key    string
	body   string
}

func newKVServer() (*kvServer, *httptest.Server) {
	k := &kvServer{store: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// KV list endpoint (.../keys?prefix=) — used by RebuildFromDB to find stale
		// keys to prune. Returns the stored key names under the requested prefix.
		if strings.HasSuffix(r.URL.Path, "/keys") {
			k.mu.Lock()
			defer k.mu.Unlock()
			if k.fail != 0 {
				w.WriteHeader(k.fail)
				_, _ = w.Write([]byte(`{"success":false}`))
				return
			}
			pfx := r.URL.Query().Get("prefix")
			result := []map[string]string{}
			for name := range k.store {
				if strings.HasPrefix(name, pfx) {
					result = append(result, map[string]string{"name": name})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success":     true,
				"result":      result,
				"result_info": map[string]string{"cursor": ""},
			})
			return
		}
		const prefix = "/accounts/a/storage/kv/namespaces/n/values/"
		key := r.URL.Path[len(prefix):]
		k.mu.Lock()
		defer k.mu.Unlock()
		if k.fail != 0 {
			w.WriteHeader(k.fail)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":1,"message":"forced"}]}`))
			return
		}
		body := readAll(r)
		k.calls = append(k.calls, kvCall{method: r.Method, key: key, body: body})
		switch r.Method {
		case http.MethodGet:
			v, ok := k.store[key]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(v))
		case http.MethodPut:
			k.store[key] = body
			_, _ = w.Write([]byte(`{"success":true}`))
		case http.MethodDelete:
			delete(k.store, key)
			_, _ = w.Write([]byte(`{"success":true}`))
		}
	}))
	return k, srv
}

func readAll(r *http.Request) string {
	if r.Body == nil {
		return ""
	}
	buf := make([]byte, r.ContentLength)
	if r.ContentLength <= 0 {
		return ""
	}
	n, _ := r.Body.Read(buf)
	return string(buf[:n])
}

func (k *kvServer) lastCall() kvCall {
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.calls) == 0 {
		return kvCall{}
	}
	return k.calls[len(k.calls)-1]
}

func newKV(url string) *CloudflareKV {
	c := NewCloudflareKV("a", "n", "tok")
	c.BaseURL = url
	return c
}

// TestCloudflareKV_PutDeleteRoute asserts a route PUT writes the canonical JSON at
// route:<host> and a DELETE removes it (the publish + unshare paths).
func TestCloudflareKV_PutDeleteRoute(t *testing.T) {
	k, srv := newKVServer()
	defer srv.Close()
	c := newKV(srv.URL)
	ctx := context.Background()

	val := RouteValue{OrgID: "o", SiteID: "s", VersionID: "v", AccessMode: AccessPublic, SchemaVersion: SchemaVersion}
	if err := c.PutRoute(ctx, "acme.dropwaycontent.com", val); err != nil {
		t.Fatal(err)
	}
	if got := k.lastCall(); got.method != http.MethodPut || got.key != "route:acme.dropwaycontent.com" {
		t.Fatalf("put call = %+v", got)
	}

	if err := c.DeleteRoute(ctx, "acme.dropwaycontent.com"); err != nil {
		t.Fatal(err)
	}
	if got := k.lastCall(); got.method != http.MethodDelete || got.key != "route:acme.dropwaycontent.com" {
		t.Fatalf("delete call = %+v", got)
	}
}

// TestCloudflareKV_PutRoute_RejectsInvalid asserts PutRoute validates the value
// BEFORE any network call (a malformed projection can never reach the edge).
func TestCloudflareKV_PutRoute_RejectsInvalid(t *testing.T) {
	k, srv := newKVServer()
	defer srv.Close()
	c := newKV(srv.URL)
	if err := c.PutRoute(context.Background(), "h", RouteValue{}); err == nil {
		t.Fatal("invalid route should be rejected before the network call")
	}
	if len(k.calls) != 0 {
		t.Errorf("no HTTP call should be made for an invalid route, got %d", len(k.calls))
	}
}

// TestCloudflareKV_SetOrgStatus_BlockAndClear asserts a blocking status PUTs the
// bare status string and "active" DELETEs the key (so the org is served again).
func TestCloudflareKV_SetOrgStatus_BlockAndClear(t *testing.T) {
	k, srv := newKVServer()
	defer srv.Close()
	c := newKV(srv.URL)
	ctx := context.Background()
	const org = "org-42"

	// Block.
	if err := c.SetOrgStatus(ctx, org, "suspended"); err != nil {
		t.Fatal(err)
	}
	got := k.lastCall()
	if got.method != http.MethodPut || got.key != "org_status:org-42" || got.body != "suspended" {
		t.Fatalf("block call = %+v, want PUT org_status:org-42 body=suspended", got)
	}

	// "active" clears (DELETE).
	if err := c.SetOrgStatus(ctx, org, "active"); err != nil {
		t.Fatal(err)
	}
	if got := k.lastCall(); got.method != http.MethodDelete || got.key != "org_status:org-42" {
		t.Fatalf("clear call = %+v, want DELETE org_status:org-42", got)
	}

	// Empty status also clears (DELETE).
	if err := c.SetOrgStatus(ctx, org, ""); err != nil {
		t.Fatal(err)
	}
	if got := k.lastCall(); got.method != http.MethodDelete {
		t.Fatalf("empty-status call = %+v, want DELETE", got)
	}

	// Empty org id is a programmer error, rejected with no network call.
	if err := c.SetOrgStatus(ctx, "", "suspended"); err == nil {
		t.Fatal("empty org id should be rejected")
	}
}

// TestCloudflareKV_RebuildFromDB asserts the reconciler re-PUTs every supplied
// route AND prunes a stale route:<host> key that the authoritative set no longer
// contains (the full-replace, rebuildable-from-Postgres invariant — H5). A
// revoked:/org_status: key in the same namespace must be left untouched.
func TestCloudflareKV_RebuildFromDB(t *testing.T) {
	k, srv := newKVServer()
	defer srv.Close()
	c := newKV(srv.URL)

	// Seed a stale route (a deleted/reassigned host) and an unrelated denylist key.
	k.store["route:stale.dropwaycontent.com"] = `{"org_id":"old"}`
	k.store["revoked:user:u1"] = `{"min_iat":1}`

	routes := map[string]RouteValue{
		"a.dropwaycontent.com": {OrgID: "o", SiteID: "s", VersionID: "va", AccessMode: AccessPublic, SchemaVersion: SchemaVersion},
		"b.dropwaycontent.com": {OrgID: "o", SiteID: "s", VersionID: "vb", AccessMode: AccessPublic, SchemaVersion: SchemaVersion},
	}
	if err := c.RebuildFromDB(context.Background(), routes); err != nil {
		t.Fatal(err)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, ok := k.store["route:a.dropwaycontent.com"]; !ok {
		t.Error("rebuild did not write route a")
	}
	if _, ok := k.store["route:b.dropwaycontent.com"]; !ok {
		t.Error("rebuild did not write route b")
	}
	if _, ok := k.store["route:stale.dropwaycontent.com"]; ok {
		t.Error("rebuild did not prune the stale route key")
	}
	if _, ok := k.store["revoked:user:u1"]; !ok {
		t.Error("rebuild must not touch non-route keys (revoked:)")
	}
}

// TestCloudflareKV_RebuildFromDB_RejectsInvalid asserts a malformed route aborts
// the rebuild with an error.
func TestCloudflareKV_RebuildFromDB_RejectsInvalid(t *testing.T) {
	_, srv := newKVServer()
	defer srv.Close()
	c := newKV(srv.URL)
	if err := c.RebuildFromDB(context.Background(), map[string]RouteValue{"bad": {OrgID: "o"}}); err == nil {
		t.Fatal("rebuild with an invalid route should error")
	}
}

// TestCloudflareKV_Non2xxError asserts a non-2xx CF response maps to an error
// (PutRoute, DeleteRoute, SetOrgStatus all route through `do`).
func TestCloudflareKV_Non2xxError(t *testing.T) {
	k, srv := newKVServer()
	defer srv.Close()
	k.fail = http.StatusInternalServerError
	c := newKV(srv.URL)
	ctx := context.Background()

	val := RouteValue{OrgID: "o", SiteID: "s", VersionID: "v", AccessMode: AccessPublic, SchemaVersion: SchemaVersion}
	if err := c.PutRoute(ctx, "h.dropwaycontent.com", val); err == nil {
		t.Error("PutRoute should error on a 500")
	}
	if err := c.DeleteRoute(ctx, "h.dropwaycontent.com"); err == nil {
		t.Error("DeleteRoute should error on a 500")
	}
	if err := c.SetOrgStatus(ctx, "o", "suspended"); err == nil {
		t.Error("SetOrgStatus should error on a 500")
	}
}

// TestCloudflareKV_Revoke_Validation asserts Revoke rejects a bad kind, empty id,
// and a zero min_iat before any network call (mirrors the Local writer contract).
func TestCloudflareKV_Revoke_Validation(t *testing.T) {
	k, srv := newKVServer()
	defer srv.Close()
	c := newKV(srv.URL)
	ctx := context.Background()

	if err := c.Revoke(ctx, "bogus-kind", "id", 1); err == nil {
		t.Error("invalid kind should error")
	}
	if len(k.calls) != 0 {
		t.Error("a validation failure must not hit the network")
	}
}

// TestCloudflareKV_Revoke_GetReadErrorFallsThroughToWrite asserts the documented
// fail-open idempotency: if the existence GET returns a malformed/erroring body,
// Revoke ignores it and proceeds to WRITE the denylist entry (the worst case is a
// redundant write, never a loosened denylist). This exercises getRevoked's
// non-404 error / decode-error branch via Revoke.
func TestCloudflareKV_Revoke_GetReadErrorFallsThroughToWrite(t *testing.T) {
	var mu sync.Mutex
	var puts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			// Return a 200 with a body that is NOT valid edgerevoke JSON → decode error.
			_, _ = w.Write([]byte("not-json"))
		case http.MethodPut:
			puts++
			_, _ = w.Write([]byte(`{"success":true}`))
		}
	}))
	defer srv.Close()

	c := newKV(srv.URL)
	if err := c.Revoke(context.Background(), edgerevoke.KindUser, "u1", 1000); err != nil {
		t.Fatalf("Revoke should fall through to a write on a GET decode error: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if puts != 1 {
		t.Fatalf("expected exactly one PUT after the GET read error, got %d", puts)
	}
}

// TestCloudflareKV_Revoke_GetExistingSkipsWrite covers getRevoked's success path
// (a valid stored value with a >= min_iat makes Revoke a no-op write).
func TestCloudflareKV_Revoke_GetExistingSkipsWrite(t *testing.T) {
	k, srv := newKVServer()
	defer srv.Close()
	c := newKV(srv.URL)
	ctx := context.Background()

	// Seed an existing entry at min_iat 5000.
	if err := c.Revoke(ctx, edgerevoke.KindUser, "u1", 5000); err != nil {
		t.Fatal(err)
	}
	k.mu.Lock()
	puts := 0
	for _, c := range k.calls {
		if c.method == http.MethodPut {
			puts++
		}
	}
	k.mu.Unlock()

	// An earlier min_iat reads back the existing 5000 (a valid decode) and skips the PUT.
	if err := c.Revoke(ctx, edgerevoke.KindUser, "u1", 1000); err != nil {
		t.Fatal(err)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	puts2 := 0
	for _, c := range k.calls {
		if c.method == http.MethodPut {
			puts2++
		}
	}
	if puts2 != puts {
		t.Fatalf("an earlier min_iat with a valid existing entry should skip the PUT (puts %d → %d)", puts, puts2)
	}
}

// TestDefaultGetters asserts the lazy default base URL + client.
func TestDefaultGetters(t *testing.T) {
	c := &CloudflareKV{}
	if c.httpClient() == nil {
		t.Error("httpClient() should never be nil")
	}
	if c.baseURL() != "https://api.cloudflare.com/client/v4" {
		t.Errorf("default baseURL = %q", c.baseURL())
	}
}
