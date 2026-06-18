// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package route

import (
	"testing"
	"time"
)

func TestNormalizeHost(t *testing.T) {
	cases := map[string]string{
		"ACME.dropwaycontent.com":      "acme.dropwaycontent.com",
		"  acme.dropwaycontent.com  ":  "acme.dropwaycontent.com",
		"acme.dropwaycontent.com:8443": "acme.dropwaycontent.com",
		"acme.dropwaycontent.com.":     "acme.dropwaycontent.com",
		"ACME.dropwaycontent.com:443.": "acme.dropwaycontent.com", // port stripped, no trailing dot left
		"Acme.DropwayContent.Com":      "acme.dropwaycontent.com",
	}
	for in, want := range cases {
		if got := NormalizeHost(in); got != want {
			t.Errorf("NormalizeHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanPath(t *testing.T) {
	type tc struct {
		in   string
		want string
		ok   bool
	}
	cases := []tc{
		{"/", "", true},
		{"/index.html", "index.html", true},
		{"/blog/", "blog/", true},
		{"/a/./b", "a/b", true},
		{"//a//b", "a/b", true},
		{"/about", "about", true},
		{"/foo%20bar", "foo bar", true},
		{"/path?x=1#frag", "path", true},
		{"/%2e%2e/secret", "", false}, // encoded ..
		{"/../etc", "", false},
		{"/a/../../b", "", false},
		{"/foo%00bar", "", false}, // NUL
		{"/foo\\bar", "", false},  // raw backslash
		{"/foo%5cbar", "", false}, // encoded backslash
		{"/bad%zz", "", false},    // malformed encoding
		{"/trailing%", "", false}, // truncated escape
	}
	for _, c := range cases {
		got, ok := CleanPath(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("CleanPath(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestSafeNextPath(t *testing.T) {
	cases := map[string]string{
		"/dashboard":       "/dashboard",
		"/a?b=1":           "/a?b=1",
		"":                 "/",
		"//evil.com":       "/",
		"/\\evil":          "/",
		"https://evil.com": "/",
		"/%2f%2fok":        "//ok", // decoded once; starts with single slash → wait, "//" → "/"
	}
	// Re-derive the tricky one: "/%2f%2fok" decodes to "//ok" which starts with //
	// → must collapse to "/".
	cases["/%2f%2fok"] = "/"
	// A decoded space is 0x20, and the Worker's check is `code <= 0x20`, so a space
	// in `next` is rejected → "/". (This matches authz.ts safeNextPath exactly.)
	cases["/with%20space"] = "/"
	cases["/bad%0a"] = "/" // decoded LF (0x0a) is a control char
	for in, want := range cases {
		if got := SafeNextPath(in); got != want {
			t.Errorf("SafeNextPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseRouteValue(t *testing.T) {
	valid := `{"org_id":"11111111-1111-1111-1111-111111111111","site_id":"22222222-2222-2222-2222-222222222222","version_id":"44444444-4444-4444-4444-444444444444","access_mode":"public","schema_version":2}`
	rv, ok := ParseRouteValue([]byte(valid))
	if !ok {
		t.Fatalf("valid route value rejected")
	}
	if rv.AccessMode != "public" || rv.SchemaVersion != 2 {
		t.Errorf("parsed wrong: %+v", rv)
	}

	bad := []string{
		`{}`,
		`[]`,
		`null`,
		`{"org_id":"not-a-uuid","site_id":"22222222-2222-2222-2222-222222222222","version_id":"44444444-4444-4444-4444-444444444444","access_mode":"public","schema_version":2}`,
		`{"org_id":"11111111-1111-1111-1111-111111111111","site_id":"22222222-2222-2222-2222-222222222222","version_id":"44444444-4444-4444-4444-444444444444","access_mode":"bogus","schema_version":2}`,
		// schema_version 3 (newer than supported)
		`{"org_id":"11111111-1111-1111-1111-111111111111","site_id":"22222222-2222-2222-2222-222222222222","version_id":"44444444-4444-4444-4444-444444444444","access_mode":"public","schema_version":3}`,
		// unknown extra field (drift tripwire)
		`{"org_id":"11111111-1111-1111-1111-111111111111","site_id":"22222222-2222-2222-2222-222222222222","version_id":"44444444-4444-4444-4444-444444444444","access_mode":"public","schema_version":2,"extra":1}`,
		// non-integer schema_version
		`{"org_id":"11111111-1111-1111-1111-111111111111","site_id":"22222222-2222-2222-2222-222222222222","version_id":"44444444-4444-4444-4444-444444444444","access_mode":"public","schema_version":1.5}`,
		// bad expires_at
		`{"org_id":"11111111-1111-1111-1111-111111111111","site_id":"22222222-2222-2222-2222-222222222222","version_id":"44444444-4444-4444-4444-444444444444","access_mode":"public","schema_version":2,"expires_at":"not-a-date"}`,
	}
	for _, b := range bad {
		if _, ok := ParseRouteValue([]byte(b)); ok {
			t.Errorf("expected reject for %s", b)
		}
	}

	// v1 (no expires_at) is accepted as non-expiring.
	v1 := `{"org_id":"11111111-1111-1111-1111-111111111111","site_id":"22222222-2222-2222-2222-222222222222","version_id":"44444444-4444-4444-4444-444444444444","access_mode":"public","schema_version":1}`
	if _, ok := ParseRouteValue([]byte(v1)); !ok {
		t.Errorf("v1 route value should be accepted")
	}
}

func TestIsRouteExpired(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	never := RouteValue{}
	if IsRouteExpired(never, now) {
		t.Errorf("route with no expires_at should never expire")
	}

	past := RouteValue{ExpiresAt: now.Add(-time.Hour).Format(time.RFC3339)}
	if !IsRouteExpired(past, now) {
		t.Errorf("past expires_at should be expired")
	}

	future := RouteValue{ExpiresAt: now.Add(time.Hour).Format(time.RFC3339)}
	if IsRouteExpired(future, now) {
		t.Errorf("future expires_at should not be expired")
	}

	// Inclusive boundary: now == exp is expired.
	exact := RouteValue{ExpiresAt: now.Format(time.RFC3339)}
	if !IsRouteExpired(exact, now) {
		t.Errorf("now==exp should be expired (inclusive boundary)")
	}

	// Malformed expires_at fails closed (expired). ParseRouteValue would reject this,
	// but IsRouteExpired must independently fail closed if one slips through.
	malformed := RouteValue{ExpiresAt: "garbage"}
	if !IsRouteExpired(malformed, now) {
		t.Errorf("malformed expires_at should fail closed (expired)")
	}
}
