package manifest

import "testing"

// TestDigestParityVector pins the deploy-digest contract to the SAME vector the
// TypeScript SDK tests against (packages/sdk/testdata/manifest-digest.json). If
// this value ever changes, the SDK's digest() would silently disagree with what
// the API recomputes at finalize — so both sides guard the identical constant.
// Regenerate BOTH fixtures together if the digest contract is ever revised.
func TestDigestParityVector(t *testing.T) {
	files := []File{
		{Path: "index.html", SHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{Path: "app.js", SHA256: "2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae"},
		{Path: "assets/style.css", SHA256: "fcde2b2edba56bf408601fb721fe9b5c338d10ee429ea04fae5511b68fbf8fb9"},
	}
	const want = "182826e14d30b208c6ffabf124c4d576726b9c093997adf6de510ff2a5fe9734"
	if got := Digest(files); got != want {
		t.Fatalf("digest contract changed: got %s, want %s (the @dropway/sdk parity vector must be regenerated in lock-step)", got, want)
	}
}
