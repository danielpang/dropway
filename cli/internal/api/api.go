// Package api is the CLI's thin client for the Shipped control plane
// (api.shipped.app). The network call is behind the Client interface so the CLI
// builds and the deploy command's plan/dry-run path runs without a live server
// (docs/ARCHITECTURE.md §7.1).
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/danielpang/shipped/cli/internal/manifest"
)

// PrepareRequest is the body POSTed to /v1/deployments/prepare. The server
// authorizes the Bearer token, scopes dedup to the caller's org, and replies
// with the subset of blobs it doesn't already have (plus presigned PUT targets).
type PrepareRequest struct {
	// SiteSlug is the target site (optional on first deploy; server may mint one).
	SiteSlug string `json:"site_slug,omitempty"`
	// Digest is the whole-deploy content address (manifest.Manifest.Digest).
	Digest string `json:"digest"`
	// Files is the path→hash manifest. The server never trusts request-body
	// identifiers for the R2 key — it derives keys from the token org + each
	// server-validated sha256 — but it needs the manifest to compute "missing".
	Files []manifest.Entry `json:"files"`
	// TotalSize is the sum of file sizes (lets the server pre-check quota).
	TotalSize int64 `json:"total_size"`
}

// PrepareResponse is the server's reply: which blobs are missing and need upload.
type PrepareResponse struct {
	DeploymentID string   `json:"deployment_id"`
	MissingSHA   []string `json:"missing_sha256"`
}

// Client is the control-plane surface the deploy command needs. Keeping it an
// interface lets the command "print what it would POST" without any network, and
// lets tests inject a fake.
type Client interface {
	PrepareDeployment(ctx context.Context, req PrepareRequest) (*PrepareResponse, error)
}

// HTTPClient is the real Client. Token is the SHIPPED_TOKEN Bearer credential.
type HTTPClient struct {
	BaseURL string       // e.g. https://api.shipped.app
	Token   string       // Bearer deploy token (SHIPPED_TOKEN)
	HTTP    *http.Client // nil → http.DefaultClient
}

// PrepareDeployment POSTs the manifest to /v1/deployments/prepare.
func (c *HTTPClient) PrepareDeployment(ctx context.Context, req PrepareRequest) (*PrepareResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/v1/deployments/prepare", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.Token)

	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("prepare: server returned %d: %s", resp.StatusCode, bytes.TrimSpace(b))
	}

	var out PrepareResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MarshalRequest renders a PrepareRequest as indented JSON for the CLI's
// dry-run/plan output (the "JSON it would POST").
func MarshalRequest(req PrepareRequest) (string, error) {
	b, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
