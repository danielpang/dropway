// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/danielpang/dropway/internal/openrouter"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// Embedder turns text into vectors (satisfied by *embeddings.Client). Nil on
// the Runner → the org-memory feature is off and turns run memory-less.
type Embedder interface {
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
	ModelID() string
}

// MemoryGate plan-gates org memory (the cloud build requires Pro+; OSS leaves
// it nil = allowed). The same cloud adapter satisfies the handlers' gate, so
// the loop's retrieval/extraction/indexing can never run for an org the API
// surface refuses.
type MemoryGate interface {
	AllowMemory(ctx context.Context, t store.Tenant) (allowed bool, reason string, err error)
}

// Memory tuning defaults.
const (
	// nearDupDistance: an extraction candidate at least this close to an
	// existing memory refreshes nothing and is dropped (semantic dedupe).
	nearDupDistance = 0.1
	// maxExtractCandidates bounds what one extraction pass may insert.
	maxExtractCandidates = 10
	// maxExtractInput caps the transcript slice sent for extraction.
	maxExtractInput = 24_000
	// extractTimeout bounds the whole async extraction pass.
	extractTimeout = 2 * time.Minute
)

// memoryEnabled reports whether memory should run for this org: the feature
// must be wired (Embedder set), the org's plan must allow it (fail-closed on
// gate errors — memory is an enhancement, never worth a wrong grant), AND the
// org's memory_enabled flag on.
func (r *Runner) memoryEnabled(ctx context.Context, t store.Tenant) bool {
	if r.Embedder == nil {
		return false
	}
	if r.MemoryGate != nil {
		allowed, _, err := r.MemoryGate.AllowMemory(ctx, t)
		if err != nil || !allowed {
			return false
		}
	}
	enabled, err := r.Store.MemoryEnabled(ctx, t)
	if err != nil {
		return false
	}
	return enabled
}

// --------------------------------------------------------------------------
// Extraction — the async pass that distills durable facts from a chat log.
// --------------------------------------------------------------------------

const extractSystemPrompt = `You extract durable organization-level memory from a conversation between a user and an assistant. Return ONLY a JSON array (no prose, no code fences). Each element: {"kind":"fact"|"preference"|"style"|"correction","content":"one self-contained sentence"}.

Extract ONLY facts that will still matter for FUTURE work for this organization: brand voice and tone, colors and typography, product and company names, structural preferences (layouts, sections, CTAs), and corrections the user made that reveal a standing preference.

Do NOT extract: one-off or site-specific instructions, anything secret-looking (keys, tokens, passwords, private URLs), personal data, or restatements of what the assistant did. Return [] when nothing qualifies. At most ` + "10" + ` items.`

// ExtractChatLogMemories runs the extraction pass over a shared chat log's
// messages past the watermark. Called (in a goroutine) by the chat handlers
// after share/append — the path by which external agents' sessions feed
// memory.
func (r *Runner) ExtractChatLogMemories(ctx context.Context, t store.Tenant, chatLogID string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), extractTimeout)
	defer cancel()
	if r.LLM == nil || !r.memoryEnabled(ctx, t) {
		return
	}
	since, err := r.Store.MemoryIngestSeq(ctx, t, "chat_log", chatLogID)
	if err != nil {
		r.logger().Warn("memory: chat watermark read failed", "err", err, "chat", chatLogID)
		return
	}
	msgs, err := r.Store.ListChatMessages(ctx, t, chatLogID, int32(since), 500)
	if err != nil {
		r.logger().Warn("memory: chat read failed", "err", err, "chat", chatLogID)
		return
	}
	if len(msgs) == 0 {
		return
	}
	var b strings.Builder
	maxSeq := since
	for _, m := range msgs {
		if int64(m.Seq) > maxSeq {
			maxSeq = int64(m.Seq)
		}
		if m.Kind != "chat" || strings.TrimSpace(m.Content) == "" {
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n", m.Role, m.Content)
	}
	r.extractInto(ctx, t, "chat_log", chatLogID, "", b.String(), maxSeq)
}

