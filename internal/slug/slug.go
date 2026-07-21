// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package slug is the single source of truth for the site-slug grammar and the
// canonical slugifier, shared by the API store, the CLI, and the MCP service. A
// slug becomes a DNS label in the canonical content host
// (`<orgSlug>-<slug>.<ContentDomain>`) and part of the Cloudflare KV route key,
// so it must be a single safe lowercase label. Keeping Valid + Slugify in one
// pure, dependency-free package lets every client normalize input to exactly
// what the server enforces, instead of each re-deriving (and drifting from) the
// rule.
package slug

import (
	"regexp"
	"strings"
)

// pattern is the strict grammar a slug MUST match: a single lowercase DNS label
// of 1-63 chars — alphanumeric start/end, interior alphanumeric or single
// hyphens. It deliberately excludes uppercase, dots, slashes, percent signs,
// spaces, and everything else unsafe in the content host or the KV route key.
var pattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// nonSlugRun matches a run of characters that are not valid slug characters.
var nonSlugRun = regexp.MustCompile(`[^a-z0-9-]+`)

// hyphenRun matches a run of two or more hyphens.
var hyphenRun = regexp.MustCompile(`-+`)

// Valid reports whether s is a safe, canonical site slug: a single lowercase DNS
// label (1-63 chars) with no leading/trailing hyphen and no `--` run. The `--`
// rejection is LOAD-BEARING even though the host separator is now a single dash:
// pre-migration hosts used `--`, and the serving path 301s any unresolved host
// containing `--` to its single-dash rewrite. A slug containing `--` would let a
// current-format host carry `--` and be falsely rewritten — do not relax this.
func Valid(s string) bool {
	if !pattern.MatchString(s) {
		return false
	}
	return !strings.Contains(s, "--")
}

// Slugify normalizes arbitrary input into a slug that satisfies Valid, mirroring
// the dashboard's client-side slugifier so the CLI/MCP and the UI agree: lower-
// case, every run of non-slug characters becomes a single hyphen, hyphen runs
// collapse, leading/trailing hyphens are trimmed, and the result is capped at 63
// chars (re-trimming any trailing hyphen the cap exposed). Returns "" when the
// input has no usable characters — callers should treat that as "no valid slug"
// rather than sending it.
func Slugify(s string) string {
	s = strings.ToLower(s)
	s = nonSlugRun.ReplaceAllString(s, "-")
	s = hyphenRun.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 63 {
		s = strings.TrimRight(s[:63], "-")
	}
	return s
}
