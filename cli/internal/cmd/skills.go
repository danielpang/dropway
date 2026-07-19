package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/danielpang/dropway/cli/internal/api"
	"github.com/danielpang/dropway/cli/internal/auth"
	"github.com/danielpang/dropway/cli/internal/manifest"
	"github.com/danielpang/dropway/internal/skillspec"
	"github.com/danielpang/dropway/internal/slug"
)

// skillRecordFile is the per-skill sidecar `dropway skills pull` writes into each
// downloaded skill folder, recording the version pulled so `skills check` can
// later compare it against the org's current version. It's a dotfile so agent
// harnesses that load SKILL.md ignore it.
const skillRecordFile = ".dropway.json"

// skillRecord is the sidecar's contents: which skill this folder is + the exact
// version that was pulled.
type skillRecord struct {
	Slug    string `json:"slug"`
	SkillID string `json:"skill_id"`
	Version int32  `json:"version"`
}

// writeSkillRecord writes the version sidecar into a pulled skill's folder
// (root = <dest>/<slug>). Best-effort provenance for `skills check`.
func writeSkillRecord(root string, rec skillRecord) error {
	body, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, skillRecordFile), append(body, '\n'), 0o644)
}

// readSkillRecord reads the version sidecar from a pulled skill's folder, if any.
func readSkillRecord(root string) (skillRecord, bool) {
	body, err := os.ReadFile(filepath.Join(root, skillRecordFile))
	if err != nil {
		return skillRecord{}, false
	}
	var rec skillRecord
	if err := json.Unmarshal(body, &rec); err != nil || rec.Slug == "" {
		return skillRecord{}, false
	}
	return rec, true
}

// presetOwnerID is the zero-UUID owner the API stamps on Dropway-seeded preset
// skills; the list view labels it "dropway" instead of printing the raw zeros.
const presetOwnerID = "00000000-0000-0000-0000-000000000000"

// defaultPullDest is where `skills pull` lands skills unless --dest overrides:
// the project-local directory agent harnesses load skills from.
const defaultPullDest = ".claude/skills"

// newSkillsCmd builds the `dropway skills` command group: push a skill folder
// to the org, list/search the org's skills, and pull skills back down into
// .claude/skills/. skillsFactory is injected so tests supply a fake
// api.SkillsClient without a live server.
func newSkillsCmd(skillsFactory func(baseURL, token string) api.SkillsClient) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Share reusable agent skills across your org (push, list, pull)",
		Long: "Org-wide skill sharing: a skill is a folder with a SKILL.md at its root\n" +
			"(plus optional supporting files). Push one to make it available to your org,\n" +
			"list what's shared, and pull skills into a project's .claude/skills/.\n" +
			"Sign in first with `dropway login` (or set " + tokenEnv + " for CI).",
	}
	cmd.AddCommand(newSkillsPushCmd(skillsFactory))
	cmd.AddCommand(newSkillsListCmd(skillsFactory))
	cmd.AddCommand(newSkillsPullCmd(skillsFactory))
	cmd.AddCommand(newSkillsCheckCmd(skillsFactory))
	return cmd
}

// ---------------------------------------------------------------------------
// dropway skills push
// ---------------------------------------------------------------------------

