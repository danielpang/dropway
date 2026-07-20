package apikey

import (
	"strings"
	"testing"
)

func TestGenerateProducesParseableKey(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		k, err := Generate()
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if !HasPrefix(k) {
			t.Fatalf("generated key lacks prefix: %q", k)
		}
		if _, err := Parse(k); err != nil {
			t.Fatalf("generated key does not parse: %q: %v", k, err)
		}
		if len(k) != len(Prefix)+randomLen {
			t.Fatalf("unexpected key length %d", len(k))
		}
		if seen[k] {
			t.Fatalf("duplicate key generated (entropy failure): %q", k)
		}
		seen[k] = true
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	long, _ := Generate()
	cases := []string{
		"",
		"nope",
		"dw_live_",      // no random part
		"dw_live_short", // too short
		Prefix + strings.Repeat("A", randomLen-1),   // one short
		Prefix + strings.Repeat("A", randomLen+1),   // one long
		Prefix + strings.Repeat("!", randomLen),     // non-base62
		"DW_LIVE_" + strings.Repeat("A", randomLen), // wrong-case prefix
		long + "x", // trailing junk
	}
	for _, c := range cases {
		if _, err := Parse(c); err == nil {
			t.Errorf("Parse(%q) = nil error, want ErrMalformed", c)
		}
	}
}

func TestHashIsStableAndPrefixed(t *testing.T) {
	k, _ := Generate()
	h1 := Hash(k)
	h2 := Hash(k)
	if h1 != h2 {
		t.Fatalf("Hash not deterministic: %q vs %q", h1, h2)
	}
	if len(h1) != 64 { // sha256 hex
		t.Fatalf("unexpected hash length %d", len(h1))
	}
	if Hash(k) == Hash(k+"x") {
		t.Fatalf("distinct secrets hashed to the same value")
	}
	dp := DisplayPrefix(k)
	if !strings.HasPrefix(dp, Prefix) {
		t.Fatalf("display prefix missing marker: %q", dp)
	}
	if len(dp) != len(Prefix)+displayRandomLen {
		t.Fatalf("display prefix length = %d, want %d", len(dp), len(Prefix)+displayRandomLen)
	}
	// The display prefix must never be enough to recover (or even mostly reveal)
	// the secret.
	if strings.Contains(dp, k[len(Prefix)+displayRandomLen:]) {
		t.Fatalf("display prefix leaks secret tail")
	}
}

func TestConstantTimeEqualHash(t *testing.T) {
	k, _ := Generate()
	h := Hash(k)
	if !ConstantTimeEqualHash(h, h) {
		t.Fatalf("equal hashes reported unequal")
	}
	if ConstantTimeEqualHash(h, Hash(k+"x")) {
		t.Fatalf("unequal hashes reported equal")
	}
}
