package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sha(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func TestBuild_HashesAndSorts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.html", "<h1>hi</h1>")
	writeFile(t, dir, "assets/app.js", "console.log(1)")
	writeFile(t, dir, "a/b/c.css", "body{}")

	m, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(m.Files) != 3 {
		t.Fatalf("got %d files, want 3", len(m.Files))
	}

	// Sorted by forward-slash path.
	wantOrder := []string{"a/b/c.css", "assets/app.js", "index.html"}
	for i, e := range m.Files {
		if e.Path != wantOrder[i] {
			t.Errorf("file[%d].Path = %q, want %q", i, e.Path, wantOrder[i])
		}
	}

	// Correct content hash for index.html.
	hm := m.PathHashMap()
	if got := hm["index.html"]; got != sha("<h1>hi</h1>") {
		t.Errorf("index.html hash = %q, want %q", got, sha("<h1>hi</h1>"))
	}

	// Total size is the sum of byte lengths.
	wantTotal := int64(len("<h1>hi</h1>") + len("console.log(1)") + len("body{}"))
	if m.TotalSize != wantTotal {
		t.Errorf("TotalSize = %d, want %d", m.TotalSize, wantTotal)
	}
	if m.Digest == "" || len(m.Digest) != 64 {
		t.Errorf("digest looks wrong: %q", m.Digest)
	}
}

func TestBuild_Deterministic(t *testing.T) {
	mk := func() *Manifest {
		dir := t.TempDir()
		writeFile(t, dir, "z.txt", "zzz")
		writeFile(t, dir, "a.txt", "aaa")
		m, err := Build(dir)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	if mk().Digest != mk().Digest {
		t.Error("digest must be stable across identical trees")
	}
}

func TestBuild_ContentChangeChangesDigest(t *testing.T) {
	dir1 := t.TempDir()
	writeFile(t, dir1, "f.txt", "one")
	m1, _ := Build(dir1)

	dir2 := t.TempDir()
	writeFile(t, dir2, "f.txt", "two")
	m2, _ := Build(dir2)

	if m1.Digest == m2.Digest {
		t.Error("different content must yield different digests")
	}
}

func TestBuild_Errors(t *testing.T) {
	if _, err := Build(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("missing dir should error")
	}

	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(f); err == nil {
		t.Error("a file (not a dir) should error")
	}
}

func TestBuild_SkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "real.txt", "real")
	// Create a symlink; if the OS refuses, skip (CI-friendly).
	if err := os.Symlink(filepath.Join(dir, "real.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	m, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.PathHashMap()["link.txt"]; ok {
		t.Error("symlinks must be skipped")
	}
	if _, ok := m.PathHashMap()["real.txt"]; !ok {
		t.Error("regular files must be included")
	}
}