// newSkillsPushCmd builds `dropway skills push <dir>`: walk + hash the folder,
// create the skill (or reuse an existing one with the same slug), then run the
// prepare → upload only-missing blobs → finalize flow (same contract as deploy).
func newSkillsPushCmd(skillsFactory func(baseURL, token string) api.SkillsClient) *cobra.Command {
	var (
		name    string
		title   string
		folders []string
		baseURL string
	)

	cmd := &cobra.Command{
		Use:   "push <dir>",
		Short: "Push a skill folder to your org (creates it or uploads a new version)",
		Long: "Walk <dir> (which must have a SKILL.md at its root), compute a SHA-256 per\n" +
			"file, and push it as an org skill: create the skill if the slug is new, else\n" +
			"upload a new version to the existing one, then prepare → upload only-changed\n" +
			"blobs → finalize. --name defaults to the directory name, slugified.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			out := cmd.OutOrStdout()

			// 1. Build the manifest (local, no network) and pre-check the one
			// structural rule the server would reject anyway, with a friendlier error.
			m, err := manifest.Build(dir)
			if err != nil {
				return err
			}
			if len(m.Files) == 0 {
				return fmt.Errorf("skills push: %q contains no files to push", dir)
			}
			if !hasRootSkillMD(m) {
				return fmt.Errorf("skills push: %q has no SKILL.md at its root — a skill is a folder with a SKILL.md describing it", dir)
			}

			// 2. Resolve the slug: --name if given (normalized like deploy's --site),
			// else the directory basename slugified.
			if name == "" {
				abs, err := filepath.Abs(dir)
				if err != nil {
					return err
				}
				name = slug.Slugify(filepath.Base(abs))
				if name == "" {
					return fmt.Errorf("skills push: cannot derive a slug from %q — pass --name <slug>", dir)
				}
				fmt.Fprintf(out, "Using skill name %q (from the directory name; override with --name)\n", name)
			} else {
				normalized := slug.Slugify(name)
				if normalized == "" {
					return fmt.Errorf("skills push: --name %q has no usable slug characters (use lowercase letters, digits, and hyphens)", name)
				}
				if normalized != name {
					fmt.Fprintf(out, "Using skill name %q (normalized from %q)\n", normalized, name)
				}
				name = normalized
			}

			files := api.ManifestFromBuild(m)
			fmt.Fprintf(out, "Pushing skill %q from %q\n  %s\n\n", name, dir, m.Summary())

			// 3. Resolve auth (DROPWAY_API_KEY, else stored `dropway login` credentials).
			ctx := context.Background()
			token, err := auth.Token(ctx, baseURL)
			if err != nil {
				return fmt.Errorf("skills push: %w", err)
			}
			client := skillsFactory(baseURL, token)

			// 4. Resolve folder slugs → IDs (the create API takes IDs).
			var folderIDs []string
			if len(folders) > 0 {
				fr, err := client.ListSkillFolders(ctx)
				if err != nil {
					return fmt.Errorf("list folders: %w", err)
				}
				folderIDs, err = resolveFolderIDs(fr.Folders, folders)
				if err != nil {
					return fmt.Errorf("skills push: %w", err)
				}
			}

			// 5. Create the skill — or, when the slug is already taken, find the
			// existing skill and push a new version to it (allowed for its owner or
			// an admin; anyone else gets the server's authorization error later).
			skill, err := client.CreateSkill(ctx, api.CreateSkillRequest{Slug: name, Title: title, Folders: folderIDs})
			switch {
			case err == nil:
				fmt.Fprintf(out, "Created skill %s (%s)\n", skill.Slug, skill.ID)
			case strings.Contains(err.Error(), "slug already in use"):
				skill, err = findSkillBySlug(ctx, client, name)
				if err != nil {
					return fmt.Errorf("skills push: slug %q is already in use but the skill was not found: %w", name, err)
				}
				fmt.Fprintf(out, "Updating existing skill %s (%s)\n", skill.Slug, skill.ID)
			default:
				return fmt.Errorf("create skill: %w", err)
			}

			// 6. Prepare → upload only the missing blobs → finalize (finalize IS
			// publish for skills — they're latest-only).
			prep, err := client.PrepareSkillUpload(ctx, skill.ID, api.PrepareRequest{Manifest: files})
			if err != nil {
				return fmt.Errorf("prepare: %w", err)
			}
			fmt.Fprintf(out, "Prepared: %d/%d blob(s) need upload\n", len(prep.Missing), len(files))

			if err := uploadMissing(ctx, client, dir, m, prep); err != nil {
				return err
			}

			fin, err := client.FinalizeSkillUpload(ctx, skill.ID, api.FinalizeRequest{
				Manifest: files,
				Digest:   m.Digest,
			})
			if err != nil {
				return fmt.Errorf("finalize: %w", err)
			}
			fmt.Fprintf(out, "\n✓ Pushed skill %s — version %s (v%d) is live for your org\n", skill.Slug, fin.VersionID, fin.VersionNo)
			for _, w := range fin.Warnings {
				fmt.Fprintf(out, "  warning: %s\n", w)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "skill slug (defaults to the directory name, slugified)")
	cmd.Flags().StringVar(&title, "title", "", "human-readable skill title")
	cmd.Flags().StringSliceVar(&folders, "folder", nil, "folder slug(s) to file the skill under (comma-separated or repeated)")
	cmd.Flags().StringVar(&baseURL, "api", defaultAPIBase(), "Dropway API base URL")
	return cmd
}

// ---------------------------------------------------------------------------
// dropway skills list
// ---------------------------------------------------------------------------

// newSkillsListCmd builds `dropway skills list`: an aligned table of the org's
// skills, optionally filtered by search text, folder, or preset flag.
func newSkillsListCmd(skillsFactory func(baseURL, token string) api.SkillsClient) *cobra.Command {
	var (
		search  string
		folder  string
		presets bool
		baseURL string
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List your org's shared skills",
		Long: "List the skills shared in your active org. Filter with --search (text),\n" +
			"--folder (a folder slug), or --presets (Dropway-seeded presets only).\n" +
			"Folders marked '*' are the skill's preset placements.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := context.Background()
			out := cmd.OutOrStdout()

			token, err := auth.Token(ctx, baseURL)
			if err != nil {
				return fmt.Errorf("skills list: %w", err)
			}
			client := skillsFactory(baseURL, token)

			resp, err := client.ListSkills(ctx, search, folder, presets)
			if err != nil {
				return fmt.Errorf("skills list: %w", err)
			}
			if len(resp.Skills) == 0 {
				fmt.Fprintln(out, "No skills found. Push one with `dropway skills push <dir> --name <slug>`.")
				return nil
			}

			skills := resp.Skills
			sort.Slice(skills, func(i, j int) bool { return skills[i].Slug < skills[j].Slug })
			printSkills(out, skills)
			return nil
		},
	}

	cmd.Flags().StringVar(&search, "search", "", "text filter over skill names/titles")
	cmd.Flags().StringVar(&folder, "folder", "", "only skills in this folder (by folder slug)")
	cmd.Flags().BoolVar(&presets, "presets", false, "only Dropway preset skills")
	cmd.Flags().StringVar(&baseURL, "api", defaultAPIBase(), "Dropway API base URL")
	return cmd
}

