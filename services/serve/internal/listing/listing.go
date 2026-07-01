// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package listing is the Go port of the serving Worker's autoindex
// (edge/serving-worker/src/listing.ts). When a directory request matches no page
// in the manifest, the server synthesizes an HTML listing of that directory's
// immediate children straight from the manifest's path set, so an upload that is
// NOT a static website (e.g. a folder of documents with no index.html) is still
// browsable instead of returning a 404. Each file links to its raw bytes, served
// with the content_type the manifest recorded.
package listing

import (
	"fmt"
	"html"
	"net/url"
	"sort"
	"strings"

	"github.com/danielpang/dropway/services/serve/internal/manifest"
)

// Entry is one row in a directory listing: a child file or subdirectory.
type Entry struct {
	// Name is the display name; a subdirectory carries a trailing slash.
	Name string
	// Href is the absolute, URL-encoded link to the file or subdirectory.
	Href string
	// IsDir is true for a subdirectory row, false for a file.
	IsDir bool
	// Size / HasSize carry the decoded byte size for files, when known.
	Size    int64
	HasSize bool
}

// DirectoryPrefix returns the manifest-key prefix a cleaned request path targets
// (either "" for root or a string ending in "/"). A directory request maps to
// itself; an extension-less "pretty" path is treated as a candidate directory
// (e.g. "docs" → "docs/"). Mirrors directoryPrefix.
func DirectoryPrefix(cleanRelPath string) string {
	if cleanRelPath == "" || strings.HasSuffix(cleanRelPath, "/") {
		return cleanRelPath
	}
	return cleanRelPath + "/"
}

// ListDirectory returns the immediate children (subdirectories + files) of
// dirPrefix within the manifest, or nil when the prefix has NO descendants (a
// genuine typo, which the caller then 404s). Subdirectories are collapsed to
// their first segment and de-duplicated; rows are ordered directories-first then
// files, alphabetically. Mirrors listDirectory.
func ListDirectory(m manifest.Manifest, dirPrefix string) []Entry {
	dirs := map[string]struct{}{}
	var files []Entry

	for key, entry := range m.Files {
		if dirPrefix != "" && !strings.HasPrefix(key, dirPrefix) {
			continue
		}
		rest := key[len(dirPrefix):]
		if rest == "" {
			continue // a key equal to the prefix is not a child
		}
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			dirs[rest[:i]] = struct{}{}
		} else {
			files = append(files, Entry{
				Name:    rest,
				Href:    encodeHref(key),
				IsDir:   false,
				Size:    entry.Size,
				HasSize: entry.HasSize,
			})
		}
	}

	if len(dirs) == 0 && len(files) == 0 {
		return nil
	}

	dirNames := make([]string, 0, len(dirs))
	for d := range dirs {
		dirNames = append(dirNames, d)
	}
	sort.Strings(dirNames)
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })

	out := make([]Entry, 0, len(dirNames)+len(files))
	for _, d := range dirNames {
		out = append(out, Entry{Name: d + "/", Href: encodeHref(dirPrefix + d + "/"), IsDir: true})
	}
	return append(out, files...)
}

