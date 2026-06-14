package manifest

import (
	"strings"
	"testing"
)

// TestSummary covers the human-readable CLI summary line and its helpers
// (humanBytes, short) across the byte-unit boundaries and the short-digest edge.

func TestSummary(t *testing.T) {
	m := &Manifest{
		Files:     []Entry{{Path: "index.html"}, {Path: "app.js"}},
		TotalSize: 2048,
		Digest:    "0123456789abcdef0123456789abcdef",
	}
	s := m.Summary()
	if !strings.Contains(s, "2 file(s)") {
		t.Errorf("summary should report the file count: %q", s)
	}
	if !strings.Contains(s, "2.0 KB") {
		t.Errorf("summary should report a human size (2.0 KB): %q", s)
	}
	// The digest is shortened to its first 12 hex chars.
	if !strings.Contains(s, "0123456789ab") {
		t.Errorf("summary should include the 12-char short digest: %q", s)
	}
	if strings.Contains(s, m.Digest) {
		t.Errorf("summary should NOT include the full digest: %q", s)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{3 * 1024 * 1024 * 1024, "3.0 GB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.n); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestShort(t *testing.T) {
	if got := short("0123456789abcdef"); got != "0123456789ab" {
		t.Errorf("short(long) = %q, want first 12 chars", got)
	}
	// A digest 12 chars or shorter is returned whole (the empty-manifest / dry-run
	// edge where the digest could be short).
	if got := short("abc"); got != "abc" {
		t.Errorf("short(short) = %q, want it unchanged", got)
	}
	if got := short("0123456789ab"); got != "0123456789ab" {
		t.Errorf("short(exactly 12) = %q, want it unchanged", got)
	}
}

// TestPathHashMap covers the path→hash projection used by the prepare body.
func TestPathHashMap(t *testing.T) {
	m := &Manifest{Files: []Entry{
		{Path: "index.html", SHA256: "aaa"},
		{Path: "app.js", SHA256: "bbb"},
	}}
	hm := m.PathHashMap()
	if hm["index.html"] != "aaa" || hm["app.js"] != "bbb" {
		t.Errorf("PathHashMap = %+v", hm)
	}
	if len(hm) != 2 {
		t.Errorf("PathHashMap len = %d, want 2", len(hm))
	}
}
