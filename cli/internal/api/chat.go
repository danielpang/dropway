// Chat client: the shared chat-log surface ("Share This Session"). Same
// conventions as api.go — wire structs mirroring the server's handlers, a
// per-command-family interface so tests inject a small fake, and JSON helpers
// on HTTPClient. This file adds the putJSON/deleteJSON verbs (and the shared
// doJSON core they and the chat calls ride on), plus typed 402 handling: a
// quota-cap response decodes into *QuotaError so commands can print the
// upgrade path instead of a raw status line.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ChatLog is the API's chat-log representation (subset the CLI needs).
type ChatLog struct {
	ID    string `json:"id"`
	OrgID string `json:"org_id"`
	// SiteID is the attached site (nil = unattached library entry).
	SiteID       *string   `json:"site_id,omitempty"`
	Title        string    `json:"title"`
	SourceTool   string    `json:"source_tool"`
	PanelEnabled bool      `json:"panel_enabled"`
	MessageCount int64     `json:"message_count"`
	CreatedBy    string    `json:"created_by"`
	CreatedAt    time.Time `json:"created_at"`
}

// ChatMessage is one stored chat-log entry.
type ChatMessage struct {
	Seq     int32  `json:"seq"`
	Role    string `json:"role"`
	Kind    string `json:"kind"`
	Content string `json:"content"`
	// Meta is the raw action metadata of a kind="action" row.
	Meta json.RawMessage `json:"meta,omitempty"`
	// VersionID stamps the deploy version current at append time.
	VersionID *string   `json:"version_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// ChatActionMeta mirrors chatspec.ActionMeta on the wire: the structured half
// of a kind="action" message.
type ChatActionMeta struct {
	Action string   `json:"action"` // "tool_use" | "file_edit"
	Tool   string   `json:"tool,omitempty"`
	Paths  []string `json:"paths,omitempty"`
}

// ChatMessageInput is one explicit message in a create/append request.
type ChatMessageInput struct {
	Kind    string          `json:"kind,omitempty"`
	Role    string          `json:"role,omitempty"`
	Content string          `json:"content,omitempty"`
	Meta    *ChatActionMeta `json:"meta,omitempty"`
}

// ChatImport is the shared ingest payload: an inline raw export (normalized
// server-side) and/or explicit canonical messages, appended in that order.
type ChatImport struct {
	Transcript    string             `json:"transcript,omitempty"`
	Format        string             `json:"format,omitempty"` // "auto" default
	DeriveActions bool               `json:"derive_actions,omitempty"`
	Messages      []ChatMessageInput `json:"messages,omitempty"`
}

// CreateChatLogRequest is the POST /v1/chats body: log metadata plus an
// optional inline import (the embedded ChatImport fields inline on the wire).
type CreateChatLogRequest struct {
	Title      string  `json:"title,omitempty"`
	SourceTool string  `json:"source_tool,omitempty"`
	SiteID     *string `json:"site_id,omitempty"`
	ChatImport
}

// CreateChatLogResponse is the POST /v1/chats body: the created log plus the
// import accounting (what was appended, tier-pruned, and import-bound-dropped).
type CreateChatLogResponse struct {
	ChatLog  ChatLog `json:"chat_log"`
	Appended int     `json:"appended"`
	Pruned   int     `json:"pruned"`
	Window   int     `json:"window"`
	Dropped  int     `json:"dropped"`
}

// ChatLogsResponse is the GET /v1/chats body.
type ChatLogsResponse struct {
	ChatLogs []ChatLog `json:"chat_logs"`
}

// ChatMessagesResponse is the GET /v1/chats/{id}/messages body.
type ChatMessagesResponse struct {
	Messages []ChatMessage `json:"messages"`
}

// ChatAppendResponse is the POST append body (log- or site-scoped): the rows
// appended plus the same trim accounting as create.
type ChatAppendResponse struct {
	Messages []ChatMessage `json:"messages"`
	Pruned   int           `json:"pruned"`
	Window   int           `json:"window"`
	Dropped  int           `json:"dropped"`
}

// QuotaError is the HTTP 402 body: a plan cap was crossed. It implements
// error with a friendly upgrade message so commands can surface it verbatim.
type QuotaError struct {
	Limit      string `json:"limit"`
	Current    int64  `json:"current"`
	Max        int64  `json:"max"`
	PlanTier   string `json:"plan_tier"`
	NextTier   string `json:"next_tier,omitempty"`
	UpgradeURL string `json:"upgrade_url,omitempty"`
}

func (e *QuotaError) Error() string {
	msg := fmt.Sprintf("quota exceeded (%s: %d/%d on the %s plan)", e.Limit, e.Current, e.Max, e.PlanTier)
	switch {
	case e.UpgradeURL != "":
		msg += ": upgrade at " + e.UpgradeURL
	case e.NextTier != "":
		msg += ": upgrade to the " + e.NextTier + " plan"
	}
	return msg
}

// ChatClient is the control-plane surface the `chat` commands need. Separate
// from Client/ReadClient (mirroring the per-command-family split) so the chat
// fakes stay small. ListSites is included because the commands resolve a
// site SLUG to its id (HTTPClient already implements it for `sites`).
type ChatClient interface {
	CreateChatLog(ctx context.Context, req CreateChatLogRequest) (*CreateChatLogResponse, error)
	ListChatLogs(ctx context.Context) (*ChatLogsResponse, error)
	GetChatLog(ctx context.Context, id string) (*ChatLog, error)
	ListChatMessages(ctx context.Context, id string, afterSeq, limit int) (*ChatMessagesResponse, error)
	AppendChatMessages(ctx context.Context, id string, req ChatImport) (*ChatAppendResponse, error)
	AppendSiteChat(ctx context.Context, siteID string, req ChatImport) (*ChatAppendResponse, error)
	SetChatLogSite(ctx context.Context, id string, siteID *string) (*ChatLog, error)
	SetChatLogPanel(ctx context.Context, id string, enabled bool) (*ChatLog, error)
	DeleteChatLog(ctx context.Context, id string) error
	DeleteChatMessage(ctx context.Context, id string, seq int32) error
	ListSites(ctx context.Context) (*SitesResponse, error)
}

// doJSON is the shared verb core: send body (nil = no body) as JSON with the
// Bearer token and decode the response into out (nil = ignore). A 402 decodes
// into *QuotaError; other non-2xx statuses keep postJSON/getJSON's error shape.
func (c *HTTPClient) doJSON(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		if resp.StatusCode == http.StatusPaymentRequired {
			var qe QuotaError
			if json.Unmarshal(rb, &qe) == nil && qe.Limit != "" {
				return &qe
			}
		}
		return fmt.Errorf("%s %s: server returned %d: %s", method, path, resp.StatusCode, bytes.TrimSpace(rb))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// putJSON PUTs body as JSON to the API path (the update counterpart to postJSON).
func (c *HTTPClient) putJSON(ctx context.Context, path string, body, out any) error {
	return c.doJSON(ctx, http.MethodPut, path, body, out)
}

// deleteJSON DELETEs the API path (responses are 204 — no body to decode).
func (c *HTTPClient) deleteJSON(ctx context.Context, path string) error {
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

// CreateChatLog creates a log, optionally attached to a site and/or seeded
// with an inline import.
func (c *HTTPClient) CreateChatLog(ctx context.Context, req CreateChatLogRequest) (*CreateChatLogResponse, error) {
	var out CreateChatLogResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/chats", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListChatLogs returns the org's chat library.
func (c *HTTPClient) ListChatLogs(ctx context.Context) (*ChatLogsResponse, error) {
	var out ChatLogsResponse
	if err := c.doJSON(ctx, http.MethodGet, "/v1/chats", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetChatLog returns one log.
func (c *HTTPClient) GetChatLog(ctx context.Context, id string) (*ChatLog, error) {
	var out struct {
		ChatLog ChatLog `json:"chat_log"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/v1/chats/"+id, nil, &out); err != nil {
		return nil, err
	}
	return &out.ChatLog, nil
}

