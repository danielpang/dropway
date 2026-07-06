// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package tools implements the Dropway MCP tools — list_sites, list_files,
// read_file, … plus the org-wide skill-sharing tools (list_skills,
// download_skill, download_skill_folder, upload_skill) — over a tenant's
// deployed documents and shared skills. Every call is org-scoped: the
// tenant comes from the validated OAuth token (auth.TenantFromContext) and the
// store enforces RLS. The exported methods take the tenant explicitly so they're
// unit-testable; the SDK handlers are thin wrappers that pull it from context.
package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	slugpkg "github.com/danielpang/dropway/internal/slug"
	"github.com/danielpang/dropway/services/mcp/internal/apiclient"
	"github.com/danielpang/dropway/services/mcp/internal/auth"
	"github.com/danielpang/dropway/services/mcp/internal/store"
)

// maxDownloadBytes caps the total bytes download_site returns inline, so a huge site
// can't blow up a single tool response. Files beyond the cap are omitted (Truncated).
// A var (not const) so tests can lower it without staging megabytes of fixtures.
var maxDownloadBytes = 10 << 20 // 10 MiB

// SiteStore is the site data the tools read (RLS-scoped).
type SiteStore interface {
	ListSites(ctx context.Context, t store.Tenant) ([]store.Site, error)
	SiteBySlug(ctx context.Context, t store.Tenant, slug string) (store.Site, error)
}

// SkillStore is the skill data the skill tools read (RLS-scoped).
type SkillStore interface {
	ListSkills(ctx context.Context, t store.Tenant, query, folderSlug string, presetsOnly bool) ([]store.Skill, error)
	SkillBySlug(ctx context.Context, t store.Tenant, slug string) (store.Skill, error)
	ListSkillFolders(ctx context.Context, t store.Tenant) ([]store.SkillFolder, error)
	SkillFolderBySlug(ctx context.Context, t store.Tenant, slug string) (store.SkillFolder, error)
	ListFolderSkills(ctx context.Context, t store.Tenant, folderID string) ([]store.Skill, error)
}

// Blobs fetches deploy/skill manifests + content-addressed blobs (satisfied by
// internal/storage.Store).
type Blobs interface {
	GetManifest(ctx context.Context, orgID, siteID, versionID string) ([]byte, error)
	GetSkillManifest(ctx context.Context, orgID, skillID, versionID string) ([]byte, error)
	GetBlob(ctx context.Context, orgID, sha256 string) (io.ReadCloser, error)
}

// ControlPlane performs WRITES through the Go API (create site / change access /
// upload skill), forwarding the user's OAuth token. nil when the MCP server has no
// API_URL configured → the write tools are not registered. Satisfied by
// *apiclient.Client.
type ControlPlane interface {
	CreateSite(ctx context.Context, token, slug, accessMode string) (apiclient.Site, error)
	SetAccess(ctx context.Context, token, siteID, mode, password string) error
	Deploy(ctx context.Context, token, siteID string, files []apiclient.DeployFile, publish bool) (apiclient.DeployResult, error)
	CreateSkill(ctx context.Context, token, slug, title string, folders []string) (apiclient.SkillInfo, error)
	UploadSkill(ctx context.Context, token, skillID string, files []apiclient.FileUpload) (apiclient.UploadResult, error)
}

// Service holds the tool dependencies.
type Service struct {
	Store  SiteStore
	Skills SkillStore
	Blobs  Blobs
	// API is the control-plane write client. Optional: when nil the read tools still
	// work but the write tools (create_site, set_site_access, deploy_site,
	// upload_skill) are not registered.
	API ControlPlane
}

// ErrNoTenant means the request reached a tool without an authenticated tenant
// (should be impossible behind the auth middleware).
var ErrNoTenant = errors.New("mcp/tools: no authenticated tenant")

// ErrNoToken means a write tool ran without the forwardable bearer token (should be
// impossible behind the auth middleware, which stashes it).
var ErrNoToken = errors.New("mcp/tools: no bearer token to forward")

// --- Tool I/O ---------------------------------------------------------------

type listSitesIn struct{}

// SiteInfo is one entry in list_sites.
type SiteInfo struct {
	Slug       string `json:"slug" jsonschema:"the site's slug"`
	AccessMode string `json:"access_mode" jsonschema:"one of public, password, allowlist, org_only"`
	Live       bool   `json:"live" jsonschema:"true if a version is published"`
	URL        string `json:"url,omitempty" jsonschema:"the site's URL, if it has a host"`
}
type listSitesOut struct {
	Sites []SiteInfo `json:"sites"`
}

type listFilesIn struct {
	Site string `json:"site" jsonschema:"the site slug (from list_sites)"`
}
type listFilesOut struct {
	Files []string `json:"files" jsonschema:"the file paths in the site's current version"`
}

type readFileIn struct {
	Site string `json:"site" jsonschema:"the site slug (from list_sites)"`
	Path string `json:"path" jsonschema:"a file path from list_files"`
}
type readFileOut struct {
	Path        string `json:"path"`
	ContentType string `json:"content_type"`
	Text        string `json:"text,omitempty" jsonschema:"the file contents, for text files"`
	Base64      string `json:"base64,omitempty" jsonschema:"base64 contents, for binary files"`
}