// printSkills renders an aligned skill table (same tabwriter style as printSites).
func printSkills(out io.Writer, skills []api.Skill) {
	tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "NAME\tTITLE\tFOLDERS\tSIZE\tOWNER")
	for _, s := range skills {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			s.Slug, s.Title, folderLabels(s.Folders), manifest.HumanBytes(s.SizeBytes), skillOwnerLabel(s))
	}
	_ = tw.Flush()
}

// folderLabels comma-joins a skill's folder slugs, marking preset placements
// with a trailing '*'.
func folderLabels(folders []api.SkillFolderRef) string {
	labels := make([]string, len(folders))
	for i, f := range folders {
		labels[i] = f.Slug
		if f.IsPreset {
			labels[i] += "*"
		}
	}
	return strings.Join(labels, ",")
}

// skillOwnerLabel shows "dropway" for Dropway-seeded preset skills — preferring
// the API's is_seeded flag, but still honoring the zero-UUID owner sentinel as a
// fallback — else the raw owner id (the CLI doesn't resolve ids).
func skillOwnerLabel(s api.Skill) string {
	if s.IsSeeded || s.OwnerID == presetOwnerID {
		return "dropway"
	}
	return s.OwnerID
}

// ---------------------------------------------------------------------------
// dropway skills pull
// ---------------------------------------------------------------------------

