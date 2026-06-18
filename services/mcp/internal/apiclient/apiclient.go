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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/danielpang/dropway/internal/contenttype"
	"github.com/danielpang/dropway/internal/manifest"
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

// --- deploy -----------------------------------------------------------------

// DeployFile is one file to publish: its served path + raw bytes (+ optional
// content type, inferred from the extension when empty).
type DeployFile struct {
	Path        string
	Data        []byte
	ContentType string
}

// DeployResult summarizes a completed deploy.
type DeployResult struct {
	VersionID     string
	LiveURL       string
	FilesUploaded int
	Published     bool
}

type manifestFile struct {
	Path        string `json:"path"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type,omitempty"`
}

// Deploy runs the full server-side deploy loop through the API under the user's
// token: prepare (manifest → missing blobs + presigned PUT URLs) → upload the
// missing blobs directly to the object store → finalize (the API re-hashes every
// blob + verifies the digest) → publish (the API flips the live pointer and writes
// the edge projection). Set publish=false to stage a version without going live.
func (c *Client) Deploy(ctx context.Context, token, siteID string, files []DeployFile, publish bool) (DeployResult, error) {
	if len(files) == 0 {
		return DeployResult{}, fmt.Errorf("deploy: no files")
	}

	// Build the manifest + the sha→bytes map, and the digest input (path+sha only).
	mf := make([]manifestFile, 0, len(files))
	digestFiles := make([]manifest.File, 0, len(files))
	bySHA := map[string][]byte{}
	for _, f := range files {
		sum := sha256.Sum256(f.Data)
		sha := hex.EncodeToString(sum[:])
		ct := f.ContentType
		if ct == "" {
			ct = contenttype.ForPath(f.Path)
		}
		mf = append(mf, manifestFile{Path: f.Path, SHA256: sha, Size: int64(len(f.Data)), ContentType: ct})
		digestFiles = append(digestFiles, manifest.File{Path: f.Path, SHA256: sha})
		bySHA[sha] = f.Data
	}
	digest := manifest.Digest(digestFiles)

	// 1) prepare → which blobs are missing + where to PUT them.
	var prep struct {
		Missing []string          `json:"missing"`
		Uploads map[string]string `json:"uploads"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/sites/"+siteID+"/deployments/prepare", token,
		map[string]any{"manifest": mf}, &prep); err != nil {
		return DeployResult{}, err
	}

	// 2) upload each missing blob to its presigned URL (raw PUT, URL is the credential).
	for _, sha := range prep.Missing {
		url := prep.Uploads[sha]
		if url == "" {
			return DeployResult{}, fmt.Errorf("deploy: no upload URL for blob %s", sha)
		}
		if err := c.uploadBlob(ctx, url, bySHA[sha]); err != nil {
			return DeployResult{}, fmt.Errorf("deploy: upload %s: %w", sha, err)
		}
	}

	// 3) finalize → the API verifies bytes + digest and records the version.
	var fin struct {
		VersionID string `json:"version_id"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/sites/"+siteID+"/deployments", token,
		map[string]any{"manifest": mf, "digest": digest}, &fin); err != nil {
		return DeployResult{}, err
	}

	res := DeployResult{VersionID: fin.VersionID, FilesUploaded: len(prep.Missing)}
	if !publish {
		return res, nil
	}

	// 4) publish → flip the live pointer + write the edge projection.
	var pub struct {
		LiveURL string `json:"live_url"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/sites/"+siteID+"/publish", token,
		map[string]string{"version_id": fin.VersionID}, &pub); err != nil {
		return DeployResult{}, err
	}
	res.LiveURL = pub.LiveURL
	res.Published = true
	return res, nil
}

// uploadBlob PUTs raw bytes to a presigned URL. No Authorization (the URL is the
// credential) and no Content-Type (it's not part of the SigV4 signature) — matching
// the dashboard's browser upload.
func (c *Client) uploadBlob(ctx context.Context, url string, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &Error{Status: resp.StatusCode, Message: "blob upload failed"}
	}
	return nil
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