type downloadSiteIn struct {
	Site string `json:"site" jsonschema:"the site slug (from list_sites)"`
}
type downloadedFile struct {
	Path        string `json:"path"`
	ContentType string `json:"content_type"`
	Size        int    `json:"size" jsonschema:"the file size in bytes"`
	Text        string `json:"text,omitempty" jsonschema:"the file contents, for text files"`
	Base64      string `json:"base64,omitempty" jsonschema:"base64 contents, for binary files"`
}
type downloadSiteOut struct {
	Site      string           `json:"site"`
	Files     []downloadedFile `json:"files"`
	Truncated bool             `json:"truncated,omitempty" jsonschema:"true if some files were omitted to stay under the size cap"`
}

type createSiteIn struct {
	Slug       string `json:"slug" jsonschema:"the new site's slug: a single lowercase DNS label (letters, digits, hyphens; 1-63 chars; no leading/trailing or doubled hyphens), unique per org. Loose input is normalized (e.g. 'My Blog' becomes 'my-blog') and the final slug is returned in the response."`
	AccessMode string `json:"access_mode,omitempty" jsonschema:"initial access: 'public' or 'org_only' (default: the org's default, usually org_only)"`
}
type createSiteOut struct {
	Slug       string `json:"slug"`
	AccessMode string `json:"access_mode"`
	URL        string `json:"url,omitempty"`
}

type setAccessIn struct {
	Site     string `json:"site" jsonschema:"the site slug (from list_sites)"`
	Mode     string `json:"mode" jsonschema:"new access mode: 'public', 'org_only', 'password', or 'allowlist'"`
	Password string `json:"password,omitempty" jsonschema:"required only when mode='password'"`
}
type setAccessOut struct {
	Slug string `json:"slug"`
	Mode string `json:"mode"`
}

type deployFileIn struct {
	Path        string `json:"path" jsonschema:"the served path, e.g. 'index.html' or 'assets/app.js'"`
	Text        string `json:"text,omitempty" jsonschema:"the file contents as text (use this OR base64)"`
	Base64      string `json:"base64,omitempty" jsonschema:"the file contents as base64, for binary files"`
	ContentType string `json:"content_type,omitempty" jsonschema:"optional MIME type; inferred from the path when omitted"`
}
type deploySiteIn struct {
	Site    string         `json:"site" jsonschema:"the site slug (from list_sites or create_site)"`
	Files   []deployFileIn `json:"files" jsonschema:"the files to publish; include an index.html for the site root"`
	Publish *bool          `json:"publish,omitempty" jsonschema:"publish (go live) after upload; default true. false stages a version without going live"`
}
type deploySiteOut struct {
	Site          string `json:"site"`
	VersionID     string `json:"version_id"`
	FilesUploaded int    `json:"files_uploaded" jsonschema:"how many new blobs were uploaded (unchanged files are skipped)"`
	Published     bool   `json:"published"`
	LiveURL       string `json:"live_url,omitempty"`
}

// seedOwnerUserID is the sentinel owner_user_id marking a Dropway-seeded preset
// skill (mirrors the API store's SeedOwnerUserID; rendered as owner "Dropway").
const seedOwnerUserID = "00000000-0000-0000-0000-000000000000"

type listSkillsIn struct {
	Query       string `json:"query,omitempty" jsonschema:"optional text filter matched against slug, title, and description"`
	Folder      string `json:"folder,omitempty" jsonschema:"optional folder slug (from the folders on list_skills entries) to restrict to"`
	PresetsOnly bool   `json:"presets_only,omitempty" jsonschema:"true to list only admin-curated preset skills"`
}

// skillFolderInfo is one folder membership on a list_skills entry.
type skillFolderInfo struct {
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	IsPreset bool   `json:"is_preset" jsonschema:"true when the skill is part of this folder's admin-curated preset set"`
}

// SkillInfo is one entry in list_skills.
type SkillInfo struct {
	Name        string            `json:"name" jsonschema:"the skill's slug — the argument for download_skill"`
	Title       string            `json:"title,omitempty"`
	Description string            `json:"description,omitempty"`
	Folders     []skillFolderInfo `json:"folders"`
	SizeBytes   int64             `json:"size_bytes" jsonschema:"total content size of the current version"`
	Owner       string            `json:"owner" jsonschema:"'Dropway' for seeded presets, otherwise the uploader's user id"`
	CreatedAt   string            `json:"created_at"`
}
type listSkillsOut struct {
	Skills []SkillInfo `json:"skills"`
}

type downloadSkillIn struct {
	Name string `json:"name" jsonschema:"the skill's slug (from list_skills)"`
}

// skillFilePayload is one downloaded skill file: utf8 text inline or base64
// bytes (the same shape the API's skill download returns).
type skillFilePayload struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Encoding string `json:"encoding" jsonschema:"'utf8' or 'base64'"`
}
type downloadSkillOut struct {
	Name      string             `json:"name"`
	Files     []skillFilePayload `json:"files"`
	Truncated bool               `json:"truncated,omitempty" jsonschema:"true if some files were omitted to stay under the size cap"`
}

type downloadSkillFolderIn struct {
	Folder string `json:"folder" jsonschema:"the folder slug (from list_skills entries' folders)"`
}