// ListChatMessages pages a log's messages forward (afterSeq/limit 0 = all).
func (c *HTTPClient) ListChatMessages(ctx context.Context, id string, afterSeq, limit int) (*ChatMessagesResponse, error) {
	v := url.Values{}
	if afterSeq > 0 {
		v.Set("after_seq", strconv.Itoa(afterSeq))
	}
	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}
	path := "/v1/chats/" + id + "/messages"
	if enc := v.Encode(); enc != "" {
		path += "?" + enc
	}
	var out ChatMessagesResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AppendChatMessages appends turns/annotations (or a normalized import) to a log.
func (c *HTTPClient) AppendChatMessages(ctx context.Context, id string, req ChatImport) (*ChatAppendResponse, error) {
	var out ChatAppendResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/chats/"+id+"/messages", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AppendSiteChat appends to a site's attached log, creating one if absent.
func (c *HTTPClient) AppendSiteChat(ctx context.Context, siteID string, req ChatImport) (*ChatAppendResponse, error) {
	var out ChatAppendResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/sites/"+siteID+"/chat", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetChatLogSite attaches (siteID set) or detaches (nil) a log's site binding.
func (c *HTTPClient) SetChatLogSite(ctx context.Context, id string, siteID *string) (*ChatLog, error) {
	req := struct {
		SiteID *string `json:"site_id"`
	}{SiteID: siteID}
	var out struct {
		ChatLog ChatLog `json:"chat_log"`
	}
	if err := c.putJSON(ctx, "/v1/chats/"+id+"/site", req, &out); err != nil {
		return nil, err
	}
	return &out.ChatLog, nil
}

// SetChatLogPanel flips the served-panel flag.
func (c *HTTPClient) SetChatLogPanel(ctx context.Context, id string, enabled bool) (*ChatLog, error) {
	req := struct {
		Enabled bool `json:"enabled"`
	}{Enabled: enabled}
	var out struct {
		ChatLog ChatLog `json:"chat_log"`
	}
	if err := c.putJSON(ctx, "/v1/chats/"+id+"/panel", req, &out); err != nil {
		return nil, err
	}
	return &out.ChatLog, nil
}

// DeleteChatLog removes a log and its messages.
func (c *HTTPClient) DeleteChatLog(ctx context.Context, id string) error {
	return c.deleteJSON(ctx, "/v1/chats/"+id)
}

// DeleteChatMessage removes one message by seq (mistakes, pasted secrets).
func (c *HTTPClient) DeleteChatMessage(ctx context.Context, id string, seq int32) error {
	return c.deleteJSON(ctx, "/v1/chats/"+id+"/messages/"+strconv.Itoa(int(seq)))
}
