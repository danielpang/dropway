// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ai

import (
	"strings"
	"testing"
)

func TestParseExtractedMemoriesToleratesProseAndFences(t *testing.T) {
	text := "Here you go:\n```json\n[{\"kind\":\"preference\",\"content\":\"Always end pages with a demo CTA\"},{\"kind\":\"bogus\",\"content\":\"Brand voice is playful\"},{\"kind\":\"style\",\"content\":\"\"}]\n```"
	got := parseExtractedMemories(text)
	if len(got) != 2 {
		t.Fatalf("got %d memories, want 2: %+v", len(got), got)
	}
	if got[0].Kind != "preference" {
		t.Errorf("kind[0] = %q", got[0].Kind)
	}
	// Unknown kinds coerce to fact rather than dropping the content.
	if got[1].Kind != "fact" {
		t.Errorf("kind[1] = %q, want fact", got[1].Kind)
	}
}

func TestParseExtractedMemoriesEmptyAndGarbage(t *testing.T) {
	if got := parseExtractedMemories("no json here"); got != nil {
		t.Errorf("garbage → %+v, want nil", got)
	}
	if got := parseExtractedMemories("[]"); len(got) != 0 {
		t.Errorf("[] → %+v, want empty", got)
	}
}

func TestStripHTML(t *testing.T) {
	in := `<html><head><style>body{color:red}</style><script>evil()</script></head>
<body><h1>Acme &amp; Co</h1><p>We build <b>rockets</b>.</p></body></html>`
	out := stripHTML(in)
	for _, banned := range []string{"<", "evil()", "color:red"} {
		if strings.Contains(out, banned) {
			t.Errorf("stripHTML left %q in output %q", banned, out)
		}
	}
	for _, want := range []string{"Acme & Co", "We build rockets ."} {
		if !strings.Contains(out, want) {
			t.Errorf("stripHTML output %q missing %q", out, want)
		}
	}
}

func TestChunkTextRespectsSizeAndParagraphs(t *testing.T) {
	para := strings.Repeat("word ", 100) // ~500 bytes
	text := para + "\n\n" + para + "\n\n" + para
	chunks := chunkText(text, 1000)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if len(c) > 1100 {
			t.Errorf("chunk %d exceeds budget: %d bytes", i, len(c))
		}
		if strings.TrimSpace(c) == "" {
			t.Errorf("chunk %d is empty", i)
		}
	}
	if got := chunkText("   ", 1000); got != nil {
		t.Errorf("whitespace-only → %v, want nil", got)
	}
}

func TestIndexableTextPath(t *testing.T) {
	yes := []string{"index.html", "docs/README.md", "notes.TXT", "a/b/page.htm"}
	no := []string{"app.js", "style.css", "logo.png", "data.json", "font.woff2"}
	for _, p := range yes {
		if !indexableTextPath(p) {
			t.Errorf("indexableTextPath(%q) = false, want true", p)
		}
	}
	for _, p := range no {
		if indexableTextPath(p) {
			t.Errorf("indexableTextPath(%q) = true, want false", p)
		}
	}
}