// newSkillsPullCmd builds `dropway skills pull <name>` / `pull --folder <slug>`:
// download one skill (or a whole folder of them) and write each under
// <dest>/<skill-slug>/.
func newSkillsPullCmd(skillsFactory func(baseURL, token string) api.SkillsClient) *cobra.Command {
	var (
		folder  string
		dest    string
		baseURL string
	)

	cmd := &cobra.Command{
		Use:   "pull [<name>]",
		Short: "Pull a shared skill (or a whole folder of skills) into .claude/skills/",
		Long: "Download a skill by name — or every skill in a folder with --folder <slug> —\n" +
			"and write each one under <dest>/<skill-slug>/ (default " + defaultPullDest + "/).",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			if (name == "") == (folder == "") {
				return fmt.Errorf("skills pull: pass a skill name OR --folder <slug> (exactly one)")
			}

			ctx := context.Background()
			out := cmd.OutOrStdout()

			token, err := auth.Token(ctx, baseURL)
			if err != nil {
				return fmt.Errorf("skills pull: %w", err)
			}
			client := skillsFactory(baseURL, token)

			if name != "" {
				return pullSkill(ctx, out, client, name, dest)
			}
			return pullFolder(ctx, out, client, folder, dest)
		},
	}

	cmd.Flags().StringVar(&folder, "folder", "", "pull every skill in this folder (by folder slug)")
	cmd.Flags().StringVar(&dest, "dest", defaultPullDest, "directory to write skills under")
	cmd.Flags().StringVar(&baseURL, "api", defaultAPIBase(), "Dropway API base URL")
	return cmd
}

// pullSkill downloads one skill by exact slug and writes it under dest/<slug>/.
func pullSkill(ctx context.Context, out io.Writer, client api.SkillsClient, name, dest string) error {
	skill, err := findSkillBySlug(ctx, client, name)
	if err != nil {
		return fmt.Errorf("skills pull: %w", err)
	}
	dl, err := client.DownloadSkill(ctx, skill.ID)
	if err != nil {
		return fmt.Errorf("download %s: %w", name, err)
	}
	n, err := writeSkillFiles(dest, dl.Slug, dl.Files)
	if err != nil {
		return fmt.Errorf("skills pull: %w", err)
	}
	// Record the pulled version so `skills check` can detect later updates.
	if err := writeSkillRecord(filepath.Join(dest, dl.Slug), skillRecord{Slug: dl.Slug, SkillID: dl.SkillID, Version: dl.Version}); err != nil {
		return fmt.Errorf("skills pull: %w", err)
	}
	fmt.Fprintf(out, "✓ Pulled skill %s (v%d) → %s (%d file(s))\n", dl.Slug, dl.Version, filepath.Join(dest, dl.Slug), n)
	return nil
}

// pullFolder bulk-downloads a folder and writes every member skill under
// dest/<skill-slug>/, fetching any bulk-truncated skill individually.
func pullFolder(ctx context.Context, out io.Writer, client api.SkillsClient, folderSlug, dest string) error {
	fr, err := client.ListSkillFolders(ctx)
	if err != nil {
		return fmt.Errorf("list folders: %w", err)
	}
	ids, err := resolveFolderIDs(fr.Folders, []string{folderSlug})
	if err != nil {
		return fmt.Errorf("skills pull: %w", err)
	}
	fd, err := client.DownloadSkillFolder(ctx, ids[0])
	if err != nil {
		return fmt.Errorf("download folder %s: %w", folderSlug, err)
	}

	total := 0
	for _, sd := range fd.Skills {
		// A truncated entry carries no files (the bulk response budget ran out);
		// fetch that skill individually and write the full payload instead.
		if sd.Truncated {
			full, err := client.DownloadSkill(ctx, sd.SkillID)
			if err != nil {
				return fmt.Errorf("download %s: %w", sd.Slug, err)
			}
			sd = *full
		}
		n, err := writeSkillFiles(dest, sd.Slug, sd.Files)
		if err != nil {
			return fmt.Errorf("skills pull: %w", err)
		}
		if err := writeSkillRecord(filepath.Join(dest, sd.Slug), skillRecord{Slug: sd.Slug, SkillID: sd.SkillID, Version: sd.Version}); err != nil {
			return fmt.Errorf("skills pull: %w", err)
		}
		fmt.Fprintf(out, "  %s (v%d) → %s (%d file(s))\n", sd.Slug, sd.Version, filepath.Join(dest, sd.Slug), n)
		total += n
	}
	for _, w := range fd.Warnings {
		fmt.Fprintf(out, "  warning: %s\n", w)
	}
	fmt.Fprintf(out, "✓ Pulled %d skill(s) from folder %s (%d file(s)) into %s\n", len(fd.Skills), folderSlug, total, dest)
	return nil
}