// folderSkillDownload is one skill inside a folder download. A truncated entry
// carries no files — fetch it individually with download_skill.
type folderSkillDownload struct {
	Name      string             `json:"name"`
	Files     []skillFilePayload `json:"files,omitempty"`
	Truncated bool               `json:"truncated,omitempty" jsonschema:"true when this skill's files were omitted because the response size cap ran out — call download_skill for it"`
}
type downloadSkillFolderOut struct {
	Folder string                `json:"folder"`
	Skills []folderSkillDownload `json:"skills"`
	Note   string                `json:"note,omitempty"`
}

type uploadSkillFileIn struct {
	Path     string `json:"path" jsonschema:"the file's path inside the skill, e.g. 'SKILL.md' or 'references/api.md'"`
	Content  string `json:"content" jsonschema:"the file contents (utf8 text, or base64 when encoding='base64')"`
	Encoding string `json:"encoding,omitempty" jsonschema:"'utf8' (default) or 'base64' for binary files"`
}
type uploadSkillIn struct {
	Name    string              `json:"name" jsonschema:"the skill's slug: a single lowercase DNS label, unique per org. Loose input is normalized (e.g. 'My Skill' becomes 'my-skill'). Re-uploading an existing name replaces its content (latest-only)."`
	Title   string              `json:"title,omitempty" jsonschema:"optional human title (only applied when the skill is first created; SKILL.md frontmatter fills it otherwise)"`
	Folders []string            `json:"folders,omitempty" jsonschema:"optional folder slugs to file the skill under (only applied when the skill is first created)"`
	Files   []uploadSkillFileIn `json:"files" jsonschema:"the skill's files; MUST include a SKILL.md at the root. Max 200 files, 5 MiB total."`
}
type uploadSkillOut struct {
	Name      string   `json:"name"`
	SkillID   string   `json:"skill_id"`
	VersionNo int32    `json:"version_no"`
	Warnings  []string `json:"warnings,omitempty"`
}

// --- Exported (testable) logic ----------------------------------------------

// ListSites returns the tenant's sites.
func (svc *Service) ListSites(ctx context.Context, t store.Tenant) (listSitesOut, error) {
	sites, err := svc.Store.ListSites(ctx, t)
	if err != nil {
		return listSitesOut{}, err
	}
	out := listSitesOut{Sites: []SiteInfo{}}
	for _, s := range sites {
		info := SiteInfo{Slug: s.Slug, AccessMode: s.AccessMode, Live: s.CurrentVersionID != nil}
		if s.Host != nil {
			info.URL = "https://" + *s.Host
		}
		out.Sites = append(out.Sites, info)
	}
	return out, nil
}

