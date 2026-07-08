// Skills client: the org-wide skill-sharing surface (push/list/pull). Same
// conventions as api.go — wire structs mirroring the server's handlers, a
// per-command-family interface so tests inject a small fake, and the shared
// postJSON/getJSON helpers on HTTPClient.
package api

import (
	"context"
	"net/url"
)

// SkillFolderRef is one folder membership on a skill row.
type SkillFolderRef struct {
	ID       string `json:"id"`
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	IsPreset bool   `json:"is_preset"`
}

// Skill is the API's skill representation (subset the CLI needs).
type Skill struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	OwnerID     string `json:"owner_id"`
	// IsSeeded marks a Dropway-provided preset (owner is the seed sentinel); the
	// list view renders the owner as "dropway" from this flag.
	IsSeeded  bool  `json:"is_seeded"`
	SizeBytes int64 `json:"size_bytes"`
	// Version is the current version's monotonic number (0 before the first
	// upload). `skills check` compares a pulled skill's recorded version to this.
	Version int32            `json:"version"`
	Folders []SkillFolderRef `json:"folders"`
}

// SkillsResponse is the GET /v1/skills body.
type SkillsResponse struct {
	Skills []Skill `json:"skills"`
}

// CreateSkillRequest registers a skill by slug; Folders are folder IDs (not
// slugs — the CLI resolves slugs first via ListSkillFolders).
type CreateSkillRequest struct {
	Slug    string   `json:"slug"`
	Title   string   `json:"title,omitempty"`
	Folders []string `json:"folders,omitempty"`
}

// SkillFolder is one org skill folder (GET /v1/skill-folders).
type SkillFolder struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Title     string `json:"title"`
	ItemCount int64  `json:"item_count"`
}

// SkillFoldersResponse is the GET /v1/skill-folders body.
type SkillFoldersResponse struct {
	Folders []SkillFolder `json:"folders"`
}

// SkillFile is one downloaded file: utf8 text inline or base64 bytes.
type SkillFile struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Encoding string `json:"encoding"` // "utf8" | "base64"
}

// SkillDownload is one whole skill's files (the per-skill and bulk shapes).
// Truncated marks a skill omitted from a BULK download because the response
// budget ran out — fetch it individually via DownloadSkill.
type SkillDownload struct {
	Slug      string      `json:"slug"`
	SkillID   string      `json:"skill_id"`
	Version   int32       `json:"version"`
	Truncated bool        `json:"truncated,omitempty"`
	Files     []SkillFile `json:"files,omitempty"`
}

// SkillFolderDownload is the GET /v1/skill-folders/{id}/download body.
type SkillFolderDownload struct {
	Folder   SkillFolder     `json:"folder"`
	Skills   []SkillDownload `json:"skills"`
	Warnings []string        `json:"warnings,omitempty"`
}

// SkillFinalizeResponse carries the created (and immediately live) version.
type SkillFinalizeResponse struct {
	VersionID string   `json:"version_id"`
	VersionNo int32    `json:"version_no"`
	Warnings  []string `json:"warnings,omitempty"`
}

// SkillsClient is the control-plane surface the `skills` commands need.
// Separate from Client/ReadClient (mirroring how those split per command
// family) so the skills fakes stay small and the deploy interface doesn't
// widen. Prepare/finalize reuse the deploy wire types — the skill upload flow
// speaks the same manifest+digest contract.
type SkillsClient interface {
	CreateSkill(ctx context.Context, req CreateSkillRequest) (*Skill, error)
	ListSkills(ctx context.Context, q, folder string, presets bool) (*SkillsResponse, error)
	ListSkillFolders(ctx context.Context) (*SkillFoldersResponse, error)
	DownloadSkill(ctx context.Context, id string) (*SkillDownload, error)
	DownloadSkillFolder(ctx context.Context, id string) (*SkillFolderDownload, error)
	PrepareSkillUpload(ctx context.Context, id string, req PrepareRequest) (*PrepareResponse, error)
	FinalizeSkillUpload(ctx context.Context, id string, req FinalizeRequest) (*SkillFinalizeResponse, error)
	UploadBlob(ctx context.Context, presignedURL string, data []byte) error
}

// CreateSkill registers a skill (metadata only — content arrives via the
// prepare/upload/finalize flow).
func (c *HTTPClient) CreateSkill(ctx context.Context, req CreateSkillRequest) (*Skill, error) {
	var out struct {
		Skill Skill `json:"skill"`
	}
	if err := c.postJSON(ctx, "/v1/skills", req, &out); err != nil {
		return nil, err
	}
	return &out.Skill, nil
}

// ListSkills lists/searches the org's skills: q is a text filter, folder a
// folder slug, presets restricts to preset-flagged members.
func (c *HTTPClient) ListSkills(ctx context.Context, q, folder string, presets bool) (*SkillsResponse, error) {
	v := url.Values{}
	if q != "" {
		v.Set("q", q)
	}
	if folder != "" {
		v.Set("folder", folder)
	}
	if presets {
		v.Set("presets", "true")
	}
	path := "/v1/skills"
	if enc := v.Encode(); enc != "" {
		path += "?" + enc
	}
	var out SkillsResponse
	if err := c.getJSON(ctx, path, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListSkillFolders returns the org's skill folders.
func (c *HTTPClient) ListSkillFolders(ctx context.Context) (*SkillFoldersResponse, error) {
	var out SkillFoldersResponse
	if err := c.getJSON(ctx, "/v1/skill-folders", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DownloadSkill fetches every file of the skill's current version.
func (c *HTTPClient) DownloadSkill(ctx context.Context, id string) (*SkillDownload, error) {
	var out SkillDownload
	if err := c.getJSON(ctx, "/v1/skills/"+id+"/download", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DownloadSkillFolder bulk-fetches a folder's skills (oversized members come
// back Truncated and must be fetched via DownloadSkill).
func (c *HTTPClient) DownloadSkillFolder(ctx context.Context, id string) (*SkillFolderDownload, error) {
	var out SkillFolderDownload
	if err := c.getJSON(ctx, "/v1/skill-folders/"+id+"/download", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PrepareSkillUpload computes missing blobs + presigned upload URLs.
func (c *HTTPClient) PrepareSkillUpload(ctx context.Context, id string, req PrepareRequest) (*PrepareResponse, error) {
	var out PrepareResponse
	if err := c.postJSON(ctx, "/v1/skills/"+id+"/uploads/prepare", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FinalizeSkillUpload server-verifies blobs and writes the new version (which
// goes live immediately — skills are latest-only).
func (c *HTTPClient) FinalizeSkillUpload(ctx context.Context, id string, req FinalizeRequest) (*SkillFinalizeResponse, error) {
	var out SkillFinalizeResponse
	if err := c.postJSON(ctx, "/v1/skills/"+id+"/uploads", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
