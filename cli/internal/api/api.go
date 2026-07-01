// Package api is the CLI's client for the Dropway control plane (api.dropway.dev).
// The network calls are behind the Client interface so the deploy command's
// plan/dry-run path runs without a live server and tests inject a fake.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/danielpang/dropway/cli/internal/manifest"
	"github.com/danielpang/dropway/internal/contenttype"
)

// ManifestFile is one file in a deploy: request-path → content hash (+ size +
// content-type). Mirrors the server's handlers.ManifestFile. The server derives
// the R2 blob key from the authenticated org + sha256 — never the client path.
type ManifestFile struct {
	Path        string `json:"path"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type,omitempty"`
}

// PrepareRequest is the body POSTed to /v1/sites/{id}/deployments/prepare.
type PrepareRequest struct {
	Manifest []ManifestFile `json:"manifest"`
}

// PrepareResponse lists which blobs are missing and their presigned PUT URLs.
type PrepareResponse struct {
	Missing []string          `json:"missing"`
	Uploads map[string]string `json:"uploads"`
}

// FinalizeRequest is the body POSTed to /v1/sites/{id}/deployments.
type FinalizeRequest struct {
	Manifest []ManifestFile `json:"manifest"`
	Digest   string         `json:"digest"`
}

// FinalizeResponse carries the created immutable version.
type FinalizeResponse struct {
	VersionID  string `json:"version_id"`
	VersionNo  int32  `json:"version_no"`
	PreviewURL string `json:"preview_url"`
}

// PublishRequest is the body POSTed to /v1/sites/{id}/publish.
type PublishRequest struct {
	VersionID string `json:"version_id"`
}

// PublishResponse carries the live URL after the pointer flip.
type PublishResponse struct {
	LiveURL   string `json:"live_url"`
	VersionID string `json:"version_id"`
}

// Site is the API's site representation (subset the CLI needs).
type Site struct {
	ID         string `json:"id"`
	Slug       string `json:"slug"`
	OwnerID    string `json:"owner_id"`
	AccessMode string `json:"access_mode"`
	LiveURL    string `json:"live_url"`
}

// SitesResponse is the GET /v1/sites body: every site in the caller's active org
// (the API scopes it to the org; the CLI filters to the caller for the personal view).
type SitesResponse struct {
	Sites []Site `json:"sites"`
}

// MeResponse is the subset of GET /v1/me the CLI needs — the caller's user id,
// used to pick out the sites they own.
type MeResponse struct {
	UserID string `json:"user_id"`
}

// CreateSiteRequest creates a site by slug.
type CreateSiteRequest struct {
	Slug string `json:"slug"`
}

// Client is the control-plane surface the deploy command needs. Keeping it an
// interface lets the command run a dry run with no network and lets tests inject
// a fake server.
type Client interface {
	CreateSite(ctx context.Context, req CreateSiteRequest) (*Site, error)
	PrepareDeployment(ctx context.Context, siteID string, req PrepareRequest) (*PrepareResponse, error)
	UploadBlob(ctx context.Context, presignedURL string, data []byte) error
	FinalizeDeployment(ctx context.Context, siteID string, req FinalizeRequest) (*FinalizeResponse, error)
	Publish(ctx context.Context, siteID string, req PublishRequest) (*PublishResponse, error)
}

// ReadClient is the read-only control-plane surface the `sites` and `read`
// commands need. Separate from Client (and its deploy fake) so the read commands
// stay testable with a small fake and don't widen the deploy interface.
type ReadClient interface {
	ListSites(ctx context.Context) (*SitesResponse, error)
	Me(ctx context.Context) (*MeResponse, error)
}

// HTTPClient is the real Client. Token is the Bearer credential (DROPWAY_TOKEN).
type HTTPClient struct {
	BaseURL string
	Token   string
	HTTP    *http.Client // nil → http.DefaultClient
}

func (c *HTTPClient) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// postJSON POSTs body as JSON to the API path with the Bearer token and decodes
// the JSON response into out (out may be nil to ignore the body).
func (c *HTTPClient) postJSON(ctx context.Context, path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("POST %s: server returned %d: %s", path, resp.StatusCode, bytes.TrimSpace(rb))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// getJSON GETs the API path with the Bearer token and decodes the JSON response
// into out. The read-only counterpart to postJSON.
func (c *HTTPClient) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("GET %s: server returned %d: %s", path, resp.StatusCode, bytes.TrimSpace(rb))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ListSites returns every site in the caller's active org.
func (c *HTTPClient) ListSites(ctx context.Context) (*SitesResponse, error) {
	var out SitesResponse
	if err := c.getJSON(ctx, "/v1/sites", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Me returns the authenticated caller's identity (used to filter to owned sites).
func (c *HTTPClient) Me(ctx context.Context) (*MeResponse, error) {
	var out MeResponse
	if err := c.getJSON(ctx, "/v1/me", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateSite creates a site.
func (c *HTTPClient) CreateSite(ctx context.Context, req CreateSiteRequest) (*Site, error) {
	var out Site
	if err := c.postJSON(ctx, "/v1/sites", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PrepareDeployment computes missing blobs + presigned upload URLs.
func (c *HTTPClient) PrepareDeployment(ctx context.Context, siteID string, req PrepareRequest) (*PrepareResponse, error) {
	var out PrepareResponse
	if err := c.postJSON(ctx, "/v1/sites/"+siteID+"/deployments/prepare", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UploadBlob PUTs the blob's bytes directly to the presigned URL (R2/MinIO).
func (c *HTTPClient) UploadBlob(ctx context.Context, presignedURL string, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, presignedURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(data))
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return fmt.Errorf("upload blob: store returned %d: %s", resp.StatusCode, bytes.TrimSpace(rb))
	}
	return nil
}

// FinalizeDeployment server-verifies blobs, writes the manifest, inserts version.
func (c *HTTPClient) FinalizeDeployment(ctx context.Context, siteID string, req FinalizeRequest) (*FinalizeResponse, error) {
	var out FinalizeResponse
	if err := c.postJSON(ctx, "/v1/sites/"+siteID+"/deployments", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Publish flips the live-version pointer and projects the route to the edge.
func (c *HTTPClient) Publish(ctx context.Context, siteID string, req PublishRequest) (*PublishResponse, error) {
	var out PublishResponse
	if err := c.postJSON(ctx, "/v1/sites/"+siteID+"/publish", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ManifestFromBuild converts a manifest.Manifest into the API wire shape.
func ManifestFromBuild(m *manifest.Manifest) []ManifestFile {
	out := make([]ManifestFile, len(m.Files))
	for i, e := range m.Files {
		out[i] = ManifestFile{
			Path:        e.Path,
			SHA256:      e.SHA256,
			Size:        e.Size,
			ContentType: contenttype.ForPath(e.Path),
		}
	}
	return out
}