// ListFiles returns the paths in a site's current published version.
func (svc *Service) ListFiles(ctx context.Context, t store.Tenant, slug string) (listFilesOut, error) {
	site, err := svc.Store.SiteBySlug(ctx, t, slug)
	if err != nil {
		return listFilesOut{}, err
	}
	if site.CurrentVersionID == nil {
		return listFilesOut{Files: []string{}}, nil // not live → no files
	}
	entries, err := svc.manifestEntries(ctx, t.OrgID, site)
	if err != nil {
		return listFilesOut{}, err
	}
	paths := make([]string, 0, len(entries))
	for p := range entries {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return listFilesOut{Files: paths}, nil
}

// ReadFile returns the contents of one file in a site's current version.
func (svc *Service) ReadFile(ctx context.Context, t store.Tenant, slug, path string) (readFileOut, error) {
	site, err := svc.Store.SiteBySlug(ctx, t, slug)
	if err != nil {
		return readFileOut{}, err
	}
	if site.CurrentVersionID == nil {
		return readFileOut{}, store.ErrNotFound
	}
	entries, err := svc.manifestEntries(ctx, t.OrgID, site)
	if err != nil {
		return readFileOut{}, err
	}
	e, ok := entries[path]
	if !ok {
		return readFileOut{}, store.ErrNotFound
	}
	rc, err := svc.Blobs.GetBlob(ctx, t.OrgID, e.SHA256)
	if err != nil {
		return readFileOut{}, err
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		return readFileOut{}, err
	}
	out := readFileOut{Path: path, ContentType: e.ContentType}
	if utf8.Valid(b) {
		out.Text = string(b)
	} else {
		out.Base64 = base64.StdEncoding.EncodeToString(b)
	}
	return out, nil
}

// DownloadSite reads EVERY file of a site's current version, returning each path's
// bytes inline (text or base64), up to maxDownloadBytes total (Truncated past that).
func (svc *Service) DownloadSite(ctx context.Context, t store.Tenant, slug string) (downloadSiteOut, error) {
	site, err := svc.Store.SiteBySlug(ctx, t, slug)
	if err != nil {
		return downloadSiteOut{}, err
	}
	out := downloadSiteOut{Site: slug, Files: []downloadedFile{}}
	if site.CurrentVersionID == nil {
		return out, nil // not live → nothing to download
	}
	entries, err := svc.manifestEntries(ctx, t.OrgID, site)
	if err != nil {
		return downloadSiteOut{}, err
	}
	paths := make([]string, 0, len(entries))
	for p := range entries {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	total := 0
	for _, p := range paths {
		e := entries[p]
		rc, err := svc.Blobs.GetBlob(ctx, t.OrgID, e.SHA256)
		if err != nil {
			return downloadSiteOut{}, err
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return downloadSiteOut{}, err
		}
		if total+len(b) > maxDownloadBytes {
			out.Truncated = true
			break
		}
		total += len(b)
		f := downloadedFile{Path: p, ContentType: e.ContentType, Size: len(b)}
		if utf8.Valid(b) {
			f.Text = string(b)
		} else {
			f.Base64 = base64.StdEncoding.EncodeToString(b)
		}
		out.Files = append(out.Files, f)
	}
	return out, nil
}

// CreateSite creates a new site via the Go API (which enforces quota + reserves the
// global host) under the user's forwarded token. The slug is normalized to the
// canonical grammar the API enforces (mirroring the dashboard/CLI), so a loose
// agent-supplied value (e.g. "My Blog") becomes a valid slug instead of a 400;
// the API echoes the final slug back in the response.
func (svc *Service) CreateSite(ctx context.Context, token, rawSlug, accessMode string) (createSiteOut, error) {
	normalized := slugpkg.Slugify(rawSlug)
	if normalized == "" {
		return createSiteOut{}, fmt.Errorf("slug %q has no usable characters (use lowercase letters, digits, and hyphens)", rawSlug)
	}
	site, err := svc.API.CreateSite(ctx, token, normalized, accessMode)
	if err != nil {
		return createSiteOut{}, err
	}
	return createSiteOut{Slug: site.Slug, AccessMode: site.AccessMode, URL: site.URL}, nil
}

// SetAccess changes a site's access mode via the Go API (admin/owner only — the API
// re-checks the live role, rewrites the edge routes, and writes the revocation
// denylist). The slug is resolved to its id under RLS first (confirming the site is
// in the caller's org) so the agent can't target an arbitrary site id.
func (svc *Service) SetAccess(ctx context.Context, t store.Tenant, token, slug, mode, password string) (setAccessOut, error) {
	site, err := svc.Store.SiteBySlug(ctx, t, slug)
	if err != nil {
		return setAccessOut{}, err
	}
	if err := svc.API.SetAccess(ctx, token, site.ID, mode, password); err != nil {
		return setAccessOut{}, err
	}
	return setAccessOut{Slug: slug, Mode: mode}, nil
}

// DeploySite uploads files to a site and (by default) publishes them, via the Go
// API's deploy loop. The slug is resolved to its id under RLS first (confirming the
// site is in the caller's org); the rest runs in the API (blob verification, version
// record, edge projection on publish).
func (svc *Service) DeploySite(ctx context.Context, t store.Tenant, token, slug string, files []deployFileIn, publish bool) (deploySiteOut, error) {
	site, err := svc.Store.SiteBySlug(ctx, t, slug)
	if err != nil {
		return deploySiteOut{}, err
	}
	if len(files) == 0 {
		return deploySiteOut{}, errors.New("mcp/tools: deploy requires at least one file")
	}
	df := make([]apiclient.DeployFile, 0, len(files))
	for _, f := range files {
		var data []byte
		switch {
		case f.Base64 != "":
			b, derr := base64.StdEncoding.DecodeString(f.Base64)
			if derr != nil {
				return deploySiteOut{}, fmt.Errorf("mcp/tools: file %q has invalid base64: %w", f.Path, derr)
			}
			data = b
		default:
			data = []byte(f.Text) // text (possibly empty) when no base64 given
		}
		df = append(df, apiclient.DeployFile{Path: f.Path, Data: data, ContentType: f.ContentType})
	}

	res, err := svc.API.Deploy(ctx, token, site.ID, df, publish)
	if err != nil {
		return deploySiteOut{}, err
	}
	return deploySiteOut{
		Site:          slug,
		VersionID:     res.VersionID,
		FilesUploaded: res.FilesUploaded,
		Published:     res.Published,
		LiveURL:       res.LiveURL,
	}, nil
}

// ListSkills returns the org's finalized shared skills matching the filters.
func (svc *Service) ListSkills(ctx context.Context, t store.Tenant, query, folder string, presetsOnly bool) (listSkillsOut, error) {
	skills, err := svc.Skills.ListSkills(ctx, t, query, folder, presetsOnly)
	if err != nil {
		return listSkillsOut{}, err
	}
	out := listSkillsOut{Skills: []SkillInfo{}}
	for _, sk := range skills {
		info := SkillInfo{
			Name:        sk.Slug,
			Title:       sk.Title,
			Description: sk.Description,
			Folders:     []skillFolderInfo{},
			SizeBytes:   sk.SizeBytes,
			Owner:       sk.OwnerUserID,
			CreatedAt:   sk.CreatedAt.UTC().Format(time.RFC3339),
		}
		if sk.OwnerUserID == seedOwnerUserID {
			info.Owner = "Dropway"
		}
		for _, f := range sk.Folders {
			info.Folders = append(info.Folders, skillFolderInfo{Slug: f.Slug, Title: f.Title, IsPreset: f.IsPreset})
		}
		out.Skills = append(out.Skills, info)
	}
	return out, nil
}

// DownloadSkill reads EVERY file of a skill's current version, returning each
// path's bytes inline (utf8 or base64), up to maxDownloadBytes total.
func (svc *Service) DownloadSkill(ctx context.Context, t store.Tenant, name string) (downloadSkillOut, error) {
	sk, err := svc.Skills.SkillBySlug(ctx, t, name)
	if err != nil {
		return downloadSkillOut{}, err
	}
	if sk.CurrentVersionID == nil {
		return downloadSkillOut{}, fmt.Errorf("mcp/tools: skill %q has no uploaded content yet", name)
	}
	out := downloadSkillOut{Name: sk.Slug, Files: []skillFilePayload{}}
	files, _, err := svc.downloadSkillFiles(ctx, t.OrgID, sk, maxDownloadBytes)
	if err != nil {
		return downloadSkillOut{}, err
	}
	if files == nil {
		out.Truncated = true // whole skill over the cap (can't happen at the API's 5 MiB skill cap)
		return out, nil
	}
	out.Files = files
	return out, nil
}

// DownloadSkillFolder downloads every finalized skill in a folder under ONE
// shared maxDownloadBytes budget. Skills that would blow the budget come back as
// truncated entries (no files) with a note to fetch them via download_skill.
func (svc *Service) DownloadSkillFolder(ctx context.Context, t store.Tenant, folderSlug string) (downloadSkillFolderOut, error) {
	folder, err := svc.Skills.SkillFolderBySlug(ctx, t, folderSlug)
	if err != nil {
		return downloadSkillFolderOut{}, err
	}
	skills, err := svc.Skills.ListFolderSkills(ctx, t, folder.ID)
	if err != nil {
		return downloadSkillFolderOut{}, err
	}
	out := downloadSkillFolderOut{Folder: folder.Slug, Skills: []folderSkillDownload{}}
	budget := maxDownloadBytes
	var truncated []string
	for _, sk := range skills {
		if sk.CurrentVersionID == nil {
			continue // finalized-only listing, but stay defensive
		}
		files, used, err := svc.downloadSkillFiles(ctx, t.OrgID, sk, budget)
		if err != nil {
			return downloadSkillFolderOut{}, err
		}
		if files == nil { // wouldn't fit in the remaining budget
			out.Skills = append(out.Skills, folderSkillDownload{Name: sk.Slug, Truncated: true})
			truncated = append(truncated, sk.Slug)
			continue
		}
		budget -= used
		out.Skills = append(out.Skills, folderSkillDownload{Name: sk.Slug, Files: files})
	}
	if len(truncated) > 0 {
		out.Note = "response size cap reached — call download_skill individually for: " + strings.Join(truncated, ", ")
	}
	return out, nil
}

// UploadSkill decodes the input files, enforces the cheap client-side rules
// (root SKILL.md, safe paths), resolves folder slugs to ids via the RLS store,
// creates the skill through the API when the slug doesn't exist yet (reusing the
// existing skill otherwise — re-upload replaces content in the latest-only
// model), and runs the API upload loop under the user's forwarded token.
func (svc *Service) UploadSkill(ctx context.Context, t store.Tenant, token string, in uploadSkillIn) (uploadSkillOut, error) {
	name := slugpkg.Slugify(in.Name)
	if name == "" {
		return uploadSkillOut{}, fmt.Errorf("name %q has no usable characters (use lowercase letters, digits, and hyphens)", in.Name)
	}
	if len(in.Files) == 0 {
		return uploadSkillOut{}, errors.New("mcp/tools: upload_skill requires at least one file")
	}

	files := make([]apiclient.FileUpload, 0, len(in.Files))
	hasSkillMD := false
	for _, f := range in.Files {
		if unsafeSkillPath(f.Path) {
			return uploadSkillOut{}, fmt.Errorf("mcp/tools: unsafe file path %q (paths must be relative, without '..')", f.Path)
		}
		var data []byte
		switch f.Encoding {
		case "", "utf8":
			data = []byte(f.Content)
		case "base64":
			b, derr := base64.StdEncoding.DecodeString(f.Content)
			if derr != nil {
				return uploadSkillOut{}, fmt.Errorf("mcp/tools: file %q has invalid base64: %w", f.Path, derr)
			}
			data = b
		default:
			return uploadSkillOut{}, fmt.Errorf("mcp/tools: file %q has unknown encoding %q (use 'utf8' or 'base64')", f.Path, f.Encoding)
		}
		if f.Path == "SKILL.md" {
			hasSkillMD = true
		}
		files = append(files, apiclient.FileUpload{Path: f.Path, Content: data})
	}
	if !hasSkillMD {
		return uploadSkillOut{}, errors.New("mcp/tools: a skill needs a SKILL.md at its root")
	}

	// Resolve folder slugs → ids under RLS (the API create takes ids). An unknown
	// slug fails fast, listing what exists so the agent can self-correct.
	var folderIDs []string
	if len(in.Folders) > 0 {
		all, err := svc.Skills.ListSkillFolders(ctx, t)
		if err != nil {
			return uploadSkillOut{}, err
		}
		byslug := make(map[string]string, len(all))
		available := make([]string, 0, len(all))
		for _, f := range all {
			byslug[f.Slug] = f.ID
			available = append(available, f.Slug)
		}
		for _, slug := range in.Folders {
			id, ok := byslug[slug]
			if !ok {
				return uploadSkillOut{}, fmt.Errorf("mcp/tools: unknown folder %q (available: %s)", slug, strings.Join(available, ", "))
			}
			folderIDs = append(folderIDs, id)
		}
	}

	// Reuse an existing skill (idempotent re-upload) or create it via the API.
	skillID := ""
	switch existing, err := svc.Skills.SkillBySlug(ctx, t, name); {
	case err == nil:
		skillID = existing.ID
	case errors.Is(err, store.ErrNotFound):
		created, cerr := svc.API.CreateSkill(ctx, token, name, in.Title, folderIDs)
		if cerr != nil {
			return uploadSkillOut{}, cerr
		}
		skillID = created.ID
	default:
		return uploadSkillOut{}, err
	}

	res, err := svc.API.UploadSkill(ctx, token, skillID, files)
	if err != nil {
		return uploadSkillOut{}, err
	}
	return uploadSkillOut{Name: name, SkillID: skillID, VersionNo: res.VersionNo, Warnings: res.Warnings}, nil
}

// downloadSkillFiles reads a skill's files under a byte budget. Returns
// (nil, 0, nil) when the skill's manifest-declared total wouldn't fit — the
// caller renders that as a truncated entry instead of a partial skill (half a
// skill is worse than none: the agent would install it missing files).
func (svc *Service) downloadSkillFiles(ctx context.Context, orgID string, sk store.Skill, budget int) ([]skillFilePayload, int, error) {
	entries, err := svc.skillManifestEntries(ctx, orgID, sk)
	if err != nil {
		return nil, 0, err
	}
	paths := make([]string, 0, len(entries))
	var declared int64
	for p, e := range entries {
		if unsafeSkillPath(p) {
			return nil, 0, fmt.Errorf("mcp/tools: skill %q manifest has unsafe path %q", sk.Slug, p)
		}
		paths = append(paths, p)
		declared += e.Size
	}
	if declared > int64(budget) {
		return nil, 0, nil
	}
	sort.Strings(paths)

	files := make([]skillFilePayload, 0, len(paths))
	total := 0
	for _, p := range paths {
		rc, err := svc.Blobs.GetBlob(ctx, orgID, entries[p].SHA256)
		if err != nil {
			return nil, 0, err
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, 0, err
		}
		if total+len(b) > budget {
			return nil, 0, nil // stored bytes exceeded the declared sizes — treat as over-budget
		}
		total += len(b)
		f := skillFilePayload{Path: p}
		if utf8.Valid(b) {
			f.Content, f.Encoding = string(b), "utf8"
		} else {
			f.Content, f.Encoding = base64.StdEncoding.EncodeToString(b), "base64"
		}
		files = append(files, f)
	}
	return files, total, nil
}

// unsafeSkillPath re-checks a skill file path locally. The API validates paths
// server-side, but the MCP layer refuses traversal-shaped paths anyway before an
// agent writes the files into .claude/skills/<name>/.
func unsafeSkillPath(p string) bool {
	return p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || strings.Contains(p, "..")
}

type manifestEntry struct {
	SHA256      string `json:"sha256"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"` // used by the skill download budget; 0 when absent
}

// manifestEntries loads + parses a site's current-version manifest into a
// path→entry map.
func (svc *Service) manifestEntries(ctx context.Context, orgID string, site store.Site) (map[string]manifestEntry, error) {
	raw, err := svc.Blobs.GetManifest(ctx, orgID, site.ID, *site.CurrentVersionID)
	if err != nil {
		return nil, err
	}
	return parseManifest(raw)
}

// skillManifestEntries loads + parses a skill's current-version manifest into a
// path→entry map.
func (svc *Service) skillManifestEntries(ctx context.Context, orgID string, sk store.Skill) (map[string]manifestEntry, error) {
	raw, err := svc.Blobs.GetSkillManifest(ctx, orgID, sk.ID, *sk.CurrentVersionID)
	if err != nil {
		return nil, err
	}
	return parseManifest(raw)
}

func parseManifest(raw []byte) (map[string]manifestEntry, error) {
	var parsed struct {
		Files map[string]manifestEntry `json:"files"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	return parsed.Files, nil
}

// --- SDK registration -------------------------------------------------------

// inputSchema builds the JSON Schema for a tool input and collapses nilable Go
// types down to plain types. The SDK infers a Go slice as {"type":["null","array"]}
// and a *bool as {"type":["null","boolean"]} (both kinds are nilable). Some MCP
// clients can't serialize a union-typed argument and coerce it to a string before
// sending — so e.g. deploy_site's `files` array arrived as a string and failed
// validation ("has type string, want one of null, array"). Publishing plain single
// types ("array", "boolean") fixes that. Used for any tool whose input has a slice
// or pointer field; scalar-only inputs are unaffected.
func inputSchema[In any]() *jsonschema.Schema {
	s, err := jsonschema.For[In](nil)
	if err != nil {
		// Schema generation is deterministic over a fixed type; a failure here is a
		// programming error, surfaced at startup rather than per request.
		panic(err)
	}
	dropNullUnions(s)
	return s
}

// dropNullUnions rewrites every "[null, T]" type union in the schema tree to plain
// "T" (and drops "null" from larger unions), recursing into nested schemas.
func dropNullUnions(s *jsonschema.Schema) {
	if s == nil {
		return
	}
	if len(s.Types) > 0 {
		keep := make([]string, 0, len(s.Types))
		for _, t := range s.Types {
			if t != "null" {
				keep = append(keep, t)
			}
		}
		if len(keep) == 1 {
			s.Type, s.Types = keep[0], nil
		} else {
			s.Types = keep
		}
	}
	dropNullUnions(s.Items)
	dropNullUnions(s.AdditionalProperties)
	for _, p := range s.Properties {
		dropNullUnions(p)
	}
	for _, p := range s.PrefixItems {
		dropNullUnions(p)
	}
	for _, p := range s.AnyOf {
		dropNullUnions(p)
	}
	for _, p := range s.OneOf {
		dropNullUnions(p)
	}
	for _, p := range s.Defs {
		dropNullUnions(p)
	}
}

// Register wires the tools onto the MCP server. The read tools are always present;
// the WRITE tools (create_site, set_site_access) are registered only when a control-
// plane client is configured (the MCP server has an API_URL).
func Register(server *mcpsdk.Server, svc *Service) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "list_sites",
		Description: "List the deployed sites in your Dropway organization (slug, access mode, whether live, URL).",
	}, svc.listSitesHandler)
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "list_files",
		Description: "List the files of a site's currently published version. Args: site (slug).",
	}, svc.listFilesHandler)
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "read_file",
		Description: "Read the contents of one file in a site's current version. Args: site (slug), path (from list_files).",
	}, svc.readFileHandler)
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "download_site",
		Description: "Download every file of a site's current version at once (path + contents). Args: site (slug). Large sites are truncated to a size cap.",
	}, svc.downloadSiteHandler)
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "list_skills",
		Description: "List the shared Claude skills in your Dropway organization. Args (all optional): query (text filter), folder (folder slug), presets_only. Note: Dropway's preset skills appear only after the org's first skills use through the API, dashboard, or CLI — MCP reads cannot trigger that seeding, but upload_skill (a write) does.",
	}, svc.listSkillsHandler)
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "download_skill",
		Description: "Download every file of a shared skill (path + contents, utf8 or base64). Args: name (slug from list_skills). Write the files into .claude/skills/<name>/ preserving each file's relative path; refuse any path containing '..'.",
	}, svc.downloadSkillHandler)
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "download_skill_folder",
		Description: "Download every skill in a skill folder at once. Args: folder (folder slug). Write each skill's files into .claude/skills/<name>/; refuse any path containing '..'. The response is capped in size — skills marked truncated carry no files, fetch each of those with download_skill.",
	}, svc.downloadSkillFolderHandler)

	if svc.API == nil {
		return // no control-plane client → read-only deployment, no write tools
	}
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "create_site",
		Description: "Create a new site in your Dropway organization. Args: slug, access_mode (optional: 'public' or 'org_only'). Subject to your plan's site limit.",
	}, svc.createSiteHandler)
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "set_site_access",
		Description: "Change a site's sharing/permissions. Args: site (slug), mode ('public'|'org_only'|'password'|'allowlist'), password (only for mode=password). Owner/admin only.",
	}, svc.setAccessHandler)
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "deploy_site",
		Description: "Upload files to a site and publish them (go live). Args: site (slug), files ([{path, text or base64, content_type?}]), publish (default true). Include an index.html for the site root. Returns the live URL.",
		// Explicit schema so `files` is a plain array and `publish` a plain boolean
		// (not "[null, …]" unions that some clients coerce to strings).
		InputSchema: inputSchema[deploySiteIn](),
	}, svc.deploySiteHandler)
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "upload_skill",
		Description: "Share a Claude skill with your Dropway organization (create or replace — uploads are latest-only). Args: name (slug), files ([{path, content, encoding? 'utf8'|'base64'}], must include a root SKILL.md), title (optional), folders (optional folder slugs, applied on first create). Max 200 files / 5 MiB total.",
		// Explicit schema so `files`/`folders` are plain arrays (see deploy_site).
		InputSchema: inputSchema[uploadSkillIn](),
	}, svc.uploadSkillHandler)
}

// logTool emits a structured record of a tool invocation — the authenticated
// tenant (user + org) plus any tool-specific metadata (site, path, mode, …). This
// is the per-call audit trail of MCP activity; keep the attrs cheap (no payloads).
func logTool(ctx context.Context, tool string, attrs ...any) {
	t, _ := auth.TenantFromContext(ctx)
	base := []any{"tool", tool, "user_id", t.UserID, "org_id", t.OrgID}
	slog.Info("mcp tool call", append(base, attrs...)...)
}

func (svc *Service) listSitesHandler(ctx context.Context, _ *mcpsdk.CallToolRequest, _ listSitesIn) (*mcpsdk.CallToolResult, listSitesOut, error) {
	logTool(ctx, "list_sites")
	t, ok := auth.TenantFromContext(ctx)
	if !ok {
		return nil, listSitesOut{}, ErrNoTenant
	}
	out, err := svc.ListSites(ctx, t)
	return nil, out, err
}

func (svc *Service) listFilesHandler(ctx context.Context, _ *mcpsdk.CallToolRequest, in listFilesIn) (*mcpsdk.CallToolResult, listFilesOut, error) {
	logTool(ctx, "list_files", "site", in.Site)
	t, ok := auth.TenantFromContext(ctx)
	if !ok {
		return nil, listFilesOut{}, ErrNoTenant
	}
	out, err := svc.ListFiles(ctx, t, in.Site)
	return nil, out, err
}

func (svc *Service) readFileHandler(ctx context.Context, _ *mcpsdk.CallToolRequest, in readFileIn) (*mcpsdk.CallToolResult, readFileOut, error) {
	logTool(ctx, "read_file", "site", in.Site, "path", in.Path)
	t, ok := auth.TenantFromContext(ctx)
	if !ok {
		return nil, readFileOut{}, ErrNoTenant
	}
	out, err := svc.ReadFile(ctx, t, in.Site, in.Path)
	return nil, out, err
}

func (svc *Service) downloadSiteHandler(ctx context.Context, _ *mcpsdk.CallToolRequest, in downloadSiteIn) (*mcpsdk.CallToolResult, downloadSiteOut, error) {
	logTool(ctx, "download_site", "site", in.Site)
	t, ok := auth.TenantFromContext(ctx)
	if !ok {
		return nil, downloadSiteOut{}, ErrNoTenant
	}
	out, err := svc.DownloadSite(ctx, t, in.Site)
	return nil, out, err
}

func (svc *Service) createSiteHandler(ctx context.Context, _ *mcpsdk.CallToolRequest, in createSiteIn) (*mcpsdk.CallToolResult, createSiteOut, error) {
	logTool(ctx, "create_site", "slug", in.Slug, "access_mode", in.AccessMode)
	token, ok := auth.TokenFromContext(ctx)
	if !ok || token == "" {
		return nil, createSiteOut{}, ErrNoToken
	}
	out, err := svc.CreateSite(ctx, token, in.Slug, in.AccessMode)
	return nil, out, err
}

func (svc *Service) setAccessHandler(ctx context.Context, _ *mcpsdk.CallToolRequest, in setAccessIn) (*mcpsdk.CallToolResult, setAccessOut, error) {
	logTool(ctx, "set_site_access", "site", in.Site, "mode", in.Mode)
	t, ok := auth.TenantFromContext(ctx)
	if !ok {
		return nil, setAccessOut{}, ErrNoTenant
	}
	token, ok := auth.TokenFromContext(ctx)
	if !ok || token == "" {
		return nil, setAccessOut{}, ErrNoToken
	}
	out, err := svc.SetAccess(ctx, t, token, in.Site, in.Mode, in.Password)
	return nil, out, err
}

func (svc *Service) listSkillsHandler(ctx context.Context, _ *mcpsdk.CallToolRequest, in listSkillsIn) (*mcpsdk.CallToolResult, listSkillsOut, error) {
	logTool(ctx, "list_skills", "query", in.Query, "folder", in.Folder, "presets_only", in.PresetsOnly)
	t, ok := auth.TenantFromContext(ctx)
	if !ok {
		return nil, listSkillsOut{}, ErrNoTenant
	}
	out, err := svc.ListSkills(ctx, t, in.Query, in.Folder, in.PresetsOnly)
	return nil, out, err
}

func (svc *Service) downloadSkillHandler(ctx context.Context, _ *mcpsdk.CallToolRequest, in downloadSkillIn) (*mcpsdk.CallToolResult, downloadSkillOut, error) {
	logTool(ctx, "download_skill", "name", in.Name)
	t, ok := auth.TenantFromContext(ctx)
	if !ok {
		return nil, downloadSkillOut{}, ErrNoTenant
	}
	out, err := svc.DownloadSkill(ctx, t, in.Name)
	return nil, out, err
}

func (svc *Service) downloadSkillFolderHandler(ctx context.Context, _ *mcpsdk.CallToolRequest, in downloadSkillFolderIn) (*mcpsdk.CallToolResult, downloadSkillFolderOut, error) {
	logTool(ctx, "download_skill_folder", "folder", in.Folder)
	t, ok := auth.TenantFromContext(ctx)
	if !ok {
		return nil, downloadSkillFolderOut{}, ErrNoTenant
	}
	out, err := svc.DownloadSkillFolder(ctx, t, in.Folder)
	return nil, out, err
}

func (svc *Service) uploadSkillHandler(ctx context.Context, _ *mcpsdk.CallToolRequest, in uploadSkillIn) (*mcpsdk.CallToolResult, uploadSkillOut, error) {
	logTool(ctx, "upload_skill", "name", in.Name, "files", len(in.Files))
	t, ok := auth.TenantFromContext(ctx)
	if !ok {
		return nil, uploadSkillOut{}, ErrNoTenant
	}
	token, ok := auth.TokenFromContext(ctx)
	if !ok || token == "" {
		return nil, uploadSkillOut{}, ErrNoToken
	}
	out, err := svc.UploadSkill(ctx, t, token, in)
	return nil, out, err
}

func (svc *Service) deploySiteHandler(ctx context.Context, _ *mcpsdk.CallToolRequest, in deploySiteIn) (*mcpsdk.CallToolResult, deploySiteOut, error) {
	logTool(ctx, "deploy_site", "site", in.Site, "files", len(in.Files))
	t, ok := auth.TenantFromContext(ctx)
	if !ok {
		return nil, deploySiteOut{}, ErrNoTenant
	}
	token, ok := auth.TokenFromContext(ctx)
	if !ok || token == "" {
		return nil, deploySiteOut{}, ErrNoToken
	}
	publish := true
	if in.Publish != nil {
		publish = *in.Publish
	}
	out, err := svc.DeploySite(ctx, t, token, in.Site, in.Files, publish)
	return nil, out, err
}
