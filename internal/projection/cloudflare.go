// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package projection

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/danielpang/dropway/internal/edgerevoke"
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
	// canonical JSON the Worker parses with @dropway/contracts.parseKVRouteValue.
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

// RebuildFromDB REPLACES the route keyspace with the supplied authoritative set,
// honoring the Writer contract ("clears the projection and re-writes it") so the
// CloudflareKV and Local writers behave identically. It lists the existing
// route:<host> keys, DELETEs any not in the authoritative set (a host deleted or
// reassigned before a wipe-and-restore must not survive as a stale — or, worse,
// wrong-tenant — route), then writes every supplied route. Only the "route:"
// prefix is touched: the revoked:/org_status: keys in the same namespace are left
// intact. This keeps the "rebuildable from Postgres" invariant exact.
func (c *CloudflareKV) RebuildFromDB(ctx context.Context, routes map[string]RouteValue) error {
	// Validate the whole input BEFORE any destructive prune, so a malformed route
	// can never leave the projection half-cleared.
	desired := make(map[string]struct{}, len(routes))
	for host, val := range routes {
		if err := val.Validate(); err != nil {
			return fmt.Errorf("rebuild: route %s: %w", host, err)
		}
		desired[RouteKey(host)] = struct{}{}
	}

	existing, err := c.listKeys(ctx, "route:")
	if err != nil {
		return fmt.Errorf("rebuild: list keys: %w", err)
	}
	for _, key := range existing {
		if _, keep := desired[key]; keep {
			continue
		}
		if err := c.deleteKey(ctx, key); err != nil {
			return fmt.Errorf("rebuild: prune %s: %w", key, err)
		}
	}

	for host, val := range routes {
		if err := c.PutRoute(ctx, host, val); err != nil {
			return fmt.Errorf("rebuild: %w", err)
		}
	}
	return nil
}

