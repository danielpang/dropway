// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package ai implements the AI website builder's agent loop: the OpenRouter
// chat-completion + tool-call loop that runs in the Go API and drives a
// disposable sandbox. It owns the trusted side of the boundary (the OpenRouter
// key, the spend cap, the cost ledger, deploy-as-draft); the sandbox is a dumb
// exec/filesystem target holding only the user's own site files.
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"
	"unicode/utf8"

	"github.com/danielpang/dropway/internal/openrouter"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/internal/sandbox"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// Runner executes builder turns. It is constructed once in main and shared;
// per-turn state lives in the call, not the Runner.
type Runner struct {
	Store      *store.Store
	Objects    storage.Store
	LLM        *openrouter.Client
	Sandboxes  sandbox.Provider
	Projection projection.Writer

	// SystemPrompt seeds every conversation. Defaults to defaultSystemPrompt.
	SystemPrompt string
	// MaxIterations bounds the tool-call loop per turn (runaway-agent backstop).
	MaxIterations int
	// SandboxTTL is the hard machine lifetime; SandboxIdle is how long an idle
	// sandbox is reused before being recreated.
	SandboxTTL time.Duration
	// MaxTurnSpendUSD bounds how much a single turn may spend before it is
	// stopped (a runaway-loop backstop that applies even with no monthly cap).
	// 0 → defaultMaxTurnSpendUSD.
	MaxTurnSpendUSD float64
	// PeriodStart returns the start of the org's current billing period for the
	// spend-cap sum. Defaults to the start of the calendar month (self-host); the
	// cloud build injects the Stripe billing-period resolver.
	PeriodStart func(ctx context.Context, t store.Tenant) time.Time
	// UsageReporter, when set, is called best-effort after each generation's cost
	// is recorded to the ledger, so the cloud build can push a Stripe metered
	// event (pass-through cost + card fee). Nil in OSS (self-host is BYO key, no
	// pass-through billing).
	UsageReporter UsageReporter
	// Embedder enables org memory (retrieval into the turn + post-turn
	// extraction). Nil → memory off; turns run exactly as before.
	Embedder Embedder
	// MemoryExtractModel is the OpenRouter model for the post-turn extraction
	// pass (a cheap tier; empty falls back to the session model). MemoryTopK
	// bounds retrieved memories per turn; MemoryMaxPerOrg caps stored rows.
	MemoryExtractModel string
	MemoryTopK         int
	MemoryMaxPerOrg    int
	// Logger receives the async transcript writer's warnings/errors (writes
	// happen off the request path, so there is no request logger to use).
	// Nil → slog.Default().
	Logger *slog.Logger
}

// UsageReporter forwards a recorded AI generation's cost to a billing meter. The
// cloud build implements it over Stripe Billing Meters; OSS leaves it nil.
type UsageReporter interface {
	ReportUsage(ctx context.Context, orgID, generationID string, costUSD float64) error
}

func (r *Runner) systemPrompt() string {
	if r.SystemPrompt != "" {
		return r.SystemPrompt
	}
	return defaultSystemPrompt
}

func (r *Runner) logger() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}

func (r *Runner) maxIterations() int {
	if r.MaxIterations > 0 {
		return r.MaxIterations
	}
	return 50
}

