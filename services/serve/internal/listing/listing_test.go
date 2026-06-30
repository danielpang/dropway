// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package listing

import (
	"strings"
	"testing"

	"github.com/danielpang/dropway/services/serve/internal/manifest"
)

// manifestOf builds a manifest from a path → size map (sha/content_type are
// irrelevant to the listing logic).
func manifestOf(files map[string]int64) manifest.Manifest {
	m := manifest.Manifest{Files: map[string]manifest.Entry{}}
	for path, size := range files {
		m.Files[path] = manifest.Entry{
			SHA256:      strings.Repeat("a", 64),
			ContentType: "text/plain",
			Size:        size,
			HasSize:     true,
		}
	}
	return m
}

func TestDirectoryPrefix(t *testing.T) {
	cases := map[string]string{
		"":      "",
		"docs/": "docs/",
		"docs":  "docs/",
		"a/b":   "a/b/",
	}
	for in, want := range cases {
		if got := DirectoryPrefix(in); got != want {
			t.Errorf("DirectoryPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestListDirectory_NilForNoDescendants(t *testing.T) {
	m := manifestOf(map[string]int64{"notes.md": 10})
	if got := ListDirectory(m, "missing/"); got != nil {
		t.Errorf("expected nil for a directory with no descendants, got %v", got)
	}
}

func TestListDirectory_RootFiles(t *testing.T) {
	m := manifestOf(map[string]int64{"notes.md": 10, "readme.txt": 20})
	got := ListDirectory(m, "")
	want := []Entry{
		{Name: "notes.md", Href: "/notes.md", IsDir: false, Size: 10, HasSize: true},
		{Name: "readme.txt", Href: "/readme.txt", IsDir: false, Size: 20, HasSize: true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestListDirectory_CollapsesSubdirs(t *testing.T) {
	m := manifestOf(map[string]int64{
		"report.md":        5,
		"assets/a.png":     1,
		"assets/b.png":     2,
		"assets/sub/c.png": 3,
	})
	got := ListDirectory(m, "")
	// Directory first (deduped to "assets/"), then the file.
	if len(got) != 2 || !got[0].IsDir || got[0].Name != "assets/" || got[0].Href != "/assets/" {
		t.Fatalf("unexpected dir row: %+v", got)
	}
	if got[1].Name != "report.md" || got[1].IsDir {
		t.Errorf("unexpected file row: %+v", got[1])
	}
}

func TestListDirectory_SubdirectoryPrefix(t *testing.T) {
	m := manifestOf(map[string]int64{
		"index.html":     100,
		"docs/a.md":      1,
		"docs/img/x.png": 3,
	})
	got := ListDirectory(m, "docs/")
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(got), got)
	}
	if got[0].Name != "img/" || got[0].Href != "/docs/img/" || !got[0].IsDir {
		t.Errorf("dir row = %+v", got[0])
	}
	if got[1].Name != "a.md" || got[1].Href != "/docs/a.md" {
		t.Errorf("file row = %+v", got[1])
	}
}

func TestListDirectory_EncodesHref(t *testing.T) {
	m := manifestOf(map[string]int64{"my report.md": 4})
	got := ListDirectory(m, "")
	if got[0].Href != "/my%20report.md" {
		t.Errorf("href = %q, want /my%%20report.md", got[0].Href)
	}
	if got[0].Name != "my report.md" {
		t.Errorf("name = %q, want raw 'my report.md'", got[0].Name)
	}
}

func TestRenderDirectoryListing_Root(t *testing.T) {
	m := manifestOf(map[string]int64{"notes.md": 1500, "readme.txt": 20})
	html := RenderDirectoryListing("", ListDirectory(m, ""))
	for _, want := range []string{
		"<title>Index of /</title>",
		"<h1>Index of /</h1>",
		`href="/notes.md"`,
		`href="/readme.txt"`,
		"1.5 KB",
		"2 items",
		`<a class="brand" href="https://dropway.dev"`,
		"<span>Dropway</span>",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered listing missing %q", want)
		}
	}
	if strings.Contains(html, "Parent directory") {
		t.Errorf("root listing should not show a parent link")
	}
}

func TestRenderDirectoryListing_ParentLink(t *testing.T) {
	m := manifestOf(map[string]int64{"docs/sub/a.md": 1})
	html := RenderDirectoryListing("docs/sub/", ListDirectory(m, "docs/sub/"))
	if !strings.Contains(html, "<h1>Index of /docs/sub/</h1>") {
		t.Errorf("missing heading; got:\n%s", html)
	}
	if !strings.Contains(html, `href="/docs/"`) {
		t.Errorf("missing parent link to /docs/")
	}
}

func TestRenderDirectoryListing_EscapesNames(t *testing.T) {
	// A slash-free hostile name (a slash would be read as a subdirectory).
	m := manifestOf(map[string]int64{"<img src=x onerror=alert(1)>.txt": 1})
	html := RenderDirectoryListing("", ListDirectory(m, ""))
	if strings.Contains(html, "<img src=x onerror=alert(1)>") {
		t.Errorf("hostile name was not escaped:\n%s", html)
	}
	if !strings.Contains(html, "&lt;img src=x onerror=alert(1)&gt;.txt") {
		t.Errorf("expected escaped name in output:\n%s", html)
	}
}

func TestRenderDirectoryListing_SingularItem(t *testing.T) {
	m := manifestOf(map[string]int64{"only.md": 1})
	html := RenderDirectoryListing("", ListDirectory(m, ""))
	if !strings.Contains(html, "1 item</footer>") {
		t.Errorf("expected singular 'item'; got:\n%s", html)
	}
}