// extractInto is the shared extraction core: one cheap LLM call over the
// transcript slice, tolerant JSON parse, embed, near-dup dedupe, upsert, then
// advance the watermark.
func (r *Runner) extractInto(ctx context.Context, t store.Tenant, sourceKind, sourceID, fallbackModel, transcript string, throughSeq int64) {
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		// Nothing extractable in the slice (tool-only turns): just advance.
		_ = r.Store.AdvanceMemoryIngest(ctx, t, sourceKind, sourceID, throughSeq)
		return
	}
	if len(transcript) > maxExtractInput {
		transcript = transcript[len(transcript)-maxExtractInput:]
	}

	model := r.MemoryExtractModel
	if model == "" {
		model = fallbackModel
	}
	if model == "" {
		r.logger().Warn("memory: no extraction model configured")
		return
	}

	messages := []openrouter.Message{
		{Role: "system", Content: extractSystemPrompt},
		{Role: "user", Content: transcript},
	}
	result, err := r.complete(ctx, model, messages)
	if err != nil {
		r.logger().Warn("memory: extraction generation failed", "err", err, "source", sourceID)
		return
	}

	candidates := parseExtractedMemories(result.Content)
	if len(candidates) > maxExtractCandidates {
		candidates = candidates[:maxExtractCandidates]
	}
	if len(candidates) == 0 {
		_ = r.Store.AdvanceMemoryIngest(ctx, t, sourceKind, sourceID, throughSeq)
		return
	}

	contents := make([]string, len(candidates))
	for i, c := range candidates {
		contents[i] = c.Content
	}
	vecs, err := r.Embedder.Embed(ctx, contents)
	if err != nil || len(vecs) != len(candidates) {
		r.logger().Warn("memory: candidate embed failed", "err", err, "source", sourceID)
		return
	}
	embModel := r.Embedder.ModelID()

	sid := sourceID
	for i, c := range candidates {
		// Semantic dedupe: a near-identical existing memory means this
		// candidate adds nothing (the exact-hash upsert path handles literal
		// repeats and refreshes updated_at). The probe covers pinned AND
		// disabled rows, so a reworded pinned fact isn't duplicated and a
		// disabled fact can't sneak back in under new wording.
		if found, dist, err := r.Store.NearestOrgMemoryDistance(ctx, t, vecs[i], embModel); err == nil && found && dist <= nearDupDistance {
			continue
		}
		_, _, err = r.Store.UpsertOrgMemory(ctx, t, store.NewMemoryInput{
			Kind:           c.Kind,
			Content:        c.Content,
			Embedding:      vecs[i],
			EmbeddingModel: embModel,
			SourceKind:     sourceKind,
			SourceID:       &sid,
		}, r.MemoryMaxPerOrg)
		if err != nil && err != store.ErrMemoryQuota {
			r.logger().Warn("memory: upsert failed", "err", err, "source", sourceID)
			return // watermark stays put; the slice re-extracts next turn
		}
	}
	if err := r.Store.AdvanceMemoryIngest(ctx, t, sourceKind, sourceID, throughSeq); err != nil {
		r.logger().Warn("memory: watermark advance failed", "err", err, "source", sourceID)
	}
}

type extractedMemory struct {
	Kind    string `json:"kind"`
	Content string `json:"content"`
}

// parseExtractedMemories tolerantly parses the model's JSON array (models
// sometimes wrap output in prose or fences despite instructions).
func parseExtractedMemories(text string) []extractedMemory {
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start < 0 || end <= start {
		return nil
	}
	var out []extractedMemory
	if err := json.Unmarshal([]byte(text[start:end+1]), &out); err != nil {
		return nil
	}
	valid := out[:0]
	for _, m := range out {
		m.Content = strings.TrimSpace(m.Content)
		switch m.Kind {
		case "fact", "preference", "style", "correction":
		default:
			m.Kind = "fact"
		}
		if m.Content == "" || len(m.Content) > 2048 {
			continue
		}
		valid = append(valid, m)
	}
	return valid
}
