// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package tools implements the Dropway MCP tools — list_sites, list_files,
// read_file — over a tenant's deployed documents. Every call is org-scoped: the
// tenant comes from the validated OAuth token (auth.TenantFromContext) and the
// store enforces RLS. The exported methods take the tenant explicitly so they're
// unit-testable; the SDK handlers are thin wrappers that pull it from context.
package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"unicode/utf8"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/danielpang/dropway/services/mcp/internal/auth"
	"github.com/danielpang/dropway/services/mcp/internal/store"
)

// SiteStore is the data the tools read (RLS-scoped).
type SiteStore interface {
	ListSites(ctx context.Context, t store.Tenant) ([]store.Site, error)
	SiteBySlug(ctx context.Context, t store.Tenant, slug string) (store.Site, error)
}

// Blobs fetches deploy manifests + content-addressed blobs (satisfied by
// internal/storage.Store).
type Blobs interface {
	GetManifest(ctx context.Context, orgID, siteID, versionID string) ([]byte, error)
	GetBlob(ctx context.Context, orgID, sha256 string) (io.ReadCloser, error)
}

// Service holds the tool dependencies.
type Service struct {
	Store SiteStore
	Blobs Blobs
}

// ErrNoTenant means the request reached a tool without an authenticated tenant
// (should be impossible behind the auth middleware).
var ErrNoTenant = errors.New("mcp/tools: no authenticated tenant")

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

type manifestEntry struct {
	SHA256      string `json:"sha256"`
	ContentType string `json:"content_type"`
}

// manifestEntries loads + parses a site's current-version manifest into a
// path→entry map.
func (svc *Service) manifestEntries(ctx context.Context, orgID string, site store.Site) (map[string]manifestEntry, error) {
	raw, err := svc.Blobs.GetManifest(ctx, orgID, site.ID, *site.CurrentVersionID)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Files map[string]manifestEntry `json:"files"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	return parsed.Files, nil
}

// --- SDK registration -------------------------------------------------------

// Register wires the three tools onto the MCP server.
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
}

func (svc *Service) listSitesHandler(ctx context.Context, _ *mcpsdk.CallToolRequest, _ listSitesIn) (*mcpsdk.CallToolResult, listSitesOut, error) {
	t, ok := auth.TenantFromContext(ctx)
	if !ok {
		return nil, listSitesOut{}, ErrNoTenant
	}
	out, err := svc.ListSites(ctx, t)
	return nil, out, err
}

func (svc *Service) listFilesHandler(ctx context.Context, _ *mcpsdk.CallToolRequest, in listFilesIn) (*mcpsdk.CallToolResult, listFilesOut, error) {
	t, ok := auth.TenantFromContext(ctx)
	if !ok {
		return nil, listFilesOut{}, ErrNoTenant
	}
	out, err := svc.ListFiles(ctx, t, in.Site)
	return nil, out, err
}

func (svc *Service) readFileHandler(ctx context.Context, _ *mcpsdk.CallToolRequest, in readFileIn) (*mcpsdk.CallToolResult, readFileOut, error) {
	t, ok := auth.TenantFromContext(ctx)
	if !ok {
		return nil, readFileOut{}, ErrNoTenant
	}
	out, err := svc.ReadFile(ctx, t, in.Site, in.Path)
	return nil, out, err
}
