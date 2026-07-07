package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielpang/dropway/cli/internal/api"
)

// fakeSkillsClient records the calls it received and returns canned responses,
// simulating the create→prepare→upload→finalize and list/download server flows.
type fakeSkillsClient struct {
	skills  []api.Skill
	folders []api.SkillFolder

	createErr error // returned by CreateSkill when set
	created   *api.CreateSkillRequest

	prepareMissing []string
	prepared       api.PrepareRequest
	uploaded       map[string]int // presigned URL → bytes uploaded

	finalized        api.FinalizeRequest
	finalizedSkillID string
	finalizeWarnings []string

	downloads      map[string]*api.SkillDownload // skill id → payload
	downloadCalls  []string                      // skill ids fetched individually
	folderDownload *api.SkillFolderDownload
}

func newFakeSkillsClient() *fakeSkillsClient {
	return &fakeSkillsClient{uploaded: map[string]int{}, downloads: map[string]*api.SkillDownload{}}
}

func (f *fakeSkillsClient) CreateSkill(_ context.Context, req api.CreateSkillRequest) (*api.Skill, error) {
	f.created = &req
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &api.Skill{ID: "skill_" + req.Slug, Slug: req.Slug, Title: req.Title}, nil
}

func (f *fakeSkillsClient) ListSkills(_ context.Context, q, folder string, presets bool) (*api.SkillsResponse, error) {
	return &api.SkillsResponse{Skills: f.skills}, nil
}

func (f *fakeSkillsClient) ListSkillFolders(_ context.Context) (*api.SkillFoldersResponse, error) {
	return &api.SkillFoldersResponse{Folders: f.folders}, nil
}

func (f *fakeSkillsClient) DownloadSkill(_ context.Context, id string) (*api.SkillDownload, error) {
	f.downloadCalls = append(f.downloadCalls, id)
	dl, ok := f.downloads[id]
	if !ok {
		return nil, errors.New("fake: no download for " + id)
	}
	return dl, nil
}

func (f *fakeSkillsClient) DownloadSkillFolder(_ context.Context, id string) (*api.SkillFolderDownload, error) {
	if f.folderDownload == nil {
		return nil, errors.New("fake: no folder download")
	}
	return f.folderDownload, nil
}

func (f *fakeSkillsClient) PrepareSkillUpload(_ context.Context, id string, req api.PrepareRequest) (*api.PrepareResponse, error) {
	f.prepared = req
	uploads := map[string]string{}
	for _, sha := range f.prepareMissing {
		uploads[sha] = "https://fake-presign.local/skills/org/" + sha
	}
	return &api.PrepareResponse{Missing: f.prepareMissing, Uploads: uploads}, nil
}

func (f *fakeSkillsClient) FinalizeSkillUpload(_ context.Context, id string, req api.FinalizeRequest) (*api.SkillFinalizeResponse, error) {
	f.finalized = req
	f.finalizedSkillID = id
	return &api.SkillFinalizeResponse{VersionID: "sv_1", VersionNo: 1, Warnings: f.finalizeWarnings}, nil
}

func (f *fakeSkillsClient) UploadBlob(_ context.Context, url string, data []byte) error {
	f.uploaded[url] = len(data)
	return nil
}

func skillsFactoryOf(c api.SkillsClient) func(string, string) api.SkillsClient {
	return func(string, string) api.SkillsClient { return c }
}

