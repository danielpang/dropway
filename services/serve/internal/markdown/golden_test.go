// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Golden-fixture parity test. Both this Go suite and the TypeScript suite
// (edge/serving-worker/test/markdown.test.ts) render the SHARED Markdown fixtures
// under testdata/markdown/*.md and assert the output matches the committed
// *.html golden. Because both ports check the same goldens, a drift in either
// renderer fails CI — the guard for the cross-language parity the package claims.
package markdown

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGoldenFixtures(t *testing.T) {
	// testdata/markdown lives at the repo root (shared with the TS suite); this
	// package sits four levels below it.
	dir := filepath.Join("..", "..", "..", "..", "testdata", "markdown")
	mdFiles, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(mdFiles) == 0 {
		t.Fatalf("no fixtures found in %s", dir)
	}
	for _, md := range mdFiles {
		md := md
		t.Run(filepath.Base(md), func(t *testing.T) {
			src, err := os.ReadFile(md)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(md[:len(md)-len(".md")] + ".html")
			if err != nil {
				t.Fatalf("missing golden for %s: %v", md, err)
			}
			if got := RenderMarkdown(string(src)); got != string(want) {
				t.Errorf("RenderMarkdown(%s) mismatch:\n got: %q\nwant: %q", filepath.Base(md), got, want)
			}
		})
	}
}
