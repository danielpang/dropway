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

// Memory tuning defaults; overridable via the Runner fields.
const (
	defaultMemoryTopK = 8
	// retrieveTimeout bounds the whole retrieval step. Retrieval must never
	// fail or stall a turn: on timeout/error the turn proceeds memory-less.
	retrieveTimeout = 2 * time.Second
	// memoryBlockBudget caps the injected block (~1,500 tokens at 4 B/token).
	memoryBlockBudget = 6000
	// maxDistance is the cosine-distance floor: matches farther than this are
	// noise, not memory (distance = 1 - similarity).
	maxDistance = 0.7
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

func (r *Runner) memoryTopK() int32 {
	if r.MemoryTopK > 0 {
		return int32(r.MemoryTopK)
	}
	return defaultMemoryTopK
}

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

// memoryBlock retrieves the org's relevant memory for a user message and
// renders the <company_memory> context block. Fail-open by design: any error
// or timeout returns "" and the turn proceeds without memory. The injected
// block is per-turn context and is never persisted into the transcript.
func (r *Runner) memoryBlock(ctx context.Context, t store.Tenant, userText string) string {
	if !r.memoryEnabled(ctx, t) {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, retrieveTimeout)
	defer cancel()

	vecs, err := r.Embedder.Embed(ctx, []string{userText})
	if err != nil || len(vecs) != 1 {
		r.logger().Warn("memory: query embed failed; proceeding memory-less", "err", err)
		return ""
	}
	model := r.Embedder.ModelID()

	pinned, err := r.Store.ListPinnedOrgMemories(ctx, t, 20)
	if err != nil {
		r.logger().Warn("memory: pinned load failed", "err", err)
	}
	found, err := r.Store.SearchOrgMemories(ctx, t, vecs[0], model, r.memoryTopK())
	if err != nil {
		r.logger().Warn("memory: search failed", "err", err)
	}
	chunks, err := r.Store.SearchContentChunks(ctx, t, vecs[0], model, r.memoryTopK()/2+1)
	if err != nil {
		r.logger().Warn("memory: chunk search failed", "err", err)
	}

	var b strings.Builder
	var usedIDs []string
	writeMemory := func(m store.OrgMemory) {
		line := fmt.Sprintf("- [%s] %s\n", m.Kind, m.Content)
		if b.Len()+len(line) > memoryBlockBudget {
			return
		}
		b.WriteString(line)
		usedIDs = append(usedIDs, m.ID)
	}

	b.WriteString("<company_memory>\n")
	b.WriteString("Facts and preferences Dropway has learned about this organization. Follow them unless the user says otherwise.\n")
	for _, m := range pinned {
		writeMemory(m)
	}
	for _, m := range found {
		if m.Distance > maxDistance {
			continue
		}
		writeMemory(m)
	}

	wroteChunkHeader := false
	for _, c := range chunks {
		if c.Distance > maxDistance {
			continue
		}
		src := c.SiteSlug
		kind := "site"
		if c.SourceKind == "skill" {
			src = c.SkillSlug
			kind = "skill"
		}
		line := fmt.Sprintf("- (%s %s %s) %s\n", kind, src, c.Path, compactWhitespace(c.Content))
		header := ""
		if !wroteChunkHeader {
			header = "Relevant excerpts from this organization's existing sites and skills:\n"
		}
		if b.Len()+len(header)+len(line) > memoryBlockBudget {
			break
		}
		b.WriteString(header)
		wroteChunkHeader = true
		b.WriteString(line)
	}
	b.WriteString("</company_memory>")

	if len(usedIDs) == 0 && !wroteChunkHeader {
		return "" // nothing relevant — don't inject an empty scaffold
	}
	// Stamp last_used_at outside the turn's critical path.
	go func() {
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = r.Store.TouchOrgMemoriesUsed(bg, t, usedIDs)
	}()
	return b.String()
}

// --------------------------------------------------------------------------
// Extraction — the async post-turn pass that distills durable facts.
// --------------------------------------------------------------------------

const extractSystemPrompt = `You extract durable organization-level memory from a conversation between a user and a website-building assistant. Return ONLY a JSON array (no prose, no code fences). Each element: {"kind":"fact"|"preference"|"style"|"correction","content":"one self-contained sentence"}.

Extract ONLY facts that will still matter for FUTURE website builds for this organization: brand voice and tone, colors and typography, product and company names, structural preferences (layouts, sections, CTAs), and corrections the user made that reveal a standing preference.

Do NOT extract: one-off or site-specific instructions, anything secret-looking (keys, tokens, passwords, private URLs), personal data, or restatements of what the assistant did. Return [] when nothing qualifies. At most ` + "10" + ` items.`

// extractSessionMemories is the post-turn extraction pass for a builder
// session. It runs in a goroutine on a detached context: failures only log,
// and the unmoved watermark makes the next turn retry the same slice.
func (r *Runner) extractSessionMemories(ctx context.Context, t store.Tenant, sess store.AISession) {
	if !r.memoryEnabled(ctx, t) {
		return
	}
	since, err := r.Store.MemoryIngestSeq(ctx, t, "ai_session", sess.ID)
	if err != nil {
		r.logger().Warn("memory: watermark read failed", "err", err, "session", sess.ID)
		return
	}
	rows, err := r.Store.ListAIMessages(ctx, t, sess.ID, int32(since))
	if err != nil {
		r.logger().Warn("memory: transcript read failed", "err", err, "session", sess.ID)
		return
	}
	if len(rows) == 0 {
		return
	}
	var b strings.Builder
	maxSeq := since
	for _, row := range rows {
		if int64(row.Seq) > maxSeq {
			maxSeq = int64(row.Seq)
		}
		var m openrouter.Message
		if err := json.Unmarshal(row.Content, &m); err != nil {
			continue
		}
		// Tool traffic carries no durable org facts; users and the model's
		// prose replies do.
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n", m.Role, m.Content)
	}
	sessID := sess.ID
	r.extractInto(ctx, t, "ai_session", sess.ID, &sessID, sess.Model, b.String(), maxSeq)
}

// ExtractChatLogMemories runs the extraction pass over a shared chat log's
// messages past the watermark. Called (in a goroutine) by the chat handlers
// after share/append — the path by which external agents' sessions feed
// memory.
func (r *Runner) ExtractChatLogMemories(ctx context.Context, t store.Tenant, chatLogID string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), extractTimeout)
	defer cancel()
	if !r.memoryEnabled(ctx, t) {
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
	r.extractInto(ctx, t, "chat_log", chatLogID, nil, "", b.String(), maxSeq)
}

// extractInto is the shared extraction core: one cheap LLM call over the
// transcript slice, tolerant JSON parse, embed, near-dup dedupe, upsert, then
// advance the watermark. usageSession, when set, attributes the generation's
// cost to that session in the ai_usage ledger.
func (r *Runner) extractInto(ctx context.Context, t store.Tenant, sourceKind, sourceID string, usageSession *string, fallbackModel, transcript string, throughSeq int64) {
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
	// streamOnce with no Emit on ctx → token deltas go nowhere; we only need
	// the terminal message + usage.
	result, err := r.streamOnce(ctx, model, messages, nil, emitFromCtx(context.Background()))
	if err != nil {
		r.logger().Warn("memory: extraction generation failed", "err", err, "source", sourceID)
		return
	}
	if result.Usage != nil {
		sid := ""
		if usageSession != nil {
			sid = *usageSession
		}
		r.recordUsage(ctx, t, sid, model, result.Usage)
	}

	candidates := parseExtractedMemories(result.Message.Content)
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
		// repeats and refreshes updated_at).
		near, err := r.Store.SearchOrgMemories(ctx, t, vecs[i], embModel, 1)
		if err == nil && len(near) > 0 && near[0].Distance <= nearDupDistance {
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

func compactWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