// tempSkill creates a valid skill folder (root SKILL.md + one asset).
func tempSkill(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# my skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "references", "notes.md"), []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runSkills(t *testing.T, client api.SkillsClient, args ...string) (string, error) {
	t.Helper()
	t.Setenv("DROPWAY_TOKEN", "test-token") // auth.Token short-circuits to this
	cmd := newSkillsCmd(skillsFactoryOf(client))
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

// ---------------------------------------------------------------------------
// pure helpers
// ---------------------------------------------------------------------------

func TestSafeSkillPath(t *testing.T) {
	ok := []string{"SKILL.md", "references/notes.md", "a/b/c.txt", ".well-known/x"}
	for _, p := range ok {
		if err := safeSkillPath(p); err != nil {
			t.Errorf("safeSkillPath(%q) = %v, want nil", p, err)
		}
	}
	bad := []string{
		"",             // empty path
		"/etc/passwd",  // absolute
		"../evil",      // traversal up front
		"a/../../evil", // traversal mid-path
		"a/..",         // traversal at end
		`a\b`,          // backslash (Windows separator smuggling)
		"a//b",         // empty segment
		"a/./b",        // "." segment
		".",            // bare dot
		"..",           // bare dotdot
	}
	for _, p := range bad {
		if err := safeSkillPath(p); err == nil {
			t.Errorf("safeSkillPath(%q) = nil, want an error", p)
		}
	}
}

func TestResolveFolderIDs(t *testing.T) {
	folders := []api.SkillFolder{
		{ID: "f1", Slug: "writing"},
		{ID: "f2", Slug: "coding"},
	}
	ids, err := resolveFolderIDs(folders, []string{"coding", "writing"})
	if err != nil {
		t.Fatalf("resolveFolderIDs: %v", err)
	}
	if len(ids) != 2 || ids[0] != "f2" || ids[1] != "f1" {
		t.Errorf("ids = %v, want [f2 f1]", ids)
	}

	_, err = resolveFolderIDs(folders, []string{"nope"})
	if err == nil {
		t.Fatal("unknown folder slug should error")
	}
	// The error must list the available slugs so the user can fix the flag.
	if !strings.Contains(err.Error(), "coding") || !strings.Contains(err.Error(), "writing") {
		t.Errorf("error should list available folder slugs, got: %v", err)
	}
}

func TestMatchSkillSlug_ExactOnly(t *testing.T) {
	skills := []api.Skill{
		{ID: "s1", Slug: "code-review"},
		{ID: "s2", Slug: "review"},
	}
	// The list API's q= is a substring filter, so both come back for "review";
	// the match must still pick the EXACT slug.
	got, ok := matchSkillSlug(skills, "review")
	if !ok || got.ID != "s2" {
		t.Errorf("matchSkillSlug = %+v, %v; want exact match s2", got, ok)
	}
	if _, ok := matchSkillSlug(skills, "revi"); ok {
		t.Error("a substring must not match")
	}
}

// ---------------------------------------------------------------------------
// skills push
// ---------------------------------------------------------------------------

// TestSkillsPush_RequiresRootSkillMD proves push fails locally, before any
// network call, when the folder has no root SKILL.md.
func TestSkillsPush_RequiresRootSkillMD(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fc := newFakeSkillsClient()
	_, err := runSkills(t, fc, "push", dir, "--name", "my-skill")
	if err == nil || !strings.Contains(err.Error(), "SKILL.md") {
		t.Fatalf("err = %v, want a SKILL.md pre-check error", err)
	}
	if fc.created != nil {
		t.Error("CreateSkill must not be called when the pre-check fails")
	}
}

// TestSkillsPush_FullFlow drives create→prepare→upload→finalize against the
// fake client and asserts each step ran with the right data.
func TestSkillsPush_FullFlow(t *testing.T) {
	dir := tempSkill(t)
	fc := newFakeSkillsClient()
	fc.folders = []api.SkillFolder{{ID: "f1", Slug: "writing"}}
	skillSHA := sha256Hex(t, filepath.Join(dir, "SKILL.md"))
	fc.prepareMissing = []string{skillSHA}

	out, err := runSkills(t, fc, "push", dir, "--name", "my-skill", "--title", "My Skill", "--folder", "writing")
	if err != nil {
		t.Fatalf("skills push: %v\n%s", err, out)
	}

	if fc.created == nil || fc.created.Slug != "my-skill" || fc.created.Title != "My Skill" {
		t.Errorf("created = %+v", fc.created)
	}
	// Folder slug resolved to its ID before create.
	if len(fc.created.Folders) != 1 || fc.created.Folders[0] != "f1" {
		t.Errorf("created folders = %v, want [f1]", fc.created.Folders)
	}
	if len(fc.prepared.Manifest) != 2 {
		t.Errorf("prepared manifest = %+v, want 2 files", fc.prepared.Manifest)
	}
	// The missing blob was uploaded with the file's bytes.
	if got := fc.uploaded["https://fake-presign.local/skills/org/"+skillSHA]; got != len("# my skill") {
		t.Errorf("uploaded bytes = %d", got)
	}
	if fc.finalizedSkillID != "skill_my-skill" || fc.finalized.Digest == "" {
		t.Errorf("finalize: skill id = %q, digest = %q", fc.finalizedSkillID, fc.finalized.Digest)
	}
	if !strings.Contains(out, "sv_1") || !strings.Contains(out, "(v1)") {
		t.Errorf("output should print the pushed version:\n%s", out)
	}
}

// TestSkillsPush_ExistingSlugReuses proves a "slug already in use" create error
// falls back to finding the existing skill and pushing a new version to it.
func TestSkillsPush_ExistingSlugReuses(t *testing.T) {
	dir := tempSkill(t)
	fc := newFakeSkillsClient()
	fc.createErr = errors.New(`POST /v1/skills: server returned 400: {"error":"slug already in use"}`)
	fc.skills = []api.Skill{{ID: "skill_existing", Slug: "my-skill"}}

	out, err := runSkills(t, fc, "push", dir, "--name", "my-skill")
	if err != nil {
		t.Fatalf("skills push: %v\n%s", err, out)
	}
	if fc.finalizedSkillID != "skill_existing" {
		t.Errorf("finalized skill id = %q, want the existing skill's id", fc.finalizedSkillID)
	}
	if !strings.Contains(out, "Updating existing skill") {
		t.Errorf("output should say the existing skill is being updated:\n%s", out)
	}
}

// TestSkillsPush_UnknownFolderErrors proves an unknown --folder slug fails
// before the skill is created, listing the available folders.
func TestSkillsPush_UnknownFolderErrors(t *testing.T) {
	dir := tempSkill(t)
	fc := newFakeSkillsClient()
	fc.folders = []api.SkillFolder{{ID: "f1", Slug: "writing"}}

	_, err := runSkills(t, fc, "push", dir, "--name", "my-skill", "--folder", "nope")
	if err == nil || !strings.Contains(err.Error(), "writing") {
		t.Fatalf("err = %v, want an unknown-folder error listing available slugs", err)
	}
	if fc.created != nil {
		t.Error("CreateSkill must not run with an unresolved folder")
	}
}

// TestSkillsPush_DefaultsNameFromDir proves --name defaults to the slugified
// directory basename.
func TestSkillsPush_DefaultsNameFromDir(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "My Cool Skill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# s"), 0o644); err != nil {
		t.Fatal(err)
	}
	fc := newFakeSkillsClient()
	out, err := runSkills(t, fc, "push", dir)
	if err != nil {
		t.Fatalf("skills push: %v\n%s", err, out)
	}
	if fc.created == nil || fc.created.Slug != "my-cool-skill" {
		t.Errorf("created = %+v, want slug my-cool-skill", fc.created)
	}
}

