// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package manifest

import (
	"encoding/json"
	"strings"
	"testing"
)

const goodSHA = "0000000000000000000000000000000000000000000000000000000000000000"

func buildManifest(t *testing.T, files map[string]map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"schema_version": 1, "files": files})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func TestParse_Valid(t *testing.T) {
	raw := buildManifest(t, map[string]map[string]any{
		"index.html": {"sha256": goodSHA, "content_type": "text/html", "size": 12},
	})
	m, ok := Parse(raw)
	if !ok {
		t.Fatalf("valid manifest rejected")
	}
	e := m.Files["index.html"]
	if e.SHA256 != goodSHA || e.ContentType != "text/html" || !e.HasSize || e.Size != 12 {
		t.Errorf("entry = %+v", e)
	}
}

func TestParse_FailsClosed(t *testing.T) {
	cases := map[string][]byte{
		"schema_version 2": buildManifestVersion(t, 2),
		"missing sha256": buildManifest(t, map[string]map[string]any{
			"index.html": {"content_type": "text/html"},
		}),
		"bad sha256": buildManifest(t, map[string]map[string]any{
			"index.html": {"sha256": "tooshort", "content_type": "text/html"},
		}),
		"empty content_type": buildManifest(t, map[string]map[string]any{
			"index.html": {"sha256": goodSHA, "content_type": ""},
		}),
		"missing content_type": buildManifest(t, map[string]map[string]any{
			"index.html": {"sha256": goodSHA},
		}),
		"not json": []byte("{not json"),
	}
	for name, raw := range cases {
		if _, ok := Parse(raw); ok {
			t.Errorf("%s: expected fail closed", name)
		}
	}
}

func buildManifestVersion(t *testing.T, v int) []byte {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{
		"schema_version": v,
		"files":          map[string]any{"index.html": map[string]any{"sha256": goodSHA, "content_type": "text/html"}},
	})
	return raw
}

func TestResolve_Candidates(t *testing.T) {
	m, ok := Parse(buildManifest(t, map[string]map[string]any{
		"index.html":      {"sha256": goodSHA, "content_type": "text/html"},
		"blog/index.html": {"sha256": goodSHA, "content_type": "text/html"},
		"about.html":      {"sha256": goodSHA, "content_type": "text/html"},
		"docs/index.html": {"sha256": goodSHA, "content_type": "text/html"},
		"style.css":       {"sha256": goodSHA, "content_type": "text/css"},
	}))
	if !ok {
		t.Fatal("parse failed")
	}

	cases := map[string]string{
		"":          "index.html",      // root → index.html
		"blog/":     "blog/index.html", // trailing slash → dir index
		"about":     "about.html",      // pretty URL → .html
		"docs":      "docs/index.html", // pretty URL → dir index (tried before .html)
		"style.css": "style.css",       // exact, extension → no fallback
	}
	for in, want := range cases {
		match, ok := m.Resolve(in)
		if !ok || match.Path != want {
			t.Errorf("Resolve(%q) = (%q,%v), want %q", in, match.Path, ok, want)
		}
	}

	// An extension path with no exact match must NOT fall back.
	if _, ok := m.Resolve("missing.css"); ok {
		t.Errorf("extension path should not fall back")
	}
	// A directory request only tries index.html (no .html fallback).
	if _, ok := m.Resolve("nodir/"); ok {
		t.Errorf("directory request should only try index.html")
	}
}

func TestNotFoundEntry(t *testing.T) {
	m, _ := Parse(buildManifest(t, map[string]map[string]any{
		"404.html": {"sha256": goodSHA, "content_type": "text/html"},
	}))
	if _, ok := m.NotFoundEntry(); !ok {
		t.Errorf("expected a custom 404 entry")
	}

	m2, _ := Parse(buildManifest(t, map[string]map[string]any{
		"index.html": {"sha256": goodSHA, "content_type": "text/html"},
	}))
	if _, ok := m2.NotFoundEntry(); ok {
		t.Errorf("did not expect a custom 404 entry")
	}
}

func TestParse_PreservesAllPaths(t *testing.T) {
	raw := buildManifest(t, map[string]map[string]any{
		"a/b/c.js": {"sha256": goodSHA, "content_type": "text/javascript"},
	})
	m, ok := Parse(raw)
	if !ok {
		t.Fatal("parse failed")
	}
	if _, ok := m.Files["a/b/c.js"]; !ok {
		t.Errorf("nested path lost; files=%v", keysOf(m.Files))
	}
}

func keysOf(m map[string]Entry) string {
	var b strings.Builder
	for k := range m {
		b.WriteString(k)
		b.WriteByte(' ')
	}
	return b.String()
}