func (r *Runner) periodStart(ctx context.Context, t store.Tenant) time.Time {
	if r.PeriodStart != nil {
		return r.PeriodStart(ctx, t)
	}
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

const defaultSystemPrompt = `You are Dropway's AI website builder. You edit a user's static website by running shell commands and reading and writing files in the site's working directory. The site is served as static files, so your final output must be static HTML, CSS, JavaScript, and assets at the site root (index.html is the home page). Prefer simple, self-contained static output. When you install dependencies or run a build, make sure the built static files end up at the site root. When you are done, stop calling tools and briefly summarize what you changed.`

// Event is a builder-loop event streamed to the caller (SSE). It is a superset
// of the OpenRouter stream events plus tool + draft lifecycle.
type Event struct {
	Type string `json:"type"` // token | status | tool_started | tool_finished | draft_ready | error | done
	// token
	Text string `json:"text,omitempty"`
	// tool_started / tool_finished
	Tool       string `json:"tool,omitempty"`
	ToolResult string `json:"tool_result,omitempty"`
	// draft_ready
	VersionID  string `json:"version_id,omitempty"`
	PreviewURL string `json:"preview_url,omitempty"`
	ExpiresAt  string `json:"expires_at,omitempty"`
	// AccessMode is the preview's access mode (mirrors the site's): "public",
	// "org_only", etc. The UI renders a public preview inline in an iframe but
	// must open a gated one in a NEW TAB — gated content authenticates via a
	// cross-site redirect whose cookie is blocked inside a cross-origin iframe.
	AccessMode string `json:"access_mode,omitempty"`
	// error
	Error string `json:"error,omitempty"`
}

// Emit is the sink for loop events (the handler's SSE writer).
type Emit func(Event)

// ContentURL renders a preview host into a display URL (injected from the API's
// ContentScheme/Port so the loop package needn't know them).
type ContentURL func(host string) string

// RunTurn processes one user message end to end: persist it, ensure a seeded
// sandbox, run the tool loop against OpenRouter, persist assistant + tool
// messages, record usage, then export the result as an AI draft version with a
// preview URL. Events are streamed via emit; the returned error is terminal
// (also emitted as an error event by the caller).
func (r *Runner) RunTurn(ctx context.Context, t store.Tenant, sess store.AISession, userText string, previewTTL time.Duration, contentURL ContentURL) error {
	// The caller (the handler) has already claimed the session for this turn
	// (status 'running') via TryBeginAITurn, which serializes writers so the
	// per-session seq allocation below is race-free. We only release the claim
	// (back to 'active') when the turn ends. RunTurn uses a detached context for
	// the release so a cancelled/timed-out turn still frees the session.
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = r.Store.SetAISessionStatus(releaseCtx, t, sess.ID, "active")
	}()

	// 1. Rebuild the conversation from the persisted transcript (resumable).
	// This happens BEFORE the user message is enqueued below, so the in-memory
	// list is exactly history + this turn's messages regardless of when the
	// async writes land. sanitizeHistory is a safety net: it drops any message
	// run that would make the provider reject the conversation (an assistant
	// tool_calls message with missing results, an orphan tool message), so one
	// historically-corrupt row can't permanently brick the session.
	history, err := r.loadHistory(ctx, t, sess.ID)
	if err != nil {
		return err
	}
	history = sanitizeHistory(history)

	// 2. Start the async transcript writer and enqueue the user message.
	// Persistence never blocks the model stream: the writer drains in the
	// background while tokens flow. Its failure semantics are fail-stop — on the
	// first permanently-failed write it cancels the turn (cancel below), the
	// backlog is discarded so the persisted transcript stays a clean prefix, and
	// the user is asked to send the message again. The Flush before publishing
	// and the deferred Close (LIFO, before the claim release above) guarantee
	// the claim is never released while writes are still in flight.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	tw := r.newTranscriptWriter(ctx, t, sess.ID, cancel)
	defer tw.Close()
	userMsg := openrouter.Message{Role: "user", Content: userText}
	tw.Append(userMsg)

	// persistFailed maps any error to the transcript failure when the writer is
	// what actually stopped the turn (the cancel above surfaces as a generic
	// context.Canceled inside the step that was running).
	persistFailed := func(err error) error {
		if terr := tw.Err(); terr != nil {
			return fmt.Errorf("%w: %w", ErrTranscriptPersist, terr)
		}
		return err
	}

	// 3. Ensure a live, seeded sandbox for the session.
	sb, err := r.ensureSandbox(ctx, t, &sess)
	if err != nil {
		return persistFailed(fmt.Errorf("sandbox: %w", err))
	}

	// Org memory: retrieve the relevant company context and append it to the
	// system prompt for THIS turn only. The block is never persisted into the
	// transcript, so it stays current as memory evolves and history rebuilds
	// are unaffected. Fail-open: an empty block means the turn runs memory-less.
	sysPrompt := r.systemPrompt()
	if block := r.memoryBlock(ctx, t, userText); block != "" {
		sysPrompt += "\n\n" + block
	}
	messages := append([]openrouter.Message{{Role: "system", Content: sysPrompt}}, history...)
	messages = append(messages, userMsg)

	tools := builderTools()
	settings, err := r.Store.GetAISettings(ctx, t)
	if err != nil {
		return persistFailed(err)
	}
	// Spend before this turn started; the running per-turn total is added to it so
	// the cap is enforced immediately after each generation, not a full iteration
	// late. periodStart is read once (stable for the turn).
	priorSpent, err := r.Store.AISpendSince(ctx, t, r.periodStart(ctx, t))
	if err != nil {
		return persistFailed(err)
	}
	var turnSpent float64

	// 4. The tool-call loop.
	for i := 0; i < r.maxIterations(); i++ {
		// Stop before the next generation if persistence already failed: nothing
		// generated past this point could be saved, so it is wasted spend.
		if terr := tw.Err(); terr != nil {
			return persistFailed(nil)
		}
		// Refuse the next generation if the org is already at/over its monthly cap,
		// or if this single turn has hit its own ceiling (a runaway-loop backstop
		// that applies even when there is no monthly cap, e.g. self-host unlimited).
		if err := r.checkTurnBudget(settings.MonthlyCapUSD, priorSpent, turnSpent); err != nil {
			return err
		}

		result, genErr := r.streamOnce(ctx, sess.Model, messages, tools, emitFromCtx(ctx))
		if genErr != nil {
			return persistFailed(genErr)
		}

		// Enqueue the assistant message for async persistence + record usage.
		tw.Append(result.Message)
		if result.Usage != nil {
			// Only count the cost toward the per-turn/cap total when the ledger
			// GENUINELY recorded it (recordUsage dedupes on the generation id), so a
			// replayed generation can't inflate turnSpent and trip the cap early.
			if r.recordUsage(ctx, t, sess.ID, sess.Model, result.Usage) {
				turnSpent += result.Usage.Cost
			}
		}
		messages = append(messages, result.Message)

		if len(result.Message.ToolCalls) == 0 {
			break // the model is done: no more tool calls.
		}

		// Dispatch each tool call against the sandbox, persist + feed back results.
		for _, call := range result.Message.ToolCalls {
			emit := emitFromCtx(ctx)
			emit(Event{Type: "tool_started", Tool: call.Function.Name})
			out := dispatchTool(ctx, sb, call)
			emit(Event{Type: "tool_finished", Tool: call.Function.Name, ToolResult: truncate(out, 4000)})

			toolMsg := openrouter.Message{Role: "tool", ToolCallID: call.ID, Name: call.Function.Name, Content: out}
			tw.Append(toolMsg)
			messages = append(messages, toolMsg)
		}
	}

	// 5. The transcript must be durable before the turn can succeed: wait for
	// the writer's backlog (near-instant when the database is healthy, since it
	// drained concurrently with the generations above) and stop here if it
	// failed, rather than publishing a draft for a conversation that was lost.
	if err := tw.Flush(); err != nil {
		return persistFailed(nil)
	}

	// 5b. Post-turn memory extraction, off the request path on a detached
	// context (the turn's outcome never depends on it; failures only log and
	// the watermark makes the next turn retry).
	if r.memoryEnabled(ctx, t) {
		extractCtx, cancelExtract := context.WithTimeout(context.WithoutCancel(ctx), extractTimeout)
		go func() {
			defer cancelExtract()
			r.extractSessionMemories(extractCtx, t, sess)
		}()
	}

	// 6. Export the result and deploy it as an AI draft version + preview.
	return r.publishDraft(ctx, t, &sess, sb, previewTTL, contentURL, emitFromCtx(ctx))
}