// ---------------------------------------------------------------------------
// skills list
// ---------------------------------------------------------------------------

func TestSkillsList_Table(t *testing.T) {
	fc := newFakeSkillsClient()
	fc.skills = []api.Skill{
		{
			ID: "s1", Slug: "code-review", Title: "Code Review", OwnerID: "user_abc", SizeBytes: 2048,
			Folders: []api.SkillFolderRef{{Slug: "coding"}, {Slug: "starter", IsPreset: true}},
		},
		{ID: "s2", Slug: "writing", OwnerID: presetOwnerID, SizeBytes: 100},
		// is_seeded drives the "dropway" label even when the owner id isn't the
		// zero-UUID sentinel.
		{ID: "s3", Slug: "seeded", OwnerID: "user_seed", IsSeeded: true, SizeBytes: 50},
	}
	out, err := runSkills(t, fc, "list")
	if err != nil {
		t.Fatalf("skills list: %v", err)
	}
	for _, want := range []string{"NAME", "TITLE", "FOLDERS", "SIZE", "OWNER"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q column header:\n%s", want, out)
		}
	}
	// Preset folder placements are starred; the zero-UUID owner reads "dropway".
	if !strings.Contains(out, "coding,starter*") {
		t.Errorf("folders should be comma-joined with presets starred:\n%s", out)
	}
	if !strings.Contains(out, "dropway") {
		t.Errorf("zero-UUID owner should print as dropway:\n%s", out)
	}
	if !strings.Contains(out, "user_abc") {
		t.Errorf("non-preset owner should print its id:\n%s", out)
	}
	// An is_seeded skill reads "dropway", not its raw owner id.
	if strings.Contains(out, "user_seed") {
		t.Errorf("is_seeded skill should render owner as dropway, not its id:\n%s", out)
	}
	if !strings.Contains(out, "2.0 KB") {
		t.Errorf("size should be humanized:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// skills pull
// ---------------------------------------------------------------------------

func TestSkillsPull_WritesFiles(t *testing.T) {
	fc := newFakeSkillsClient()
	fc.skills = []api.Skill{{ID: "s1", Slug: "my-skill"}}
	fc.downloads["s1"] = &api.SkillDownload{
		Slug: "my-skill", SkillID: "s1",
		Files: []api.SkillFile{
			{Path: "SKILL.md", Content: "# hi", Encoding: "utf8"},
			{Path: "assets/logo.bin", Content: base64.StdEncoding.EncodeToString([]byte{0x00, 0x01}), Encoding: "base64"},
		},
	}
	dest := t.TempDir()

	out, err := runSkills(t, fc, "pull", "my-skill", "--dest", dest)
	if err != nil {
		t.Fatalf("skills pull: %v\n%s", err, out)
	}
	md, err := os.ReadFile(filepath.Join(dest, "my-skill", "SKILL.md"))
	if err != nil || string(md) != "# hi" {
		t.Errorf("SKILL.md = %q, %v", md, err)
	}
	bin, err := os.ReadFile(filepath.Join(dest, "my-skill", "assets", "logo.bin"))
	if err != nil || !bytes.Equal(bin, []byte{0x00, 0x01}) {
		t.Errorf("base64 file not decoded: %v, %v", bin, err)
	}
	if !strings.Contains(out, "2 file(s)") {
		t.Errorf("summary should count files:\n%s", out)
	}
}

// TestSkillsPull_RejectsTraversal proves a malicious downloaded path is refused
// and nothing escapes the destination directory.
func TestSkillsPull_RejectsTraversal(t *testing.T) {
	fc := newFakeSkillsClient()
	fc.skills = []api.Skill{{ID: "s1", Slug: "evil"}}
	fc.downloads["s1"] = &api.SkillDownload{
		Slug: "evil", SkillID: "s1",
		Files: []api.SkillFile{{Path: "../../outside.txt", Content: "boom", Encoding: "utf8"}},
	}
	dest := filepath.Join(t.TempDir(), "skills")

	_, err := runSkills(t, fc, "pull", "evil", "--dest", dest)
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("err = %v, want a path-safety refusal", err)
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(dest), "outside.txt")); statErr == nil {
		t.Error("traversal file escaped the destination")
	}
}

