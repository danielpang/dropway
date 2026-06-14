// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package projection

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// CloudflareKV is a Writer backed by the Cloudflare Workers KV REST API. It is
// the production projection writer: the Go API PUTs/DELETEs the route value at
// `route:<host>` and the serving Worker reads it (read-only) via its KV binding.
//
// Config comes from the environment (the deploy agent's .env.example):
//
//	CF_ACCOUNT_ID, CF_KV_NAMESPACE_ID, CF_API_TOKEN.
type CloudflareKV struct {
	AccountID   string
	NamespaceID string
	APIToken    string
	HTTP        *http.Client // nil → a 10s-timeout client
	BaseURL     string       // nil/"" → https://api.cloudflare.com/client/v4 (overridable for tests)
}

// NewCloudflareKV builds a CloudflareKV writer.
func NewCloudflareKV(accountID, namespaceID, apiToken string) *CloudflareKV {
	return &CloudflareKV{
		AccountID:   accountID,
		NamespaceID: namespaceID,
		APIToken:    apiToken,
		HTTP:        &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *CloudflareKV) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return "https://api.cloudflare.com/client/v4"
}

func (c *CloudflareKV) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// kvValueURL builds the REST URL for a single KV key (write/delete).
func (c *CloudflareKV) kvValueURL(key string) string {
	return fmt.Sprintf("%s/accounts/%s/storage/kv/namespaces/%s/values/%s",
		c.baseURL(), c.AccountID, c.NamespaceID, key)
}

// PutRoute writes the route value (PUT .../values/route:<host>).
func (c *CloudflareKV) PutRoute(ctx context.Context, host string, val RouteValue) error {
	if err := val.Validate(); err != nil {
		return err
	}
	body, err := json.Marshal(val)
	if err != nil {
		return err
	}
	// The KV REST API stores the raw request body as the value; we send the
	// canonical JSON the Worker parses with @shipped/contracts.parseKVRouteValue.
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		c.kvValueURL(RouteKey(host)), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.auth(req)
	return c.do(req, "put route "+host)
}

// DeleteRoute removes a host's route (DELETE .../values/route:<host>).
func (c *CloudflareKV) DeleteRoute(ctx context.Context, host string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		c.kvValueURL(RouteKey(host)), nil)
	if err != nil {
		return err
	}
	c.auth(req)
	return c.do(req, "delete route "+host)
}

// RebuildFromDB re-pushes every supplied route. KV has no transactional bulk
// replace via this single-key path, so the reconciler semantics are: write every
// authoritative route (last-writer-wins). Stale keys for deleted sites are
// handled by DeleteRoute on delete; a full GC pass is a Phase-4 concern. This
// keeps the "rebuildable from Postgres" invariant: after a wipe, replaying these
// writes restores serving.
func (c *CloudflareKV) RebuildFromDB(ctx context.Context, routes map[string]RouteValue) error {
	for host, val := range routes {
		if err := c.PutRoute(ctx, host, val); err != nil {
			return fmt.Errorf("rebuild: %w", err)
		}
	}
	return nil
}

func (c *CloudflareKV) auth(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.APIToken)
}

// do executes the request and maps a non-2xx to an error including the body.
func (c *CloudflareKV) do(req *http.Request, what string) error {
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("projection: %s: %w", what, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("projection: %s: cloudflare returned %d: %s",
			what, resp.StatusCode, bytes.TrimSpace(b))
	}
	return nil
}

// Ensure CloudflareKV satisfies Writer.
var _ Writer = (*CloudflareKV)(nil)
