package projection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func countKVMethod(k *kvServer, method string) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	n := 0
	for _, c := range k.calls {
		if c.method == method {
			n++
		}
	}
	return n
}

func testRoute(version string) RouteValue {
	return RouteValue{
		OrgID: "o", SiteID: "s", VersionID: version,
		AccessMode: AccessPublic, SchemaVersion: SchemaVersion, PlanTier: "free",
	}
}

// TestCloudflareKV_PutRoute_SkipsWriteWhenUnchanged is the quota regression.
// The candidate-projection repair re-PUTs every host every five minutes, and a
// republish of an unchanged route does too. Workers KV's free plan allows
// 1,000 writes per day (HTTP 429, error 10048). An identical value must not PUT.
func TestCloudflareKV_PutRoute_SkipsWriteWhenUnchanged(t *testing.T) {
	k, srv := newKVServer()
	defer srv.Close()
	c := newKV(srv.URL)

	val := testRoute("v")
	// Different key order and whitespace than json.Marshal. A raw byte compare
	// would treat this as a change and spend a write.
	k.store[RouteKey("acme.dropwaycontent.com")] = fmt.Sprintf(`{
		"schema_version": %d,
		"access_mode": "public",
		"version_id": "v",
		"site_id": "s",
		"org_id": "o",
		"plan_tier": "free"
	}`, SchemaVersion)

	if err := c.PutRoute(context.Background(), "acme.dropwaycontent.com", val); err != nil {
		t.Fatal(err)
	}
	if n := countKVMethod(k, http.MethodPut); n != 0 {
		t.Fatalf("unchanged route issued %d PUTs; want 0", n)
	}
	if n := countKVMethod(k, http.MethodGet); n != 1 {
		t.Fatalf("unchanged route GETs = %d, want 1", n)
	}
}

// TestCloudflareKV_PutRoute_WritesWhenValueChanges guards the repair path: a
// stale version (or any other field) must still be written.
func TestCloudflareKV_PutRoute_WritesWhenValueChanges(t *testing.T) {
	k, srv := newKVServer()
	defer srv.Close()
	c := newKV(srv.URL)

	stale, err := json.Marshal(testRoute("old"))
	if err != nil {
		t.Fatal(err)
	}
	k.store[RouteKey("acme.dropwaycontent.com")] = string(stale)

	if err := c.PutRoute(context.Background(), "acme.dropwaycontent.com", testRoute("new")); err != nil {
		t.Fatal(err)
	}
	if n := countKVMethod(k, http.MethodPut); n != 1 {
		t.Fatalf("changed route PUTs = %d, want 1", n)
	}
	got := k.store[RouteKey("acme.dropwaycontent.com")]
	var stored RouteValue
	if err := json.Unmarshal([]byte(got), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.VersionID != "new" {
		t.Fatalf("stored version = %q, want new", stored.VersionID)
	}
}

// quotaScript is a one-off KV stand-in whose GET and PUT statuses differ, so a
// quota failure on the write can be distinguished from the preceding read.
type quotaScript struct {
	mu    sync.Mutex
	gets  int
	puts  int
	getSt int
	putSt int
	body  string
}

func (q *quotaScript) handler(w http.ResponseWriter, r *http.Request) {
	q.mu.Lock()
	defer q.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		q.gets++
		if q.getSt != 0 {
			w.WriteHeader(q.getSt)
			_, _ = io.WriteString(w, q.body)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	case http.MethodPut:
		q.puts++
		if q.putSt != 0 {
			w.WriteHeader(q.putSt)
			_, _ = io.WriteString(w, q.body)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"success":true}`)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

const quotaBody = `{"result":null,"errors":[{"code":10048,"message":"your account has reached the free usage limit for this operation for today"}],"success":false}`

func TestCloudflareKV_PutRoute_DailyWriteQuota(t *testing.T) {
	q := &quotaScript{getSt: http.StatusNotFound, putSt: http.StatusTooManyRequests, body: quotaBody}
	srv := httptest.NewServer(http.HandlerFunc(q.handler))
	defer srv.Close()
	c := newKV(srv.URL)

	err := c.PutRoute(context.Background(), "temporary--wen-wen.dropwaycontent.com", testRoute("v"))
	if !errors.Is(err, ErrDailyQuota) {
		t.Fatalf("err = %v, want ErrDailyQuota", err)
	}
	if q.puts != 1 {
		t.Fatalf("PUTs = %d, want 1", q.puts)
	}
}

func TestCloudflareKV_PutRoute_ReadQuotaDoesNotWrite(t *testing.T) {
	q := &quotaScript{getSt: http.StatusTooManyRequests, putSt: http.StatusOK, body: quotaBody}
	srv := httptest.NewServer(http.HandlerFunc(q.handler))
	defer srv.Close()
	c := newKV(srv.URL)

	err := c.PutRoute(context.Background(), "h.dropwaycontent.com", testRoute("v"))
	if !errors.Is(err, ErrDailyQuota) {
		t.Fatalf("err = %v, want ErrDailyQuota", err)
	}
	if q.puts != 0 {
		t.Fatalf("a quota-limited read must not fall through to a write; PUTs = %d", q.puts)
	}
}

func TestCloudflareKV_PutRoute_Generic429IsNotDailyQuota(t *testing.T) {
	q := &quotaScript{
		getSt: http.StatusNotFound,
		putSt: http.StatusTooManyRequests,
		body:  `{"success":false,"errors":[{"code":971,"message":"rate limited"}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(q.handler))
	defer srv.Close()
	c := newKV(srv.URL)

	err := c.PutRoute(context.Background(), "h.dropwaycontent.com", testRoute("v"))
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrDailyQuota) {
		t.Fatalf("a non-10048 429 must stay a normal error, got %v", err)
	}
}

func TestCloudflareKV_PutRoute_ReadErrorStillWrites(t *testing.T) {
	q := &quotaScript{getSt: http.StatusInternalServerError, putSt: 0, body: `{"success":false}`}
	srv := httptest.NewServer(http.HandlerFunc(q.handler))
	defer srv.Close()
	c := newKV(srv.URL)

	if err := c.PutRoute(context.Background(), "h.dropwaycontent.com", testRoute("v")); err != nil {
		t.Fatal(err)
	}
	if q.puts != 1 {
		t.Fatalf("PUTs = %d, want 1 after a transient read failure", q.puts)
	}
}

func TestQuotaRetryAfter_UntilNextUTCMidnight(t *testing.T) {
	now := time.Date(2026, 11, 15, 15, 42, 48, 0, time.UTC)
	wait := QuotaRetryAfter(now)
	want := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC).Sub(now)
	if wait != want {
		t.Fatalf("wait = %s, want %s", wait, want)
	}

	late := time.Date(2026, 11, 15, 23, 59, 30, 0, time.UTC)
	if got := QuotaRetryAfter(late); got < time.Minute {
		t.Fatalf("wait at end of day = %s, want at least 1m", got)
	}
}