func TestSkillsPull_UnknownSkillErrors(t *testing.T) {
	fc := newFakeSkillsClient()
	fc.skills = []api.Skill{{ID: "s1", Slug: "code-review"}} // substring hit, not exact
	_, err := runSkills(t, fc, "pull", "review")
	if err == nil || !strings.Contains(err.Error(), "no skill named") {
		t.Fatalf("err = %v, want a not-found error (exact match only)", err)
	}
}

// TestSkillsPull_Folder_FetchesTruncated proves a bulk folder pull writes the
// inline skills and re-fetches truncated ones individually.
func TestSkillsPull_Folder_FetchesTruncated(t *testing.T) {
	fc := newFakeSkillsClient()
	fc.folders = []api.SkillFolder{{ID: "f1", Slug: "writing"}}
	fc.folderDownload = &api.SkillFolderDownload{
		Folder: api.SkillFolder{ID: "f1", Slug: "writing"},
		Skills: []api.SkillDownload{
			{Slug: "small", SkillID: "s1", Files: []api.SkillFile{{Path: "SKILL.md", Content: "small", Encoding: "utf8"}}},
			{Slug: "big", SkillID: "s2", Truncated: true},
		},
	}
	fc.downloads["s2"] = &api.SkillDownload{
		Slug: "big", SkillID: "s2",
		Files: []api.SkillFile{{Path: "SKILL.md", Content: "big", Encoding: "utf8"}},
	}
	dest := t.TempDir()

	out, err := runSkills(t, fc, "pull", "--folder", "writing", "--dest", dest)
	if err != nil {
		t.Fatalf("skills pull --folder: %v\n%s", err, out)
	}
	if len(fc.downloadCalls) != 1 || fc.downloadCalls[0] != "s2" {
		t.Errorf("download calls = %v, want just the truncated skill s2", fc.downloadCalls)
	}
	for _, name := range []string{"small", "big"} {
		b, err := os.ReadFile(filepath.Join(dest, name, "SKILL.md"))
		if err != nil || string(b) != name {
			t.Errorf("%s/SKILL.md = %q, %v", name, b, err)
		}
	}
	if !strings.Contains(out, "2 skill(s)") {
		t.Errorf("summary should count skills:\n%s", out)
	}
}