// ErrTranscriptPersist marks a turn stopped because its transcript could not be
// written to the database. The handler maps it to a user-facing "please try
// sending your message again"; the persisted transcript stays a clean prefix of
// the conversation (nothing after the failed write is stored).
var ErrTranscriptPersist = errors.New("ai: transcript persistence failed")

// sanitizeHistory drops message runs that would make the provider reject the
// conversation: an assistant tool_calls message whose tool results are missing
// (in part or full), and any tool message with no parent assistant tool_calls.
// The transcript writer's fail-stop semantics should make such rows impossible
// going forward; this is the load-time safety net so one bad historical row can
// never permanently brick a session with provider 400s.
func sanitizeHistory(msgs []openrouter.Message) []openrouter.Message {
	out := make([]openrouter.Message, 0, len(msgs))
	for i := 0; i < len(msgs); {
		m := msgs[i]
		switch {
		case m.Role == "tool":
			i++ // orphan tool result (its assistant message was lost): drop
		case m.Role == "assistant" && len(m.ToolCalls) > 0:
			// Keep the block only when every tool call has its result among the
			// tool messages that follow.
			want := make(map[string]bool, len(m.ToolCalls))
			for _, c := range m.ToolCalls {
				want[c.ID] = true
			}
			block := []openrouter.Message{m}
			j := i + 1
			for ; j < len(msgs) && msgs[j].Role == "tool"; j++ {
				if want[msgs[j].ToolCallID] {
					delete(want, msgs[j].ToolCallID)
					block = append(block, msgs[j])
				}
			}
			if len(want) == 0 {
				out = append(out, block...)
			}
			i = j
		default:
			out = append(out, m)
			i++
		}
	}
	return out
}

