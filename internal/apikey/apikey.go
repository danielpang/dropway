// Package apikey mints and parses Dropway org-scoped API keys.
//
// A key is a high-entropy bearer secret of the form
//
//	dw_live_<43 base62 chars>   (256 bits of crypto/rand)
//
// The fixed `dw_live_` prefix makes a key recognizable at the auth boundary
// (distinguishing it from a Better Auth JWT in the same Authorization: Bearer
// header), greppable in leaked code, and registrable with secret scanners.
//
// The server stores only Hash(secret) — a plain SHA-256, NOT bcrypt. The secret is
// 256 bits of entropy, so brute force is not a threat and verification must be an
// indexed equality lookup on every request; bcrypt's per-verify cost + salt would
// preclude that. (Site passwords are low-entropy and DO use bcrypt — see
// internal/pwhash.) The plaintext exists only in the creation response; afterward
// only the hash and a short non-secret display prefix are retained.
package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"
)

// Prefix is the fixed, human-and-machine-recognizable key marker. HasPrefix keys
// off exactly this so the auth middleware can route a token to the key path
// without a DB hit.
const Prefix = "dw_live_"

// base62 is the alphabet for the random portion: URL-safe, shell-safe, and free of
// the +/= that would need escaping in env vars or CI secret stores.
const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// randomLen is the number of base62 characters in the random portion. 43 base62
// chars carry log2(62)*43 ≈ 256 bits of entropy.
const randomLen = 43

// displayRandomLen is how many chars of the random portion the stored, non-secret
// key_prefix keeps (e.g. "dw_live_3fk9"): enough to match a leaked key to a row,
// useless for recovery.
const displayRandomLen = 4

// ErrMalformed is returned by Parse for a token that is not a well-formed key.
var ErrMalformed = errors.New("apikey: malformed key")

// Generate returns a fresh key secret. The full secret is returned to the caller
// exactly once (it is never persisted); the caller stores Hash(secret) and
// DisplayPrefix(secret).
func Generate() (string, error) {
	b := make([]byte, randomLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	// Map each random byte into the base62 alphabet. Modulo bias over 62 on a
	// uniform byte is negligible for this purpose (the goal is 256 bits of secret,
	// not a uniform base62 string), and every position still draws from a fresh CSPRNG
	// byte, so there is no shortening of the keyspace that matters at this scale.
	var sb strings.Builder
	sb.Grow(len(Prefix) + randomLen)
	sb.WriteString(Prefix)
	for _, c := range b {
		sb.WriteByte(base62[int(c)%len(base62)])
	}
	return sb.String(), nil
}

// HasPrefix reports whether token looks like an API key (so the auth middleware
// routes it to the key path rather than the JWT verifier). It is a cheap syntactic
// check, not authentication — Parse + a hash lookup do that.
func HasPrefix(token string) bool {
	return strings.HasPrefix(token, Prefix)
}

// Parse validates a token's shape and returns it unchanged, or ErrMalformed. It
// enforces the prefix and the exact random length so a truncated/garbage token is
// rejected before it ever hits the database.
func Parse(token string) (string, error) {
	if !strings.HasPrefix(token, Prefix) {
		return "", ErrMalformed
	}
	rest := token[len(Prefix):]
	if len(rest) != randomLen {
		return "", ErrMalformed
	}
	for i := 0; i < len(rest); i++ {
		if !isBase62(rest[i]) {
			return "", ErrMalformed
		}
	}
	return token, nil
}

// Hash returns the lowercase-hex SHA-256 of the full secret — the value stored in
// app.api_keys.key_hash and looked up on every keyed request.
func Hash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// DisplayPrefix returns the non-secret key_prefix stored for display: the fixed
// marker plus the first few chars of the random portion (e.g. "dw_live_3fk9"). For
// a token that lacks the prefix it returns the marker alone (defensive; callers
// pass a freshly Generate()d secret).
func DisplayPrefix(secret string) string {
	rest := strings.TrimPrefix(secret, Prefix)
	if len(rest) > displayRandomLen {
		rest = rest[:displayRandomLen]
	}
	return Prefix + rest
}

// ConstantTimeEqualHash compares two hex hashes without leaking timing. The DB
// lookup is an indexed equality on the hash (no oracle, since the attacker-supplied
// input is hashed first), but callers doing an in-memory comparison use this.
func ConstantTimeEqualHash(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func isBase62(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}
