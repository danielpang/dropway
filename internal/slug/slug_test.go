// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package slug

import (
	"strings"
	"testing"
)

func TestValid(t *testing.T) {
	valid := []string{
		"a", "acme", "my-site", "docs-internal", "blog2", "a1", "1a",
		"a-b-c", strings.Repeat("a", 63),
	}
	for _, s := range valid {
		if !Valid(s) {
			t.Errorf("Valid(%q) = false, want true", s)
		}
	}

	invalid := []string{
		"",                      // empty
		"-acme",                 // leading hyphen
		"acme-",                 // trailing hyphen
		"Acme",                  // uppercase
		"ac me",                 // space
		"a/b",                   // path separator → KV-key path injection
		"a.b",                   // dot → extra DNS label
		"a%2e",                  // percent → KV-key escaping
		"a#x",                   // fragment
		"a?x",                   // query
		"victimorg--victimsite", // `--` reserved: legacy hosts redirect on it
		"a--b",                  // any `--` run (legacy-redirect invariant)
		strings.Repeat("a", 64), // too long (max DNS label is 63)
		"a\tb",                  // control char
	}
	for _, s := range invalid {
		if Valid(s) {
			t.Errorf("Valid(%q) = true, want false", s)
		}
	}
}

// Slugify must normalize loose input into something Valid accepts (or "").
func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"My Blog":          "my-blog",
		"My_Site":          "my-site",
		"my.docs":          "my-docs",
		"UPPER":            "upper",
		"  spaced  ":       "spaced",
		"a--b":             "a-b", // collapse the doubled hyphen (reserved for legacy hosts)
		"--lead-trail--":   "lead-trail",
		"weird!!!chars$$$": "weird-chars",
		"café":             "caf", // non-ASCII dropped, trailing hyphen trimmed
		"already-valid":    "already-valid",
		"blog":             "blog", // a stable slug is unchanged
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}

	// No-usable-characters inputs slugify to "" (the caller rejects these).
	for _, in := range []string{"", "   ", "!!!", "---", "你好"} {
		if got := Slugify(in); got != "" {
			t.Errorf("Slugify(%q) = %q, want \"\"", in, got)
		}
	}

	// Whatever Slugify returns non-empty MUST satisfy Valid, including at the
	// 63-char boundary where the cap could otherwise expose a trailing hyphen.
	long := strings.Repeat("ab-", 40) // 120 chars, ends mid-hyphen-group after cap
	if got := Slugify(long); got != "" && !Valid(got) {
		t.Errorf("Slugify(%q) = %q which is not Valid", long, got)
	}
	if got := Slugify(strings.Repeat("a", 100)); !Valid(got) {
		t.Errorf("Slugify of 100 a's = %q which is not Valid", got)
	}
}