// streamOnce runs one OpenRouter generation, forwarding token deltas to emit and
// returning the terminal done event (assembled message + usage).
func (r *Runner) streamOnce(ctx context.Context, model string, messages []openrouter.Message, tools []openrouter.Tool, emit Emit) (openrouter.Event, error) {
	ch, err := r.LLM.ChatStream(ctx, openrouter.ChatRequest{Model: model, Messages: messages, Tools: tools})
	if err != nil {
		return openrouter.Event{}, err
	}
	for ev := range ch {
		switch ev.Type {
		case openrouter.EventDelta:
			emit(Event{Type: "token", Text: ev.Delta})
		case openrouter.EventDone:
			return ev, nil
		case openrouter.EventError:
			return openrouter.Event{}, ev.Err
		}
	}
	return openrouter.Event{}, errors.New("ai: stream ended without a terminal event")
}

// publishDraft exports the sandbox tree, ingests it as an AI draft version, and
// registers a preview route, emitting draft_ready.
func (r *Runner) publishDraft(ctx context.Context, t store.Tenant, sess *store.AISession, sb sandbox.Sandbox, previewTTL time.Duration, contentURL ContentURL, emit Emit) error {
	tarRC, err := sb.ExportTar(ctx, sandbox.DefaultWorkdir)
	if err != nil {
		return fmt.Errorf("export: %w", err)
	}
	defer tarRC.Close()

	ver, err := ingestTar(ctx, r.Objects, r.Store, t, sess.SiteID, tarRC)
	if err != nil {
		return err // the draft was never created → a legitimate turn failure
	}
	_ = r.Store.SetAISessionLatestVersion(ctx, t, sess.ID, ver.ID)

	// From here the draft version IS committed (and the turn's generations are
	// already billed), so a preview-registration failure must NOT fail the turn.
	// We always emit draft_ready; the preview URL is the authoritative host from
	// the row when it succeeds, else the deterministic fallback host.
	var previewURL, expiresAt, accessMode string
	if prev, perr := r.Store.CreatePreviewRoute(ctx, t, sess.SiteID, ver.ID, previewTTL); perr != nil {
		emit(Event{Type: "status", Text: "Your changes are saved as a draft. The preview link is taking a moment to activate."})
		if url, mode, ok := r.draftPreviewURL(ctx, t, sess.SiteID, ver.ID, contentURL); ok {
			previewURL = url
			accessMode = mode
			expiresAt = time.Now().UTC().Add(previewTTL).Format(time.RFC3339)
		}
	} else {
		previewURL = contentURL(prev.Host)
		expiresAt = prev.ExpiresAt.UTC().Format(time.RFC3339)
		accessMode = prev.Route.AccessMode
		// Project the preview route to the edge so the URL serves. Best-effort: the
		// row is authoritative and the reconcile/rebuild path backstops a KV miss;
		// tell the user the preview may lag so a temporary 404 isn't mysterious.
		if r.Projection != nil {
			if err := r.Projection.PutRoute(ctx, prev.Host, prev.Route); err != nil {
				emit(Event{Type: "status", Text: "The preview is taking a moment to go live. If it does not load, refresh in a few seconds."})
			}
		}
		// Keep at most one live preview per site: drop the earlier drafts' preview
		// hosts (and their KV keys). Best-effort — a failure just leaves the old
		// previews to expire on their TTL, so it never fails the turn.
		if stale, derr := r.Store.DeleteOtherSitePreviewRoutes(ctx, t, sess.SiteID, ver.ID); derr == nil && r.Projection != nil {
			for _, host := range stale {
				_ = r.Projection.DeleteRoute(ctx, host)
			}
		}
	}
	emit(Event{
		Type:       "draft_ready",
		VersionID:  ver.ID,
		PreviewURL: previewURL,
		ExpiresAt:  expiresAt,
		AccessMode: accessMode,
	})
	return nil
}

