// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package contenttype

import (
	"strings"
	"testing"
)

func TestForPath(t *testing.T) {
	cases := map[string]string{
		"index.html":       "text/html",
		"page.htm":         "text/html",
		"assets/app.js":    "javascript",
		"mod.mjs":          "javascript",
		"data.json":        "json",
		"styles/site.css":  "text/css",
		"icon.svg":         "image/svg+xml",
		"app.wasm":         "application/wasm",
		"site.webmanifest": "application/manifest+json",
		"logo.png":         "image/png",
		"x.unknown":        "application/octet-stream",
		"noext":            "application/octet-stream",
		"UPPER.HTML":       "text/html", // extension match is case-insensitive
	}
	for path, want := range cases {
		if got := ForPath(path); !strings.Contains(got, want) {
			t.Errorf("ForPath(%q) = %q, want substring %q", path, got, want)
		}
	}
}

// JS/JSON/HTML/CSS are pinned (not left to the host mime table) so the result is
// deterministic across machines and carries the charset we want.
func TestForPath_PinnedDeterministic(t *testing.T) {
	if got := ForPath("a.js"); got != "text/javascript; charset=utf-8" {
		t.Errorf("js = %q", got)
	}
	if got := ForPath("a.json"); got != "application/json" {
		t.Errorf("json = %q", got)
	}
	if got := ForPath("a.css"); got != "text/css; charset=utf-8" {
		t.Errorf("css = %q", got)
	}
}
