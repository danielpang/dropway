// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package skillseeds embeds the default preset skills every org's skill
// folders start with (engineering / product / marketing starters). The API
// materializes them lazily per org on the first skills touch (see
// store.SeedOrgSkills): blobs staged content-addressed like any upload, one
// finalized version, folder membership flagged is_preset.
//
// Each seed lives under seeds/<slug>/: a preset.json ({title, description,
// folder}) plus the skill's files (a root SKILL.md and any supporting files).
// Content updates here reach NEW orgs only — an already-seeded org keeps its
// admins' curation (org_meta.skills_seeded guards re-seeding).
package skillseeds

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/danielpang/dropway/internal/contenttype"
	"github.com/danielpang/dropway/internal/manifest"
	"github.com/danielpang/dropway/internal/skillspec"
)

//go:embed seeds
var seedsFS embed.FS

// File is one file of a seed skill, hashed and ready to stage as a blob.
type File struct {
	Path        string
	Content     []byte
	SHA256      string
	Size        int64
	ContentType string
}

// Seed is one embedded preset skill.
type Seed struct {
	Slug        string
	Title       string
	Description string
	FolderSlug  string
	Files       []File
	// Digest is the whole-skill content address (internal/manifest.Digest over
	// the files), i.e. the version's content_hash.
	Digest string
	// TotalSize sums the distinct blobs' sizes (a seed never repeats content,
	// so this equals the plain file-size sum).
	TotalSize int64
}

type presetMeta struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Folder      string `json:"folder"`
}

// Load parses the embedded seed set. It is deterministic (slug-sorted) and
// validates each seed against the same skillspec rules real uploads face, so
// a bad seed fails at startup, not at an org's first request.
func Load() ([]Seed, error) {
	entries, err := seedsFS.ReadDir("seeds")
	if err != nil {
		return nil, fmt.Errorf("skillseeds: read seeds dir: %w", err)
	}
	var out []Seed
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		seed, err := loadSeed(e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, seed)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

func loadSeed(slug string) (Seed, error) {
	dir := "seeds/" + slug

	metaRaw, err := seedsFS.ReadFile(dir + "/preset.json")
	if err != nil {
		return Seed{}, fmt.Errorf("skillseeds: %s: missing preset.json: %w", slug, err)
	}
	var meta presetMeta
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		return Seed{}, fmt.Errorf("skillseeds: %s: parse preset.json: %w", slug, err)
	}
	if meta.Folder == "" {
		return Seed{}, fmt.Errorf("skillseeds: %s: preset.json has no folder", slug)
	}

	seed := Seed{
		Slug:        slug,
		Title:       meta.Title,
		Description: meta.Description,
		FolderSlug:  meta.Folder,
	}
	err = fs.WalkDir(seedsFS, dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(path, dir+"/")
		if rel == "preset.json" {
			return nil // seed metadata, not skill content
		}
		content, err := seedsFS.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		seed.Files = append(seed.Files, File{
			Path:        rel,
			Content:     content,
			SHA256:      hex.EncodeToString(sum[:]),
			Size:        int64(len(content)),
			ContentType: contenttype.ForPath(rel),
		})
		return nil
	})
	if err != nil {
		return Seed{}, fmt.Errorf("skillseeds: %s: %w", slug, err)
	}

	sort.Slice(seed.Files, func(i, j int) bool { return seed.Files[i].Path < seed.Files[j].Path })
	infos := make([]skillspec.FileInfo, len(seed.Files))
	manifestFiles := make([]manifest.File, len(seed.Files))
	for i, f := range seed.Files {
		infos[i] = skillspec.FileInfo{Path: f.Path, Size: f.Size}
		manifestFiles[i] = manifest.File{Path: f.Path, SHA256: f.SHA256}
		seed.TotalSize += f.Size
	}
	if err := skillspec.Validate(infos); err != nil {
		return Seed{}, fmt.Errorf("skillseeds: %s: %w", slug, err)
	}
	seed.Digest = manifest.Digest(manifestFiles)
	return seed, nil
}