// TestSkillsPull_RequiresExactlyOneTarget proves name and --folder are mutually
// exclusive and one of them is required.
func TestSkillsPull_RequiresExactlyOneTarget(t *testing.T) {
	fc := newFakeSkillsClient()
	if _, err := runSkills(t, fc, "pull"); err == nil {
		t.Error("pull with no target should error")
	}
	if _, err := runSkills(t, fc, "pull", "x", "--folder", "y"); err == nil {
		t.Error("pull with both a name and --folder should error")
	}
}

// TestSkillsPull_WritesVersionRecord proves pull writes the .dropway.json sidecar
// recording the pulled version, which `check` then reads.
func TestSkillsPull_WritesVersionRecord(t *testing.T) {
	fc := newFakeSkillsClient()
	fc.skills = []api.Skill{{ID: "s1", Slug: "writing", Version: 2}}
	fc.downloads["s1"] = &api.SkillDownload{
		Slug: "writing", SkillID: "s1", Version: 2,
		Files: []api.SkillFile{{Path: "SKILL.md", Content: "# writing", Encoding: "utf8"}},
	}
	dest := t.TempDir()
	if _, err := runSkills(t, fc, "pull", "writing", "--dest", dest); err != nil {
		t.Fatalf("pull: %v", err)
	}
	rec, ok := readSkillRecord(filepath.Join(dest, "writing"))
	if !ok || rec.Slug != "writing" || rec.SkillID != "s1" || rec.Version != 2 {
		t.Fatalf("version record wrong: %+v ok=%v", rec, ok)
	}
}

// TestSkillsCheck_ReportsAndUpdates proves `check` flags an outdated pulled skill
// and `check --update` re-pulls it (bumping the recorded version).
func TestSkillsCheck_ReportsAndUpdates(t *testing.T) {
	dest := t.TempDir()
	// Simulate a previously-pulled skill at v1.
	if err := os.MkdirAll(filepath.Join(dest, "writing"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeSkillRecord(filepath.Join(dest, "writing"), skillRecord{Slug: "writing", SkillID: "s1", Version: 1}); err != nil {
		t.Fatal(err)
	}

	fc := newFakeSkillsClient()
	fc.skills = []api.Skill{{ID: "s1", Slug: "writing", Version: 3}} // org moved ahead
	fc.downloads["s1"] = &api.SkillDownload{
		Slug: "writing", SkillID: "s1", Version: 3,
		Files: []api.SkillFile{{Path: "SKILL.md", Content: "# writing v3", Encoding: "utf8"}},
	}

	// Report-only: names the outdated skill, doesn't re-pull.
	out, err := runSkills(t, fc, "check", "--dest", dest)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !strings.Contains(out, "writing") || !strings.Contains(out, "v1") || !strings.Contains(out, "v3") {
		t.Errorf("report should name the outdated skill + versions:\n%s", out)
	}
	if len(fc.downloadCalls) != 0 {
		t.Errorf("report-only check must not download, got %v", fc.downloadCalls)
	}

	// --update re-pulls and rewrites the record to v3.
	if _, err := runSkills(t, fc, "check", "--dest", dest, "--update"); err != nil {
		t.Fatalf("check --update: %v", err)
	}
	rec, _ := readSkillRecord(filepath.Join(dest, "writing"))
	if rec.Version != 3 {
		t.Errorf("record should be bumped to v3, got %d", rec.Version)
	}
}

// TestPlanSkillUpdates unit-tests the pure comparison.
func TestPlanSkillUpdates(t *testing.T) {
	records := []skillRecord{
		{Slug: "behind", Version: 1},
		{Slug: "current", Version: 5},
		{Slug: "gone", Version: 2},
	}
	current := map[string]api.Skill{
		"behind":  {Slug: "behind", Version: 4},
		"current": {Slug: "current", Version: 5},
		// "gone" absent → skipped.
	}
	got := planSkillUpdates(records, current)
	if len(got) != 1 || got[0].Slug != "behind" || got[0].Have != 1 || got[0].Want != 4 {
		t.Fatalf("planSkillUpdates = %+v, want only behind 1→4", got)
	}
}
