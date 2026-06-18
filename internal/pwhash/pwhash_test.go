package pwhash

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestHashVerify_RoundTrip(t *testing.T) {
	h, err := Hash("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if h == "correct horse battery staple" {
		t.Fatal("hash equals plaintext (not hashed!)")
	}
	if err := Verify(h, "correct horse battery staple"); err != nil {
		t.Fatalf("verify of correct password failed: %v", err)
	}
	if err := Verify(h, "wrong"); !errors.Is(err, ErrMismatch) {
		t.Fatalf("verify of wrong password = %v, want ErrMismatch", err)
	}
}

func TestHash_RejectsEmptyAndLong(t *testing.T) {
	if _, err := Hash(""); err == nil {
		t.Error("empty password should be rejected")
	}
	if _, err := Hash(strings.Repeat("x", MaxPasswordLen+1)); err == nil {
		t.Error("over-long password should be rejected")
	}
}

func TestVerify_LongPasswordIsMismatch(t *testing.T) {
	h, _ := Hash("short")
	if err := Verify(h, strings.Repeat("x", MaxPasswordLen+1)); !errors.Is(err, ErrMismatch) {
		t.Fatalf("over-long verify = %v, want ErrMismatch", err)
	}
}

func TestHash_DistinctSalts(t *testing.T) {
	a, _ := Hash("same")
	b, _ := Hash("same")
	if a == b {
		t.Fatal("two hashes of the same password are identical (no salt)")
	}
}

// TestDummyVerify asserts the timing-equalization dummy always reports a mismatch
// (callers use it only for its constant-time bcrypt side effect). A non-trivial
// elapsed time confirms it actually ran a bcrypt comparison (not a fast
// malformed-hash bail-out), which is the whole point.
func TestDummyVerify(t *testing.T) {
	if err := DummyVerify("anything"); !errors.Is(err, ErrMismatch) {
		t.Fatalf("DummyVerify = %v, want ErrMismatch", err)
	}
	start := time.Now()
	_ = DummyVerify("another")
	if elapsed := time.Since(start); elapsed < time.Millisecond {
		t.Errorf("DummyVerify too fast (%v) — likely a malformed-hash bail-out, not real bcrypt work", elapsed)
	}
}
