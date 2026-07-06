// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package skillspec is the shared validation vocabulary for org-shared skills:
// what makes an uploaded directory a valid skill (a root SKILL.md, bounded
// size/count, clean relative paths) and the minimal SKILL.md frontmatter
// parse the API uses to fill a skill's title/description.
//
// The API enforces these rules at BOTH upload steps (prepare rejects a bad
// manifest before any bytes move; finalize re-asserts against server-verified
// data). Clients (dashboard, CLI, MCP) pre-check cheaply for better errors,
// but the API is the boundary.
package skillspec

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// SkillMD is the required root file: a skill IS a directory with a SKILL.md
	// at its top level (frontmatter name/description + instructions).
	SkillMD = "SKILL.md"
	// MaxTotalBytes bounds a skill's total content (well under the MCP 10 MiB
	// inline download cap so a skill always round-trips through an agent).
	MaxTotalBytes = 5 << 20 // 5 MiB
	// MaxFiles bounds the file count (a skill is instructions + a few assets,
	// not a site).
	MaxFiles = 200
	// MaxFrontmatterBytes bounds how much of SKILL.md the API reads to parse
	// frontmatter on finalize.
	MaxFrontmatterBytes = 64 << 10 // 64 KiB
)

// ErrMissingSkillMD is returned when the manifest has no root SKILL.md.
var ErrMissingSkillMD = errors.New("skillspec: no SKILL.md at the skill root")

// FileInfo is the manifest slice Validate needs: path + size.
type FileInfo struct {
	Path string
	Size int64
}

// Validate asserts files describes a valid skill upload: a root SKILL.md,
// ≤ MaxFiles entries, ≤ MaxTotalBytes total, clean relative paths, no
// duplicates. The returned error is client-safe (surfaced as a 400).
func Validate(files []FileInfo) error {
	if len(files) == 0 {
		return errors.New("skillspec: manifest is empty")
	}
	if len(files) > MaxFiles {
		return fmt.Errorf("skillspec: %d files exceeds the %d-file limit", len(files), MaxFiles)
	}
	var total int64
	seen := make(map[string]struct{}, len(files))
	hasSkillMD := false
	for _, f := range files {
		if !CleanPath(f.Path) {
			return fmt.Errorf("skillspec: invalid path %q (must be a clean relative path)", f.Path)
		}
		if _, dup := seen[f.Path]; dup {
			return fmt.Errorf("skillspec: duplicate path %q", f.Path)
		}
		seen[f.Path] = struct{}{}
		if f.Path == SkillMD {
			hasSkillMD = true
		}
		if f.Size < 0 {
			return fmt.Errorf("skillspec: negative size for %q", f.Path)
		}
		total += f.Size
	}
	if !hasSkillMD {
		return ErrMissingSkillMD
	}
	if total > MaxTotalBytes {
		return fmt.Errorf("skillspec: total size %d exceeds the %d-byte limit", total, MaxTotalBytes)
	}
	return nil
}

// CleanPath reports whether p is a safe skill-relative POSIX path: non-empty,
// relative, forward slashes only, no empty/"."/".." segments, no NUL. Every
// writer (CLI pull, dashboard download, agents) also re-checks before writing
// so a manifest path can never escape the destination directory.
func CleanPath(p string) bool {
	if p == "" || len(p) > 512 || strings.ContainsAny(p, "\x00\\") {
		return false
	}
	if strings.HasPrefix(p, "/") || strings.Contains(p, "//") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// Frontmatter is the subset of SKILL.md YAML frontmatter Dropway reads.
type Frontmatter struct {
	Name        string
	Description string
}

// ParseFrontmatter extracts name/description from a SKILL.md's leading YAML
// frontmatter block (--- ... ---). It is a deliberately MINIMAL parser — the
// two fields are single-line scalars in every real skill, and the API treats a
// parse miss as a warning, never an error — so we avoid a YAML dependency for
// two keys. Quotes around values are stripped; unknown keys are ignored;
// multi-line/nested values yield empty fields.
func ParseFrontmatter(b []byte) Frontmatter {
	if len(b) > MaxFrontmatterBytes {
		b = b[:MaxFrontmatterBytes]
	}
	if !utf8.Valid(b) {
		return Frontmatter{}
	}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return Frontmatter{}
	}
	var fm Frontmatter
	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			break
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"'`)
		switch strings.TrimSpace(key) {
		case "name":
			fm.Name = val
		case "description":
			fm.Description = val
		}
	}
	return fm
}
