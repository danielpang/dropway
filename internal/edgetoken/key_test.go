package edgetoken

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

// TestNewSigner_BadKeySize asserts the constructor rejects a private key of the
// wrong length (a misconfigured EDGE_SIGNING_KEY must never produce a signer).
func TestNewSigner_BadKeySize(t *testing.T) {
	if _, err := NewSigner(ed25519.PrivateKey([]byte("too-short"))); err == nil {
		t.Fatal("expected an error for a wrong-size private key")
	}
}

// TestKidFor covers the key-id derivation edge cases: an empty key yields an empty
// kid; a normal 32-byte key is truncated to the first 16 bytes (hex), and the kid
// is deterministic for the same key.
func TestKidFor(t *testing.T) {
	if KidFor(nil) != "" {
		t.Errorf("KidFor(nil) = %q, want empty", KidFor(nil))
	}
	if KidFor(ed25519.PublicKey{}) != "" {
		t.Errorf("KidFor(empty) should be empty")
	}

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	kid := KidFor(pub)
	if !strings.HasPrefix(kid, "edge-") {
		t.Errorf("kid = %q, want edge- prefix", kid)
	}
	// 16 bytes → 32 hex chars after the "edge-" prefix.
	if got := len(strings.TrimPrefix(kid, "edge-")); got != 32 {
		t.Errorf("kid hex len = %d, want 32 (first 16 bytes)", got)
	}
	if KidFor(pub) != kid {
		t.Error("KidFor must be deterministic for the same key")
	}

	// A short (<16 byte) key uses all of its bytes (no panic on the slice bound).
	short := ed25519.PublicKey([]byte("abcd"))
	if got := KidFor(short); got != "edge-"+hex.EncodeToString([]byte("abcd")) {
		t.Errorf("short-key kid = %q", got)
	}
}

// TestJWKSJSON asserts the marshaled JWKS bytes are well-formed and carry the
// signer's single OKP/Ed25519 key (the HTTP handler serves these bytes).
func TestJWKSJSON(t *testing.T) {
	s := newTestSigner(t)
	b, err := s.JWKSJSON()
	if err != nil {
		t.Fatal(err)
	}
	var set JWKS
	if err := json.Unmarshal(b, &set); err != nil {
		t.Fatalf("JWKSJSON not valid JSON: %v", err)
	}
	if len(set.Keys) != 1 || set.Keys[0].Kid != s.Kid() {
		t.Fatalf("jwks json = %s", b)
	}
}

// TestParsePrivateKey_Encodings asserts a 32-byte seed and a 64-byte full key both
// load to a signer that produces the SAME kid (i.e. the same underlying key). The
// decode ladder tries base64url first, so a base64url encoding is the
// deterministic, unambiguous form (a hex string of the same bytes is ALSO valid
// base64 and would be decoded by that earlier branch to a different length — see
// TestDecodeKeyBytes_HexIsShadowedByBase64).
func TestParsePrivateKey_Encodings(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	seed := priv.Seed()  // 32 bytes
	full := []byte(priv) // 64 bytes
	wantKid := KidFor(priv.Public().(ed25519.PublicKey))

	encodings := map[string]string{
		"seed base64url": base64.RawURLEncoding.EncodeToString(seed),
		"full base64url": base64.RawURLEncoding.EncodeToString(full),
	}
	for name, enc := range encodings {
		t.Run(name, func(t *testing.T) {
			pk, err := parsePrivateKey(enc)
			if err != nil {
				t.Fatalf("parsePrivateKey(%s): %v", name, err)
			}
			s, err := NewSigner(pk)
			if err != nil {
				t.Fatal(err)
			}
			if s.Kid() != wantKid {
				t.Errorf("%s → kid %q, want %q (different key!)", name, s.Kid(), wantKid)
			}
		})
	}

	// A base64-std seed (the second rung of the ladder) also loads. Std and URL
	// alphabets differ only in '+ /' vs '- _'; RawURLEncoding.DecodeString rejects a
	// string containing '+' or '/', so a std encoding with those chars deterministically
	// falls through to the std rung. Find such a seed so the std branch is always hit.
	for i := 0; i < 200; i++ {
		_, p2, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		s2 := p2.Seed()
		std := base64.StdEncoding.EncodeToString(s2)
		if !strings.ContainsAny(std, "+/") {
			continue
		}
		// '+'/'/' are not in the base64url alphabet, so the url rung fails and the std rung wins.
		if _, urlErr := base64.RawURLEncoding.DecodeString(std); urlErr == nil {
			continue // (extremely unlikely) still url-decodable; skip
		}
		pk, err := parsePrivateKey(std)
		if err != nil {
			t.Fatalf("parsePrivateKey(base64 std): %v", err)
		}
		want := KidFor(p2.Public().(ed25519.PublicKey))
		if s, _ := NewSigner(pk); s.Kid() != want {
			t.Errorf("base64-std seed → kid %q, want %q", s.Kid(), want)
		}
		return
	}
	t.Skip("could not generate a seed whose std encoding has '+' or '/' (vanishingly unlikely)")
}