// ---------------------------------------------------------------------------
// dropway skills check
// ---------------------------------------------------------------------------

// newSkillsCheckCmd builds `dropway skills check`: scan the pulled skills under
// <dest> (each carries a .dropway.json recording the version it was pulled at),
// compare against the org's current version, and report which are outdated.
// With --update, re-pull every outdated skill.
func newSkillsCheckCmd(skillsFactory func(baseURL, token string) api.SkillsClient) *cobra.Command {
	var (
		dest    string
		update  bool
		baseURL string
	)

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check pulled skills for updates (and optionally update them)",
		Long: "Scan the skills already pulled into <dest> (default " + defaultPullDest + "/) and\n" +
			"compare each one's recorded version against your org's current version.\n" +
			"Reports which skills are out of date; pass --update to re-pull them.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := context.Background()
			out := cmd.OutOrStdout()

			records, err := readSkillRecords(dest)
			if err != nil {
				return fmt.Errorf("skills check: %w", err)
			}
			if len(records) == 0 {
				fmt.Fprintf(out, "No pulled skills found under %s (pull one with `dropway skills pull <name>`).\n", dest)
				return nil
			}

			token, err := auth.Token(ctx, baseURL)
			if err != nil {
				return fmt.Errorf("skills check: %w", err)
			}
			client := skillsFactory(baseURL, token)

			// One list call → current version per slug (avoids an N-call fan-out).
			resp, err := client.ListSkills(ctx, "", "", false)
			if err != nil {
				return fmt.Errorf("skills check: %w", err)
			}
			current := make(map[string]api.Skill, len(resp.Skills))
			for _, s := range resp.Skills {
				current[s.Slug] = s
			}

			outdated := planSkillUpdates(records, current)
			if len(outdated) == 0 {
				fmt.Fprintf(out, "✓ All %d pulled skill(s) are up to date.\n", len(records))
				return nil
			}

			for _, u := range outdated {
				fmt.Fprintf(out, "• %s: v%d installed → v%d available\n", u.Slug, u.Have, u.Want)
			}
			if !update {
				fmt.Fprintf(out, "\n%d skill(s) out of date. Re-run with --update to pull the latest.\n", len(outdated))
				return nil
			}

			fmt.Fprintln(out, "\nUpdating…")
			for _, u := range outdated {
				if err := pullSkill(ctx, out, client, u.Slug, dest); err != nil {
					return err
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&dest, "dest", defaultPullDest, "directory the skills were pulled into")
	cmd.Flags().BoolVar(&update, "update", false, "re-pull every outdated skill")
	cmd.Flags().StringVar(&baseURL, "api", defaultAPIBase(), "Dropway API base URL")
	return cmd
}

// skillUpdate is one outdated pulled skill: the recorded (have) vs current (want)
// version.
type skillUpdate struct {
	Slug string
	Have int32
	Want int32
}

// readSkillRecords loads the .dropway.json sidecar from every immediate
// subdirectory of dest. A directory without a (valid) sidecar is skipped — it
// wasn't pulled by us, so we can't reason about its version.
func readSkillRecords(dest string) ([]skillRecord, error) {
	entries, err := os.ReadDir(dest)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var records []skillRecord
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if rec, ok := readSkillRecord(filepath.Join(dest, e.Name())); ok {
			records = append(records, rec)
		}
	}
	return records, nil
}

