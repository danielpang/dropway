// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package apiclient is a thin HTTP client the MCP server uses to perform
// control-plane WRITES through the Go API — create a site, change a site's access
// mode. It forwards the user's OAuth access token (the API is configured to accept
// the MCP audience), so every write reuses the API's authz (admin re-check), quota,
// edge-route projection, revocation, and audit. The API remains the single writer
// of the edge projection; the MCP server never writes it directly.
package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client calls the Go API over HTTP.
type Client struct {
	baseURL string
	http    *http.Client
}

// New builds a Client for the API base URL (e.g. http://api:8080).
func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// Site is the subset of the API's Site response the MCP tools surface.
type Site struct {
	ID         string `json:"id"`
	Slug       string `json:"slug"`
	AccessMode string `json:"access_mode"`
	URL        string `json:"url"`
}

// Error is a non-2xx API response, carrying the status and the API's error message
// (from the {message,code} body) so the MCP tool can relay a useful reason.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("api %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("api %d", e.Status)
}

// CreateSite calls POST /v1/sites. accessMode "" lets the API inherit the org
// default; only "public"/"org_only" are valid at create (the API 400s otherwise).
func (c *Client) CreateSite(ctx context.Context, token, slug, accessMode string) (Site, error) {
	body := map[string]string{"slug": slug}
	if accessMode != "" {
		body["access_mode"] = accessMode
	}
	var site Site
	if err := c.do(ctx, http.MethodPost, "/v1/sites", token, body, &site); err != nil {
		return Site{}, err
	}
	return site, nil
}

// SetAccess calls PUT /v1/sites/{id}/access. password is only used for mode=password.
func (c *Client) SetAccess(ctx context.Context, token, siteID, mode, password string) error {
	body := map[string]string{"mode": mode}
	if password != "" {
		body["password"] = password
	}
	return c.do(ctx, http.MethodPut, "/v1/sites/"+siteID+"/access", token, body, nil)
}

// do issues a JSON request with the bearer token and decodes a 2xx JSON body into
// out (nil to ignore). Non-2xx → *Error with the API's message.
func (c *Client) do(ctx context.Context, method, path, token string, body, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := ""
		var e struct {
			Message string `json:"message"`
			Error   string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil {
			msg = e.Message
			if msg == "" {
				msg = e.Error
			}
		}
		return &Error{Status: resp.StatusCode, Message: msg}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return err
		}
	}
	return nil
}