// TestParsePrivateKey_Errors asserts the bad-input branches: undecodable text and
// a decodable-but-wrong-length blob both error.
func TestParsePrivateKey_Errors(t *testing.T) {
	// A string with whitespace is not valid base64url/std/hex.
	if _, err := parsePrivateKey("!!! not base64 or hex !!!"); err == nil {
		t.Error("undecodable key should error")
	}
	// base64url of 16 bytes decodes fine but is the wrong length (≠ seed 32 / key 64).
	if _, err := parsePrivateKey(base64.RawURLEncoding.EncodeToString(make([]byte, 16))); err == nil {
		t.Error("wrong-length key should error")
	}
}

// TestDecodeKeyBytes covers the base64url success rung and the all-fail path.
func TestDecodeKeyBytes(t *testing.T) {
	raw := []byte{0xde, 0xad, 0xbe, 0xef}
	if got, err := decodeKeyBytes(base64.RawURLEncoding.EncodeToString(raw)); err != nil || string(got) != string(raw) {
		t.Errorf("base64url decode = %x %v", got, err)
	}
	// All-encodings-fail path: '@' and a space are outside base64url, base64-std, and hex.
	if _, err := decodeKeyBytes("@ @"); err == nil {
		t.Error("a string decodable by no encoding should error")
	}
}

// TestDecodeKeyBytes_HexIsShadowedByBase64 documents a real subtlety of the decode
// ladder: a pure-hex string is ALSO valid base64, and since base64url is tried
// first, a hex-encoded value is decoded as base64 (yielding different bytes), not
// hex. This is why callers should prefer base64url for EDGE_SIGNING_KEY. We assert
// the precedence so a future reorder of the ladder is caught.
func TestDecodeKeyBytes_HexIsShadowedByBase64(t *testing.T) {
	const hexStr = "deadbeef" // 8 chars; valid base64url AND valid hex
	got, err := decodeKeyBytes(hexStr)
	if err != nil {
		t.Fatal(err)
	}
	wantB64, _ := base64.RawURLEncoding.DecodeString(hexStr)
	if string(got) != string(wantB64) {
		t.Errorf("decodeKeyBytes(%q) = %x, want the base64url decode %x (base64 takes precedence over hex)", hexStr, got, wantB64)
	}
}

// TestLoadOrGenerateSigner_BadKey asserts a malformed EDGE_SIGNING_KEY surfaces an
// error (rather than silently generating a fresh key, which would break the
// SEPARATE-keypair contract and confuse the Worker's JWKS).
func TestLoadOrGenerateSigner_BadKey(t *testing.T) {
	if _, _, _, err := LoadOrGenerateSigner("not-a-valid-key!!"); err == nil {
		t.Fatal("a malformed EDGE_SIGNING_KEY should error, not generate")
	}
	// Whitespace is trimmed; a value that is only whitespace is treated as empty →
	// generates an ephemeral key.
	s, seed, generated, err := LoadOrGenerateSigner("   ")
	if err != nil {
		t.Fatal(err)
	}
	if !generated || seed == "" || s == nil {
		t.Fatalf("whitespace-only key should generate: generated=%v seed=%q", generated, seed)
	}
}