// planSkillUpdates returns the records whose recorded version is behind the org's
// current version. A skill no longer in the org (or with no current version) is
// skipped — there's nothing to update to.
func planSkillUpdates(records []skillRecord, current map[string]api.Skill) []skillUpdate {
	var out []skillUpdate
	for _, rec := range records {
		s, ok := current[rec.Slug]
		if !ok || s.Version == 0 {
			continue
		}
		if s.Version > rec.Version {
			out = append(out, skillUpdate{Slug: rec.Slug, Have: rec.Version, Want: s.Version})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out
}

// ---------------------------------------------------------------------------
// helpers (pure — unit-tested directly)
// ---------------------------------------------------------------------------

// hasRootSkillMD reports whether the manifest has the required root SKILL.md.
// Local pre-check so push fails with a friendly error before any network call
// (the server enforces the same rule).
func hasRootSkillMD(m *manifest.Manifest) bool {
	for _, e := range m.Files {
		if e.Path == "SKILL.md" {
			return true
		}
	}
	return false
}

// resolveFolderIDs maps folder slugs to their IDs, erroring on an unknown slug
// with the list of available ones (so the user can fix the flag without a
// second command).
func resolveFolderIDs(folders []api.SkillFolder, slugs []string) ([]string, error) {
	idBySlug := make(map[string]string, len(folders))
	available := make([]string, len(folders))
	for i, f := range folders {
		idBySlug[f.Slug] = f.ID
		available[i] = f.Slug
	}
	sort.Strings(available)

	ids := make([]string, 0, len(slugs))
	for _, s := range slugs {
		id, ok := idBySlug[s]
		if !ok {
			return nil, fmt.Errorf("no folder with slug %q (available: %s)", s, strings.Join(available, ", "))
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// matchSkillSlug picks the skill whose slug EXACTLY equals want (the list API's
// q= filter is a substring search, so "review" would also return "code-review").
func matchSkillSlug(skills []api.Skill, want string) (*api.Skill, bool) {
	for i := range skills {
		if skills[i].Slug == want {
			return &skills[i], true
		}
	}
	return nil, false
}

// findSkillBySlug resolves a skill by exact slug via the list API's text filter.
func findSkillBySlug(ctx context.Context, client api.SkillsClient, want string) (*api.Skill, error) {
	resp, err := client.ListSkills(ctx, want, "", false)
	if err != nil {
		return nil, err
	}
	skill, ok := matchSkillSlug(resp.Skills, want)
	if !ok {
		return nil, fmt.Errorf("no skill named %q in your org (try `dropway skills list`)", want)
	}
	return skill, nil
}

// safeSkillPath rejects any downloaded path that could escape the destination
// directory. It delegates to the canonical skillspec.CleanPath (absolute paths,
// empty/"."/".." segments, backslashes, NUL, >512 chars) so the CLI and the
// server share one path-safety rule. Defense in depth — the server validates
// paths too, but the CLI must not trust the wire when it's about to write to
// the local filesystem.
func safeSkillPath(p string) error {
	if !skillspec.CleanPath(p) {
		return fmt.Errorf("unsafe skill path %q", p)
	}
	return nil
}

// writeSkillFiles writes one downloaded skill's files under dest/<skillSlug>/,
// decoding base64 entries, and returns how many files it wrote. Every path
// (including the slug, which becomes a directory name) is safety-checked first.
func writeSkillFiles(dest, skillSlug string, files []api.SkillFile) (int, error) {
	if err := safeSkillPath(skillSlug); err != nil {
		return 0, fmt.Errorf("refusing skill slug %q: %w", skillSlug, err)
	}
	root := filepath.Join(dest, filepath.FromSlash(skillSlug))
	for _, f := range files {
		if err := safeSkillPath(f.Path); err != nil {
			return 0, fmt.Errorf("refusing to write %q: %w", f.Path, err)
		}
		data, err := decodeSkillFile(f)
		if err != nil {
			return 0, fmt.Errorf("decode %s: %w", f.Path, err)
		}
		target := filepath.Join(root, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return 0, err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return 0, err
		}
	}
	return len(files), nil
}

// decodeSkillFile returns a downloaded file's bytes, decoding per its encoding.
func decodeSkillFile(f api.SkillFile) ([]byte, error) {
	switch f.Encoding {
	case "", "utf8":
		return []byte(f.Content), nil
	case "base64":
		return base64.StdEncoding.DecodeString(f.Content)
	default:
		return nil, fmt.Errorf("unknown encoding %q", f.Encoding)
	}
}

// defaultSkillsClientFactory builds the real skills HTTP client.
func defaultSkillsClientFactory(baseURL, token string) api.SkillsClient {
	return &api.HTTPClient{BaseURL: baseURL, Token: token}
}