// draftPreviewURL computes the DETERMINISTIC preview URL for a draft version
// (org slug + site slug + version id), used as a fallback when the preview-route
// row couldn't be written. It also returns the site's access mode so the UI can
// choose inline-iframe (public) vs open-in-new-tab (gated). ok is false if the
// org slug or site can't be read.
func (r *Runner) draftPreviewURL(ctx context.Context, t store.Tenant, siteID, versionID string, contentURL ContentURL) (url, accessMode string, ok bool) {
	orgSlug, err := r.Store.OrgSlug(ctx, t)
	if err != nil {
		return "", "", false
	}
	site, err := r.Store.GetSite(ctx, t, siteID)
	if err != nil {
		return "", "", false
	}
	return contentURL(projection.PreviewHostForSite(versionID, orgSlug, site.Slug)), site.AccessMode, true
}

// defaultMaxTurnSpendUSD bounds how much a single turn may spend before it is
// stopped, so a runaway tool loop can't rack up unbounded cost even when there
// is no monthly cap. Overridable via Runner.MaxTurnSpendUSD.
const defaultMaxTurnSpendUSD = 5.0

func (r *Runner) maxTurnSpendUSD() float64 {
	if r.MaxTurnSpendUSD > 0 {
		return r.MaxTurnSpendUSD
	}
	return defaultMaxTurnSpendUSD
}

// checkTurnBudget refuses the next generation when the org has hit its monthly
// cap (priorSpent + this turn's spend so far) or when this single turn has hit
// its per-turn ceiling. Pure (no DB read per generation): priorSpent is read
// once at the top of the turn and turnSpent accumulates the recorded costs.
func (r *Runner) checkTurnBudget(monthlyCapUSD, priorSpent, turnSpent float64) error {
	// The per-turn ceiling is a non-configurable runaway backstop; the monthly cap
	// is the org's own setting. They carry DIFFERENT labels so the handler can tell
	// the user which one tripped (only the monthly one is raisable in settings).
	if turnSpent >= r.maxTurnSpendUSD() {
		return capExceeded(quotaResourceAITurnSpend, r.maxTurnSpendUSD(), priorSpent+turnSpent)
	}
	if monthlyCapUSD > 0 && priorSpent+turnSpent >= monthlyCapUSD {
		return capExceeded(quotaResourceAISpend, monthlyCapUSD, priorSpent+turnSpent)
	}
	return nil
}

// capExceeded builds the quota error the handler maps to the existing 402/cap
// pathway. The USD amounts are carried in CENTS (Current/Max) so a sub-dollar cap
// (e.g. $0.50) is preserved rather than truncated to 0 by the int64 fields.
func capExceeded(limit quota.Resource, capUSD, spent float64) error {
	return &quota.ExceededError{
		Limit:   limit,
		Current: int64(math.Round(spent * 100)),
		Max:     int64(math.Round(capUSD * 100)),
	}
}

