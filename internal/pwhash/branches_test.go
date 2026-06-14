package pwhash

import (
	"errors"
	"testing"
)

// TestVerify_MalformedHash asserts that a non-bcrypt (malformed) stored hash yields
// a wrapped error — NOT ErrMismatch. The distinction matters: a malformed hash is
// a data/config bug to surface, whereas ErrMismatch is the normal wrong-password
// signal the gate shows the user.
func TestVerify_MalformedHash(t *testing.T) {
	err := Verify("this-is-not-a-bcrypt-hash", "whatever")
	if err == nil {
		t.Fatal("a malformed hash should error")
	}
	if errors.Is(err, ErrMismatch) {
		t.Errorf("malformed hash should NOT be reported as a plain mismatch: %v", err)
	}
}

// TestDummyVerify_OverLongIsFastMismatch asserts the timing-equalization dummy
// short-circuits an over-long password to ErrMismatch (it never panics or runs
// bcrypt on >72 bytes).
func TestDummyVerify_OverLongIsFastMismatch(t *testing.T) {
	long := make([]byte, MaxPasswordLen+1)
	for i := range long {
		long[i] = 'a'
	}
	if err := DummyVerify(string(long)); !errors.Is(err, ErrMismatch) {
		t.Errorf("DummyVerify(over-long) = %v, want ErrMismatch", err)
	}
}

// TestHash_AtMaxLength asserts a password exactly at the bcrypt limit hashes and
// verifies (the boundary is inclusive).
func TestHash_AtMaxLength(t *testing.T) {
	pw := make([]byte, MaxPasswordLen)
	for i := range pw {
		pw[i] = 'p'
	}
	h, err := Hash(string(pw))
	if err != nil {
		t.Fatalf("password at the max length should hash: %v", err)
	}
	if err := Verify(h, string(pw)); err != nil {
		t.Fatalf("verify of max-length password failed: %v", err)
	}
}
