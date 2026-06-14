package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// TestDigest_ExactFormat pins the byte-for-byte digest contract: the SHA-256 over
// the files sorted by path, one "<sha256>  <path>\n" line each (TWO spaces). This
// is the same algorithm the CLI used inline and the server now recomputes, so a
// drift here would break idempotency across the two.
func TestDigest_ExactFormat(t *testing.T) {
	a := hash("<h1>hi</h1>")
	b := hash("console.log(1)")

	// Expected = sha256 over the SORTED lines: "app.js" sorts before "index.html".
	want := hash(b + "  app.js\n" + a + "  index.html\n")

	got := Digest([]File{
		{Path: "index.html", SHA256: a},
		{Path: "app.js", SHA256: b},
	})
	if got != want {
		t.Fatalf("Digest = %s, want %s", got, want)
	}
}

// TestDigest_OrderIndependent proves the input order does not affect the result
// (Digest sorts internally) and does not mutate the caller's slice.
func TestDigest_OrderIndependent(t *testing.T) {
	a := hash("a")
	b := hash("b")
	c := hash("c")

	in := []File{{Path: "z", SHA256: c}, {Path: "a", SHA256: a}, {Path: "m", SHA256: b}}
	d1 := Digest(in)

	// Caller slice unchanged (Digest sorts a copy).
	if in[0].Path != "z" || in[1].Path != "a" || in[2].Path != "m" {
		t.Fatalf("Digest mutated the input slice: %+v", in)
	}

	d2 := Digest([]File{{Path: "a", SHA256: a}, {Path: "m", SHA256: b}, {Path: "z", SHA256: c}})
	if d1 != d2 {
		t.Fatalf("digest depends on input order: %s != %s", d1, d2)
	}
}

// TestDigest_SensitiveToContentAndPath proves a change to either a file's hash or
// its path changes the digest (so it is a faithful whole-deploy content address).
func TestDigest_SensitiveToContentAndPath(t *testing.T) {
	base := Digest([]File{{Path: "index.html", SHA256: hash("v1")}})
	changedHash := Digest([]File{{Path: "index.html", SHA256: hash("v2")}})
	changedPath := Digest([]File{{Path: "main.html", SHA256: hash("v1")}})

	if base == changedHash {
		t.Error("digest must change when a file's content hash changes")
	}
	if base == changedPath {
		t.Error("digest must change when a file's path changes")
	}
}