// quotaResourceAISpend labels the org's monthly AI-spend cap (raisable in
// settings); quotaResourceAITurnSpend labels the non-configurable per-turn
// runaway backstop. The handler maps each to a different user message.
const (
	quotaResourceAISpend     quota.Resource = "ai_monthly_spend_usd"
	quotaResourceAITurnSpend quota.Resource = "ai_turn_spend_usd"
)

// AITurnSpendLimit is exported so the handler (a different package) can tell the
// per-turn backstop apart from the monthly cap when shaping the error message.
const AITurnSpendLimit = quotaResourceAITurnSpend

// recordUsage writes one generation to the cost ledger and (cloud) meters it.
// It returns true only when the ledger GENUINELY recorded a NEW row (dedup on
// the generation id), so the caller counts the cost toward the cap exactly once.
func (r *Runner) recordUsage(ctx context.Context, t store.Tenant, sessionID, model string, u *openrouter.Usage) (recorded bool) {
	// An empty session id (e.g. chat-log memory extraction) books the cost to
	// the org with no session attribution — the column is nullable for exactly
	// this (billing rows outlive conversations).
	var sessPtr *string
	if sessionID != "" {
		sessPtr = &sessionID
	}
	genID := u.GenerationID
	if genID == "" {
		return false // nothing to reconcile against; skip (free/unpriced)
	}
	recorded, err := r.Store.RecordAIUsage(ctx, t, store.AIUsageRow{
		SessionID:              sessPtr,
		Model:                  model,
		OpenrouterGenerationID: genID,
		PromptTokens:           u.PromptTokens,
		CompletionTokens:       u.CompletionTokens,
		CostUSD:                u.Cost,
	})
	if err != nil {
		return false
	}
	// Push the pass-through cost to the billing meter (cloud). Only on a
	// genuinely-new ledger row (recorded), so a retried turn never double-bills.
	// Best-effort: a failed report leaves reported_to_billing_at NULL for the ops
	// retry sweep.
	if recorded && r.UsageReporter != nil && u.Cost > 0 {
		_ = r.UsageReporter.ReportUsage(ctx, t.OrgID, genID, u.Cost)
	}
	return recorded
}

// loadHistory rebuilds the OpenRouter message list from the persisted transcript.
func (r *Runner) loadHistory(ctx context.Context, t store.Tenant, sessionID string) ([]openrouter.Message, error) {
	rows, err := r.Store.ListAIMessages(ctx, t, sessionID, 0)
	if err != nil {
		return nil, err
	}
	out := make([]openrouter.Message, 0, len(rows))
	for _, row := range rows {
		var m openrouter.Message
		if err := json.Unmarshal(row.Content, &m); err != nil {
			return nil, fmt.Errorf("ai: decode message %s: %w", row.ID, err)
		}
		out = append(out, m)
	}
	return out, nil
}

// truncate caps s at n bytes, backing up to a UTF-8 rune boundary so it never
// splits a multi-byte rune (which would emit invalid UTF-8 that json.Marshal
// then replaces with U+FFFD in the streamed tool_result).
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// emitCtxKey carries the per-turn Emit sink through ctx so streamOnce and the
// tool loop reach it without threading it through every signature.
type emitCtxKey struct{}

// WithEmit attaches an Emit sink to ctx for the duration of a turn.
func WithEmit(ctx context.Context, emit Emit) context.Context {
	return context.WithValue(ctx, emitCtxKey{}, emit)
}

// EmitFromContext returns the Emit sink attached by WithEmit, or a no-op sink.
// Exported so a test runner can emit events the same way RunTurn does.
func EmitFromContext(ctx context.Context) Emit { return emitFromCtx(ctx) }

func emitFromCtx(ctx context.Context) Emit {
	if e, ok := ctx.Value(emitCtxKey{}).(Emit); ok && e != nil {
		return e
	}
	return func(Event) {}
}
