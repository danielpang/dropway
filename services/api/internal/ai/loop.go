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

	// 1. Persist the user message.
	if _, err := r.appendMessage(ctx, t, sess.ID, openrouter.Message{Role: "user", Content: userText}); err != nil {
		return err
	}

	// 2. Ensure a live, seeded sandbox for the session.
	sb, err := r.ensureSandbox(ctx, t, &sess)
	if err != nil {
		return fmt.Errorf("sandbox: %w", err)
	}

	// 3. Rebuild the conversation from the persisted transcript (resumable).
	history, err := r.loadHistory(ctx, t, sess.ID)
	if err != nil {
		return err
	}
	messages := append([]openrouter.Message{{Role: "system", Content: r.systemPrompt()}}, history...)

	tools := builderTools()
	settings, err := r.Store.GetAISettings(ctx, t)
	if err != nil {
		return err
	}
	// Spend before this turn started; the running per-turn total is added to it so
	// the cap is enforced immediately after each generation, not a full iteration
	// late. periodStart is read once (stable for the turn).
	priorSpent, err := r.Store.AISpendSince(ctx, t, r.periodStart(ctx, t))
	if err != nil {
		return err
	}
	var turnSpent float64

	// 4. The tool-call loop.
	for i := 0; i < r.maxIterations(); i++ {
		// Refuse the next generation if the org is already at/over its monthly cap,
		// or if this single turn has hit its own ceiling (a runaway-loop backstop
		// that applies even when there is no monthly cap, e.g. self-host unlimited).
		if err := r.checkTurnBudget(settings.MonthlyCapUSD, priorSpent, turnSpent); err != nil {
			return err
		}

		result, genErr := r.streamOnce(ctx, sess.Model, messages, tools, emitFromCtx(ctx))
		if genErr != nil {
			return genErr
		}

		// Persist the assistant message + record usage.
		if _, err := r.appendMessage(ctx, t, sess.ID, result.Message); err != nil {
			return err
		}
		if result.Usage != nil {
			turnSpent += result.Usage.Cost
			r.recordUsage(ctx, t, sess.ID, sess.Model, result.Usage)
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
			if _, err := r.appendMessage(ctx, t, sess.ID, toolMsg); err != nil {
				return err
			}
			messages = append(messages, toolMsg)
		}
	}

	// 5. Export the result and deploy it as an AI draft version + preview.
	return r.publishDraft(ctx, t, &sess, sb, previewTTL, contentURL, emitFromCtx(ctx))
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
		return err
	}
	_ = r.Store.SetAISessionLatestVersion(ctx, t, sess.ID, ver.ID)

	prev, err := r.Store.CreatePreviewRoute(ctx, t, sess.SiteID, ver.ID, previewTTL)
	if err != nil {
		return err
	}
	// Project the preview route to the edge so the URL serves. Best-effort: the
	// row is authoritative and the reconcile/rebuild path backstops a KV miss, but
	// tell the user the preview may lag so a temporary 404 isn't mysterious.
	if r.Projection != nil {
		if err := r.Projection.PutRoute(ctx, prev.Host, prev.Route); err != nil {
			emit(Event{Type: "status", Text: "The preview is taking a moment to go live. If it does not load, refresh in a few seconds."})
		}
	}
	emit(Event{
		Type:       "draft_ready",
		VersionID:  ver.ID,
		PreviewURL: contentURL(prev.Host),
		ExpiresAt:  prev.ExpiresAt.UTC().Format(time.RFC3339),
	})
	return nil
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
	if turnSpent >= r.maxTurnSpendUSD() {
		return capExceeded(r.maxTurnSpendUSD(), priorSpent+turnSpent)
	}
	if monthlyCapUSD > 0 && priorSpent+turnSpent >= monthlyCapUSD {
		return capExceeded(monthlyCapUSD, priorSpent+turnSpent)
	}
	return nil
}

// capExceeded builds the quota error the handler maps to the existing 402/cap
// pathway (the dashboard's cap/upgrade UI).
func capExceeded(capUSD, spent float64) error {
	return &quota.ExceededError{
		Limit:   quotaResourceAISpend,
		Current: int64(spent),
		Max:     int64(capUSD),
	}
}

// quotaResourceAISpend labels the AI monthly-spend cap in a quota.ExceededError
// so the 402 body distinguishes it from the site/storage caps.
const quotaResourceAISpend quota.Resource = "ai_monthly_spend_usd"

func (r *Runner) recordUsage(ctx context.Context, t store.Tenant, sessionID, model string, u *openrouter.Usage) {
	sid := sessionID
	genID := u.GenerationID
	if genID == "" {
		return // nothing to reconcile against; skip (free/unpriced)
	}
	recorded, err := r.Store.RecordAIUsage(ctx, t, store.AIUsageRow{
		SessionID:              &sid,
		Model:                  model,
		OpenrouterGenerationID: genID,
		PromptTokens:           u.PromptTokens,
		CompletionTokens:       u.CompletionTokens,
		CostUSD:                u.Cost,
	})
	// Push the pass-through cost to the billing meter (cloud). Only on a
	// genuinely-new ledger row (recorded), so a retried turn never double-bills.
	// Best-effort: a failed report leaves reported_to_billing_at NULL for the ops
	// retry sweep.
	if err == nil && recorded && r.UsageReporter != nil && u.Cost > 0 {
		_ = r.UsageReporter.ReportUsage(ctx, t.OrgID, genID, u.Cost)
	}
}

// appendMessage persists one OpenRouter message as an ai_messages row.
func (r *Runner) appendMessage(ctx context.Context, t store.Tenant, sessionID string, m openrouter.Message) (store.AIMessage, error) {
	body, err := json.Marshal(m)
	if err != nil {
		return store.AIMessage{}, err
	}
	return r.Store.AppendAIMessage(ctx, t, sessionID, m.Role, body)
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