// RenderDirectoryListing renders a complete, self-contained HTML listing page.
// Inline styles only (the content CSP allows style-src 'unsafe-inline'), no
// scripts, every tenant-supplied name HTML-escaped. Links are absolute so they
// resolve whether or not the request carried a trailing slash. Mirrors
// renderDirectoryListing.
func RenderDirectoryListing(dirPrefix string, entries []Entry) string {
	display := "/" + dirPrefix
	var rows strings.Builder

	if dirPrefix != "" {
		fmt.Fprintf(&rows,
			`<tr><td><a href="%s">Parent directory</a></td><td></td></tr>`,
			html.EscapeString(parentHref(dirPrefix)))
		rows.WriteByte('\n')
	}
	for _, e := range entries {
		cls := ""
		size := ""
		if e.IsDir {
			cls = ` class="dir"`
		} else {
			size = html.EscapeString(formatSize(e))
		}
		fmt.Fprintf(&rows,
			`<tr><td><a%s href="%s">%s</a></td><td>%s</td></tr>`,
			cls, html.EscapeString(e.Href), html.EscapeString(e.Name), size)
		rows.WriteByte('\n')
	}

	noun := "items"
	if len(entries) == 1 {
		noun = "item"
	}

	return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Index of ` + html.EscapeString(display) + `</title>
<style>
:root { color-scheme: light dark; }
body { font: 15px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; margin: 0; padding: 0; background: #fafafa; color: #1a1a1a; }
header { position: sticky; top: 0; z-index: 10; display: flex; align-items: center; gap: 0.75rem; min-height: 3.4rem; box-sizing: border-box; padding: 0.6rem 1.25rem; background: rgba(250,250,250,0.85); backdrop-filter: saturate(1.8) blur(8px); border-bottom: 1px solid #e3e3e3; }
header a.brand { display: inline-flex; align-items: center; gap: 0.45rem; text-decoration: none; color: inherit; font-size: 0.9rem; font-weight: 700; white-space: nowrap; }
header a.brand:hover { text-decoration: none; }
header a.brand svg { width: 1.3rem; height: 1.3rem; display: block; }
main { max-width: 760px; margin: 0 auto; padding: 2.5rem 1.25rem; }
h1 { font-size: 1.05rem; font-weight: 600; margin: 0 0 1.25rem; word-break: break-all; }
table { width: 100%; border-collapse: collapse; }
th { text-align: left; font-size: 0.78rem; text-transform: uppercase; letter-spacing: 0.04em; color: #888; font-weight: 600; padding: 0 0 0.5rem; border-bottom: 1px solid #e3e3e3; }
th:last-child, td:last-child { text-align: right; color: #888; font-variant-numeric: tabular-nums; white-space: nowrap; }
td { padding: 0.45rem 0; border-bottom: 1px solid #ededed; }
a { color: #1a56db; text-decoration: none; word-break: break-all; }
a:hover { text-decoration: underline; }
a.dir { font-weight: 600; }
footer { margin-top: 1.5rem; font-size: 0.8rem; color: #999; }
@media (prefers-color-scheme: dark) {
  body { background: #16181c; color: #e6e6e6; }
  header { background: rgba(22,24,28,0.85); border-bottom-color: #2a2d34; }
  th { color: #8a8f98; border-bottom-color: #2a2d34; }
  th:last-child, td:last-child { color: #8a8f98; }
  td { border-bottom-color: #23262c; }
  a { color: #6ea8ff; }
  footer { color: #6a6f78; }
}
</style>
</head>
<body>
<header>
<a class="brand" href="https://dropway.dev" target="_blank" rel="noopener noreferrer">
<svg viewBox="0 0 100 100" aria-hidden="true"><rect width="100" height="100" rx="18" fill="#5647e1"></rect><g transform="translate(17 17) scale(0.66)"><path fill="#ffffff" fill-rule="evenodd" d="M50 7 L85 55 L62 92 L38 92 L15 55 Z M50 7 L34 51 L46 58 Z"></path></g></svg>
<span>Dropway</span>
</a>
</header>
<main>
<h1>Index of ` + html.EscapeString(display) + `</h1>
<table>
<thead><tr><th>Name</th><th>Size</th></tr></thead>
<tbody>
` + strings.TrimRight(rows.String(), "\n") + `
</tbody>
</table>
<footer>` + fmt.Sprintf("%d %s", len(entries), noun) + `</footer>
</main>
</body>
</html>
`
}

// encodeHref builds an absolute, per-segment URL-encoded href for a manifest key
// (preserving any trailing slash). Mirrors encodeHref.
func encodeHref(key string) string {
	trailing := strings.HasSuffix(key, "/")
	var segs []string
	for _, s := range strings.Split(key, "/") {
		if s == "" {
			continue
		}
		segs = append(segs, url.PathEscape(s))
	}
	encoded := strings.Join(segs, "/")
	if trailing && encoded != "" {
		return "/" + encoded + "/"
	}
	return "/" + encoded
}

// parentHref returns the absolute href to the parent of a directory prefix
// ("docs/sub/" → "/docs/"). Mirrors parentHref.
func parentHref(dirPrefix string) string {
	trimmed := strings.TrimSuffix(dirPrefix, "/")
	if i := strings.LastIndexByte(trimmed, '/'); i >= 0 {
		return encodeHref(trimmed[:i+1])
	}
	return "/"
}

// formatSize renders a human-readable byte size; "" when the size is unknown.
// Mirrors formatSize.
func formatSize(e Entry) string {
	if !e.HasSize {
		return ""
	}
	n := e.Size
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	v := float64(n) / 1024
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if v < 10 {
		return fmt.Sprintf("%.1f %s", v, units[i])
	}
	return fmt.Sprintf("%.0f %s", v, units[i])
}
