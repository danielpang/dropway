// Package pwhash hashes and verifies site passwords for the `password` access
// mode (docs/ARCHITECTURE.md §6). It wraps bcrypt: a salted, adaptive hash whose
// CompareHashAndPassword is constant-time with respect to the hash, so a password
// check does not leak timing about how much of the password matched. The plaintext
// is NEVER stored — only the bcrypt hash goes into site_access_policy.password_hash.
package pwhash

import (
	"errors"

	"golang.org/x/crypto/bcrypt"
)

// cost is the bcrypt work factor. 12 is a reasonable 2020s default (well above
// bcrypt's own DefaultCost of 10) without being so slow it invites a CPU-DoS on the
// password gate.
const cost = 12

// ErrMismatch is returned by Verify when the password does not match the hash.
var ErrMismatch = errors.New("pwhash: password does not match")

// MaxPasswordLen bounds the input bcrypt accepts (bcrypt silently truncates at 72
// bytes; we reject longer up front so the truncation is never surprising).
const MaxPasswordLen = 72

// Hash returns the bcrypt hash of password. An empty password is rejected (a
// password-mode site must have a real password).
func Hash(password string) (string, error) {
	if password == "" {
		return "", errors.New("pwhash: empty password")
	}
	if len(password) > MaxPasswordLen {
		return "", errors.New("pwhash: password too long")
	}
	b, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Verify reports whether password matches the bcrypt hash. It returns ErrMismatch
// on a non-match (constant-time w.r.t. the hash) and a wrapped error for a
// malformed hash. An over-long password is a mismatch (never a panic).
func Verify(hash, password string) error {
	if len(password) > MaxPasswordLen {
		return ErrMismatch
	}
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if err == nil {
		return nil
	}
	if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		return ErrMismatch
	}
	return err
}

// dummyHash is a precomputed bcrypt hash (of a random value) used by DummyVerify to
// burn an equivalent amount of CPU when there is no real hash to compare against —
// so the password gate's response time does not reveal whether a host exists / is a
// password site (a timing-oracle defense; ARCHITECTURE.md §10 denial-of-wallet /
// enumeration). The cost matches `cost` above.
const dummyHash = "$2a$12$krlz5cRnRx/Y3iVv83Ch4.aueU0daM0BsggOa4QQfrsa1rY1FMe7W"

// DummyVerify performs a throwaway bcrypt comparison to equalize timing on the
// "no password to check" path (unknown host / not a password site). It always
// returns ErrMismatch; callers use it purely for its constant-time side effect.
func DummyVerify(password string) error {
	if len(password) > MaxPasswordLen {
		return ErrMismatch
	}
	_ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(password))
	return ErrMismatch
}
