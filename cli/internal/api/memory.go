// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// Memory is one org-memory entry (`dropway memory ...`).
type Memory struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	Content    string   `json:"content"`
	SourceKind string   `json:"source_kind"`
	SourceTool string   `json:"source_tool,omitempty"`
	Pinned     bool     `json:"pinned"`
	Disabled   bool     `json:"disabled"`
	UpdatedAt  string   `json:"updated_at"`
	Distance   *float64 `json:"distance,omitempty"`
}

// MemoryPatch is a partial update for PatchMemory (nil = leave as-is).
type MemoryPatch struct {
	Content  *string `json:"content,omitempty"`
	Kind     *string `json:"kind,omitempty"`
	Pinned   *bool   `json:"pinned,omitempty"`
	Disabled *bool   `json:"disabled,omitempty"`
}

// MemoryClient is the command-family interface for `dropway memory` (small,
// like SkillsClient, so the fake in tests stays small).
type MemoryClient interface {
	SearchMemories(ctx context.Context, query string, k int) ([]Memory, error)
	ListMemories(ctx context.Context, kind string, pinnedOnly bool, limit int) ([]Memory, error)
	AddMemory(ctx context.Context, content, kind string) (*Memory, bool, error)
	PatchMemory(ctx context.Context, id string, patch MemoryPatch) (*Memory, error)
	DeleteMemory(ctx context.Context, id string) error
}

type memoryListResponse struct {
	Memories []Memory `json:"memories"`
}

// SearchMemories calls POST /v1/ai/memories/search (pinned + top-k relevant).
func (c *HTTPClient) SearchMemories(ctx context.Context, query string, k int) ([]Memory, error) {
	body := map[string]any{"query": query}
	if k > 0 {
		body["k"] = k
	}
	var out memoryListResponse
	if err := c.postJSON(ctx, "/v1/ai/memories/search", body, &out); err != nil {
		return nil, err
	}
	return out.Memories, nil
}

// ListMemories calls GET /v1/ai/memories.
func (c *HTTPClient) ListMemories(ctx context.Context, kind string, pinnedOnly bool, limit int) ([]Memory, error) {
	v := url.Values{}
	if kind != "" {
		v.Set("kind", kind)
	}
	if pinnedOnly {
		v.Set("pinned", "true")
	}
	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}
	path := "/v1/ai/memories"
	if q := v.Encode(); q != "" {
		path += "?" + q
	}
	var out memoryListResponse
	if err := c.getJSON(ctx, path, &out); err != nil {
		return nil, err
	}
	return out.Memories, nil
}

// AddMemory calls POST /v1/ai/memories, stamping source_tool=cli. created is
// false when the content deduped against an existing entry.
func (c *HTTPClient) AddMemory(ctx context.Context, content, kind string) (*Memory, bool, error) {
	body := map[string]string{"content": content, "source_tool": "cli"}
	if kind != "" {
		body["kind"] = kind
	}
	var out struct {
		Memory  Memory `json:"memory"`
		Created bool   `json:"created"`
	}
	if err := c.postJSON(ctx, "/v1/ai/memories", body, &out); err != nil {
		return nil, false, err
	}
	return &out.Memory, out.Created, nil
}

// PatchMemory calls PATCH /v1/ai/memories/{id} (admin-only server-side).
func (c *HTTPClient) PatchMemory(ctx context.Context, id string, patch MemoryPatch) (*Memory, error) {
	var out struct {
		Memory Memory `json:"memory"`
	}
	if err := c.sendJSON(ctx, http.MethodPatch, "/v1/ai/memories/"+url.PathEscape(id), patch, &out); err != nil {
		return nil, err
	}
	return &out.Memory, nil
}

// DeleteMemory calls DELETE /v1/ai/memories/{id} (admin-only server-side).
func (c *HTTPClient) DeleteMemory(ctx context.Context, id string) error {
	return c.sendJSON(ctx, http.MethodDelete, "/v1/ai/memories/"+url.PathEscape(id), nil, nil)
}

// sendJSON is postJSON generalized over the HTTP method (PATCH/DELETE), added
// with the memory commands — the first CLI family with partial updates.
func (c *HTTPClient) sendJSON(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
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
		return c.statusError(method, path, resp.StatusCode, rb)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
