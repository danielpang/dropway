// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ai

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/danielpang/dropway/services/api/internal/store"
)

// Content indexing: after a site version or skill is published, its text
// content is chunked, embedded, and stored in org_content_chunks so retrieval
// can quote the org's existing sites and skills ("make the pricing section
// like our launch site"). Chunks ride their source's lifecycle (FK CASCADE);
// search only ever surfaces chunks of each site's CURRENT version.

const (
	// chunkSize is the target chunk length in bytes (~250 tokens).
	chunkSize = 1000
	// maxChunksPerSource bounds one version/skill's chunk count.
	maxChunksPerSource = 200
	// maxIndexFileBytes skips absurdly large text files.
	maxIndexFileBytes = 512 << 10
	// indexTimeout bounds one async indexing pass.
	indexTimeout = 2 * time.Minute
)

// IndexSiteVersion chunks + embeds a published site version's text content.
// Idempotent via the org_memory_ingests watermark; safe to call after every
// publish. Runs best-effort: errors log and leave the previous chunks intact.
func (r *Runner) IndexSiteVersion(ctx context.Context, t store.Tenant, siteID, versionID string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), indexTimeout)
	defer cancel()
	if !r.memoryEnabled(ctx, t) {
		return
	}
	if done, err := r.Store.MemoryIngestSeq(ctx, t, "site_version", versionID); err != nil || done > 0 {
		return // already indexed (or watermark unreadable — retry next publish)
	}
	raw, err := r.Objects.GetManifest(ctx, t.OrgID, siteID, versionID)
	if err != nil {
		r.logger().Warn("memory index: manifest read failed", "err", err, "version", versionID)
		return
	}
	r.indexManifest(ctx, t, "site_version", raw, &versionID, &siteID, nil)
}

// IndexSkill chunks + embeds a skill's current-version content.
func (r *Runner) IndexSkill(ctx context.Context, t store.Tenant, skillID, versionID string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), indexTimeout)
	defer cancel()
	if !r.memoryEnabled(ctx, t) {
		return
	}
	// Skills re-publish under new version ids; the watermark keys on the skill
	// so a re-upload re-indexes (ReplaceContentChunks swaps atomically).
	raw, err := r.Objects.GetSkillManifest(ctx, t.OrgID, skillID, versionID)
	if err != nil {
		r.logger().Warn("memory index: skill manifest read failed", "err", err, "skill", skillID)
		return
	}
	r.indexManifest(ctx, t, "skill", raw, nil, nil, &skillID)
}

// indexManifest is the shared pass: parse the manifest, load text files, chunk,
// embed, swap the chunk set, advance the watermark (site versions only).
func (r *Runner) indexManifest(ctx context.Context, t store.Tenant, sourceKind string, rawManifest []byte, versionID, siteID, skillID *string) {
	var parsed struct {
		Files map[string]struct {
			SHA256 string `json:"sha256"`
			Size   int64  `json:"size"`
		} `json:"files"`
	}
	if err := json.Unmarshal(rawManifest, &parsed); err != nil {
		r.logger().Warn("memory index: manifest parse failed", "err", err)
		return
	}

	// Deterministic order so chunk_seq assignment is stable across retries.
	paths := make([]string, 0, len(parsed.Files))
	for p := range parsed.Files {
		if indexableTextPath(p) {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)

	var chunks []store.NewChunkInput
	for _, p := range paths {
		entry := parsed.Files[p]
		if entry.Size > maxIndexFileBytes {
			continue
		}
		rc, err := r.Objects.GetBlob(ctx, t.OrgID, entry.SHA256)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(rc, maxIndexFileBytes))
		rc.Close()
		if err != nil {
			continue
		}
		text := string(data)
		if strings.HasSuffix(strings.ToLower(p), ".html") || strings.HasSuffix(strings.ToLower(p), ".htm") {
			text = stripHTML(text)
		}
		for seq, piece := range chunkText(text, chunkSize) {
			chunks = append(chunks, store.NewChunkInput{Path: p, ChunkSeq: int32(seq), Content: piece})
			if len(chunks) >= maxChunksPerSource {
				break
			}
		}
		if len(chunks) >= maxChunksPerSource {
			break
		}
	}

	if len(chunks) > 0 {
		contents := make([]string, len(chunks))
		for i, c := range chunks {
			contents[i] = c.Content
		}
		vecs, err := r.Embedder.Embed(ctx, contents)
		if err != nil || len(vecs) != len(chunks) {
			r.logger().Warn("memory index: embed failed", "err", err, "kind", sourceKind)
			return
		}
		for i := range chunks {
			chunks[i].Embedding = vecs[i]
		}
	}

	if err := r.Store.ReplaceContentChunks(ctx, t, sourceKind, versionID, siteID, skillID, r.Embedder.ModelID(), chunks); err != nil {
		r.logger().Warn("memory index: chunk replace failed", "err", err, "kind", sourceKind)
		return
	}
	if sourceKind == "site_version" && versionID != nil {
		_ = r.Store.AdvanceMemoryIngest(ctx, t, "site_version", *versionID, 1)
	}
}

// indexableTextPath reports whether a manifest path is worth indexing: the
// human-readable content of a site/skill (markup, markdown, plain text) — not
// code, styles, or binaries.
func indexableTextPath(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".html", ".htm", ".md", ".markdown", ".txt":
		return true
	default:
		return false
	}
}

var (
	scriptRe = regexp.MustCompile(`(?is)<(script|style|noscript)\b.*?</(script|style|noscript)>`)
	tagRe    = regexp.MustCompile(`(?s)<[^>]*>`)
)

// stripHTML reduces markup to its visible text: script/style blocks dropped,
// tags removed, entities decoded, whitespace collapsed per line.
func stripHTML(s string) string {
	s = scriptRe.ReplaceAllString(s, " ")
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, ln := range lines {
		if ln = strings.Join(strings.Fields(ln), " "); ln != "" {
			out = append(out, ln)
		}
	}
	return strings.Join(out, "\n")
}

// chunkText splits text into ~size-byte pieces, preferring paragraph then line
// boundaries so chunks stay semantically coherent.
func chunkText(text string, size int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	var chunks []string
	var cur strings.Builder
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			chunks = append(chunks, s)
		}
		cur.Reset()
	}
	for _, para := range strings.Split(text, "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		// A paragraph that alone exceeds the budget is split hard.
		for len(para) > size {
			cut := strings.LastIndex(para[:size], "\n")
			if cut < size/2 {
				cut = size
			}
			if cur.Len() > 0 {
				flush()
			}
			chunks = append(chunks, strings.TrimSpace(para[:cut]))
			para = strings.TrimSpace(para[cut:])
		}
		if cur.Len() > 0 && cur.Len()+len(para) > size {
			flush()
		}
		if cur.Len() > 0 {
			cur.WriteString("\n\n")
		}
		cur.WriteString(para)
	}
	flush()
	return chunks
}
