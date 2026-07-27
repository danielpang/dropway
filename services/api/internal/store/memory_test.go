// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"strings"
	"testing"
)

func TestNormalizeMemoryContent(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  Brand palette is  navy #0A2540. ", "brand palette is navy #0a2540."},
		{"One\n\ttwo   three", "one two three"},
		{"", ""},
	}
	for _, c := range cases {
		if got := NormalizeMemoryContent(c.in); got != c.want {
			t.Errorf("NormalizeMemoryContent(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMemoryContentHashStableAcrossWhitespaceAndCase(t *testing.T) {
	a := MemoryContentHash("Always end pages with a Book a Demo CTA")
	b := MemoryContentHash("  always   end pages with a book a demo CTA ")
	if a != b {
		t.Errorf("hash not stable across normalization: %s vs %s", a, b)
	}
	if c := MemoryContentHash("something else entirely"); c == a {
		t.Error("distinct content produced identical hashes")
	}
}

func TestVectorText(t *testing.T) {
	if got := VectorText(nil); got != "" {
		t.Errorf("VectorText(nil) = %q, want empty", got)
	}
	got := VectorText([]float32{0.5, -1, 0.25})
	if got != "[0.5,-1,0.25]" {
		t.Errorf("VectorText = %q, want [0.5,-1,0.25]", got)
	}
	if strings.ContainsAny(got, " \n") {
		t.Errorf("VectorText output contains whitespace: %q", got)
	}
}

// The ANN queries must always carry the org filter in SQL (RLS is the
// backstop, not the only guard), and content-chunk search must pin site
// chunks to each site's current version.
func TestMemorySearchQueriesOrgScopedInSQL(t *testing.T) {
	for _, c := range []struct{ constName, want string }{
		{"searchOrgMemories", "org_id = $1"},
		{"searchContentChunks", "c.org_id = $1"},
		{"searchContentChunks", "s.current_version_id = c.version_id"},
		{"listPinnedOrgMemories", "org_id = $1"},
	} {
		sql := generatedQuerySQL(t, c.constName)
		if !strings.Contains(sql, c.want) {
			t.Errorf("%s: generated SQL missing %q", c.constName, c.want)
		}
	}
}
