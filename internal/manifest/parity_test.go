package manifest

import "testing"

// TestDigestParityVectors pins the deploy-digest contract to the SAME vectors the
// TypeScript SDK tests against (packages/sdk/testdata/manifest-digest.json). If any
// value changes, the SDK's digest() would silently disagree with what the API
// recomputes at finalize — so both sides guard the identical constants. The
// non-ascii vector mixes a supplementary-plane filename (emoji) with a high-BMP one
// so a UTF-16-vs-UTF-8 sort divergence in either implementation is caught here.
// Regenerate BOTH fixtures together if the digest contract is ever revised.
func TestDigestParityVectors(t *testing.T) {
	cases := []struct {
		name  string
		files []File
		want  string
	}{
		{
			name: "ascii",
			files: []File{
				{Path: "index.html", SHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
				{Path: "app.js", SHA256: "2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae"},
				{Path: "assets/style.css", SHA256: "fcde2b2edba56bf408601fb721fe9b5c338d10ee429ea04fae5511b68fbf8fb9"},
			},
			want: "182826e14d30b208c6ffabf124c4d576726b9c093997adf6de510ff2a5fe9734",
		},
		{
			name: "non-ascii",
			files: []File{
				{Path: "a.txt", SHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
				{Path: "Ａ.txt", SHA256: "2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae"},
				{Path: "😀.txt", SHA256: "fcde2b2edba56bf408601fb721fe9b5c338d10ee429ea04fae5511b68fbf8fb9"},
			},
			want: "38ce7b72fbd462471bdbdf90c7d97ffb14f9e1c1eb34b13c88790a47ac5bb820",
		},
	}
	for _, c := range cases {
		if got := Digest(c.files); got != c.want {
			t.Errorf("digest[%s] changed: got %s, want %s (the @dropway/sdk parity vector must be regenerated in lock-step)", c.name, got, c.want)
		}
	}
}
