// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package route is the Go port of the serving Worker's host normalization +
// request-path sanitization (edge/serving-worker/src/route.ts). It is the
// security boundary's first line: NormalizeHost feeds the resolve_host lookup,
// and CleanPath feeds manifest resolution. Both MUST match the Worker byte-for-
// byte so a self-host server never diverges on traversal handling or host keying.
package route

import "strings"

// NormalizeHost mirrors route.ts normalizeHost: trim, lowercase, strip the
// :port suffix (everything from the LAST ':'), then drop a single trailing '.'.
// IPv6 literals are not valid content hosts, so a last-colon split is safe.
func NormalizeHost(rawHost string) string {
	host := strings.ToLower(strings.TrimSpace(rawHost))
	if i := strings.LastIndex(host, ":"); i != -1 {
		host = host[:i]
	}
	if strings.HasSuffix(host, ".") {
		host = host[:len(host)-1]
	}
	return host
}

// CleanPath decodes + sanitizes a URL path into a clean, prefix-relative manifest
// key, or reports ok=false when the path is unsafe. It is a faithful port of
// route.ts cleanPath:
//
//   - strip from the first '?'/'#' (defensive; callers pass the path only),
//   - percent-decode EXACTLY ONCE; a malformed encoding → unsafe,
//   - reject a decoded NUL or backslash,
//   - remember a trailing slash; strip leading slashes,
//   - drop ""/"." segments; ANY ".." segment → unsafe (traversal),
//   - rejoin and re-append the trailing slash when the result is non-empty.
//
// The result never starts with '/'. ok=false MUST map to a 404 (fail closed).
func CleanPath(rawPath string) (string, bool) {
	path := rawPath
	if q := strings.IndexAny(path, "?#"); q != -1 {
		path = path[:q]
	}

	decoded, ok := decodeOnce(path)
	if !ok {
		return "", false
	}

	if strings.ContainsRune(decoded, 0) || strings.Contains(decoded, "\\") {
		return "", false
	}

	endedWithSlash := len(decoded) > 1 && strings.HasSuffix(decoded, "/")
	rel := strings.TrimLeft(decoded, "/")

	out := make([]string, 0, 8)
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" || seg == "." {
			continue
		}
		if seg == ".." {
			return "", false
		}
		out = append(out, seg)
	}

	rel = strings.Join(out, "/")
	if endedWithSlash && rel != "" {
		rel += "/"
	}
	return rel, true
}

// decodeOnce percent-decodes a path EXACTLY ONCE with JS decodeURIComponent
// semantics (reject a malformed %-escape, decode %2F to '/', leave '+' as-is).
// Go's url.PathUnescape rejects a bare '%' too, but treats some sequences
// differently, so we implement the decode-once contract directly to stay faithful
// to the Worker (decodeURIComponent does NOT translate '+' to space and decodes
// every valid %HH including %2F). Returns ok=false on any malformed escape.
func decodeOnce(s string) (string, bool) {
	if !strings.Contains(s, "%") {
		return s, true
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '%' {
			b.WriteByte(c)
			continue
		}
		if i+2 >= len(s) {
			return "", false
		}
		hi, ok1 := fromHex(s[i+1])
		lo, ok2 := fromHex(s[i+2])
		if !ok1 || !ok2 {
			return "", false
		}
		b.WriteByte(hi<<4 | lo)
		i += 2
	}
	return b.String(), true
}

func fromHex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

// SafeNextPath normalizes an untrusted `next` redirect hint into a SAFE same-host
// absolute path (open-redirect defense), a faithful port of authz.ts safeNextPath:
// decode once (malformed → "/"); must start with exactly one '/'; reject "//"
// (protocol-relative), any backslash, and any char <=0x20 or ==0x7f. Anything
// else collapses to "/". The result always begins with exactly one '/'.
func SafeNextPath(next string) string {
	if next == "" {
		return "/"
	}
	candidate, ok := decodeOnce(next)
	if !ok {
		return "/"
	}
	if !strings.HasPrefix(candidate, "/") {
		return "/"
	}
	if strings.HasPrefix(candidate, "//") {
		return "/"
	}
	if strings.Contains(candidate, "\\") {
		return "/"
	}
	for i := 0; i < len(candidate); i++ {
		if candidate[i] <= 0x20 || candidate[i] == 0x7f {
			return "/"
		}
	}
	return candidate
}