// listKeys returns every KV key under prefix, following the list API's pagination
// cursor. Used by RebuildFromDB to find stale keys to prune.
func (c *CloudflareKV) listKeys(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	cursor := ""
	for {
		u := fmt.Sprintf("%s/accounts/%s/storage/kv/namespaces/%s/keys?prefix=%s&limit=1000",
			c.baseURL(), c.AccountID, c.NamespaceID, url.QueryEscape(prefix))
		if cursor != "" {
			u += "&cursor=" + url.QueryEscape(cursor)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		c.auth(req)
		resp, err := c.httpClient().Do(req)
		if err != nil {
			return nil, err
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("cloudflare returned %d: %s", resp.StatusCode, bytes.TrimSpace(body))
		}
		var parsed struct {
			Result []struct {
				Name string `json:"name"`
			} `json:"result"`
			ResultInfo struct {
				Cursor string `json:"cursor"`
			} `json:"result_info"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, err
		}
		for _, r := range parsed.Result {
			keys = append(keys, r.Name)
		}
		if parsed.ResultInfo.Cursor == "" {
			break
		}
		cursor = parsed.ResultInfo.Cursor
	}
	return keys, nil
}

// deleteKey DELETEs a single KV key by its full name (e.g. "route:<host>").
func (c *CloudflareKV) deleteKey(ctx context.Context, key string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.kvValueURL(key), nil)
	if err != nil {
		return err
	}
	c.auth(req)
	return c.do(req, "prune "+key)
}

// Revoke writes (or tightens) the hard-revocation denylist entry for (kind, id) in
// the SAME KV namespace as the route projection, under the "revoked:" prefix (the
// edgerevoke contract). It is IDEMPOTENT: it reads the existing entry and keeps the
// LATER min_iat (max wins), so the denylist only ever tightens and a re-run is a
// no-op. The serving Worker + /authz read these keys on every gated request.
func (c *CloudflareKV) Revoke(ctx context.Context, kind edgerevoke.Kind, id string, minIAT int64) error {
	if !kind.Valid() {
		return fmt.Errorf("projection: invalid revoke kind %q", kind)
	}
	if id == "" {
		return fmt.Errorf("projection: revoke id is empty")
	}
	v := edgerevoke.Value{MinIAT: minIAT}
	if err := v.Validate(); err != nil {
		return err
	}
	key := edgerevoke.Key(kind, id)

	// Idempotent max: read the current value; if it already has a >= min_iat, keep
	// it. A read failure (missing key / transient) falls through to a write — a
	// LOWER min_iat can never be written because we only proceed to write our value,
	// and our value is monotonic from the caller (now()); the worst case is a
	// redundant write, never a loosened denylist.
	if cur, ok, err := c.getRevoked(ctx, key); err == nil && ok && cur.MinIAT >= v.MinIAT {
		return nil
	}

	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.kvValueURL(key), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.auth(req)
	return c.do(req, "revoke "+key)
}

// LookupRevoked reads the denylist entry for (kind, id) — the read side of the
// hard-revocation denylist the /authz mint path consults so a revoked subject
// cannot immediately re-mint a fresh edge token (H2). A clean miss → (_, false, nil);
// a transient KV error is returned so the caller FAILS CLOSED.
func (c *CloudflareKV) LookupRevoked(ctx context.Context, kind edgerevoke.Kind, id string) (edgerevoke.Value, bool, error) {
	if !kind.Valid() {
		return edgerevoke.Value{}, false, fmt.Errorf("projection: invalid revoke kind %q", kind)
	}
	if id == "" {
		return edgerevoke.Value{}, false, fmt.Errorf("projection: revoke id is empty")
	}
	return c.getRevoked(ctx, edgerevoke.Key(kind, id))
}

// getRevoked GETs the denylist value at key. (false, nil) on a 404 (no entry yet).
func (c *CloudflareKV) getRevoked(ctx context.Context, key string) (edgerevoke.Value, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.kvValueURL(key), nil)
	if err != nil {
		return edgerevoke.Value{}, false, err
	}
	c.auth(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return edgerevoke.Value{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return edgerevoke.Value{}, false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return edgerevoke.Value{}, false, fmt.Errorf("projection: get %s: cloudflare returned %d: %s",
			key, resp.StatusCode, bytes.TrimSpace(b))
	}
	var v edgerevoke.Value
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return edgerevoke.Value{}, false, err
	}
	return v, true, nil
}

// SetOrgStatus projects the org's suspension/over-limit signal to
// `org_status:<orgID>` (the Worker's read key). A blocking status PUTs the bare
// status string; the canonical "active" (or empty) DELETEs the key so the org is
// served again. This is the fast KV flag that makes a DB-side suspension actually
// block at the edge (the DB column alone never reaches the Worker). It is
// best-effort and rebuildable — the caller logs a failure rather than failing the
// webhook (DB is the source of truth).
func (c *CloudflareKV) SetOrgStatus(ctx context.Context, orgID, status string) error {
	if orgID == "" {
		return fmt.Errorf("projection: SetOrgStatus with empty orgID")
	}
	key := OrgStatusKey(orgID)
	if status == "" || status == OrgStatusActive {
		// Clear: an active org is served, so the absence of the key is the signal.
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.kvValueURL(key), nil)
		if err != nil {
			return err
		}
		c.auth(req)
		return c.do(req, "clear org_status "+orgID)
	}
	// Block: write the bare status string the Worker reads (isBlockingStatus).
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.kvValueURL(key), bytes.NewReader([]byte(status)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain")
	c.auth(req)
	return c.do(req, "set org_status "+orgID)
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

// Ensure CloudflareKV satisfies Writer, Revoker, and OrgStatusWriter.
var (
	_ Writer          = (*CloudflareKV)(nil)
	_ Revoker         = (*CloudflareKV)(nil)
	_ OrgStatusWriter = (*CloudflareKV)(nil)
)
