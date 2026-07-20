package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/danielpang/dropway/cli/internal/api"
)

// fakeReadClient is a canned ReadClient for the sites/read command tests.
type fakeReadClient struct {
	sites     []api.Site
	userID    string
	err       error
	deleted   []string // site ids passed to DeleteSite
	deleteErr error
}

func (f *fakeReadClient) ListSites(context.Context) (*api.SitesResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &api.SitesResponse{Sites: f.sites}, nil
}

func (f *fakeReadClient) Me(context.Context) (*api.MeResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &api.MeResponse{UserID: f.userID}, nil
}

func (f *fakeReadClient) DeleteSite(_ context.Context, siteID string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, siteID)
	return nil
}

func readFactoryOf(c api.ReadClient) func(string, string) api.ReadClient {
	return func(string, string) api.ReadClient { return c }
}

func newSitesFixture() *fakeReadClient {
	return &fakeReadClient{
		userID: "user_me",
		sites: []api.Site{
			{ID: "s1", Slug: "zeta", OwnerID: "user_me", AccessMode: "public", LiveURL: "https://acme.dropway.dev/zeta"},
			{ID: "s2", Slug: "alpha", OwnerID: "user_me", AccessMode: "org_only", LiveURL: "https://acme.dropway.dev/alpha"},
			{ID: "s3", Slug: "teammate", OwnerID: "user_other", AccessMode: "public", LiveURL: "https://acme.dropway.dev/teammate"},
		},
	}
}

func runSites(t *testing.T, client api.ReadClient, args ...string) (string, error) {
	t.Helper()
	t.Setenv("DROPWAY_API_KEY", "test-token") // auth.Token short-circuits to this
	cmd := newSitesCmd(readFactoryOf(client))
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

func TestSites_PersonalFiltersToOwner(t *testing.T) {
	out, err := runSites(t, newSitesFixture())
	if err != nil {
		t.Fatalf("sites: %v", err)
	}
	// Owned sites appear, sorted by slug; the teammate's does not.
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "zeta") {
		t.Errorf("expected owned sites in output:\n%s", out)
	}
	if strings.Contains(out, "teammate") {
		t.Errorf("personal view leaked a site the caller does not own:\n%s", out)
	}
	if i, j := strings.Index(out, "alpha"), strings.Index(out, "zeta"); i > j {
		t.Errorf("sites not sorted by slug:\n%s", out)
	}
	// No OWNER column in the personal view.
	if strings.Contains(out, "OWNER") {
		t.Errorf("personal view should not show the OWNER column:\n%s", out)
	}
}

func TestSites_AllShowsOrgWithOwnerLabels(t *testing.T) {
	out, err := runSites(t, newSitesFixture(), "--all")
	if err != nil {
		t.Fatalf("sites --all: %v", err)
	}
	if !strings.Contains(out, "teammate") {
		t.Errorf("--all should include every org site:\n%s", out)
	}
	if !strings.Contains(out, "OWNER") {
		t.Errorf("--all should show the OWNER column:\n%s", out)
	}
	if !strings.Contains(out, "you") {
		t.Errorf("--all should label the caller's own sites 'you':\n%s", out)
	}
}

func TestSites_EmptyPersonalHint(t *testing.T) {
	client := &fakeReadClient{
		userID: "user_me",
		sites:  []api.Site{{Slug: "x", OwnerID: "user_other", AccessMode: "public", LiveURL: "u"}},
	}
	out, err := runSites(t, client)
	if err != nil {
		t.Fatalf("sites: %v", err)
	}
	if !strings.Contains(out, "--all") {
		t.Errorf("empty personal view should hint at --all:\n%s", out)
	}
}

func TestOwnerLabel(t *testing.T) {
	if got := ownerLabel("user_me", "user_me"); got != "you" {
		t.Errorf("own site: got %q, want you", got)
	}
	if got := ownerLabel("user_0123456789abc", "user_me"); got != "user_01234…" {
		t.Errorf("long owner id: got %q", got)
	}
}

// runSitesDelete drives `dropway sites delete` with the given stdin + args.
func runSitesDelete(t *testing.T, client api.ReadClient, stdin string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("DROPWAY_API_KEY", "test-token")
	cmd := newSitesCmd(readFactoryOf(client))
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(append([]string{"delete"}, args...))
	err := cmd.Execute()
	return buf.String(), err
}

func TestSitesDelete_BySlugConfirmed(t *testing.T) {
	fc := newSitesFixture()
	out, err := runSitesDelete(t, fc, "y\n", "alpha")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(fc.deleted) != 1 || fc.deleted[0] != "s2" {
		t.Fatalf("expected s2 deleted, got %v", fc.deleted)
	}
	if !strings.Contains(out, `Deleted site "alpha"`) {
		t.Errorf("missing confirmation line:\n%s", out)
	}
}

func TestSitesDelete_AbortsWhenNotConfirmed(t *testing.T) {
	fc := newSitesFixture()
	out, err := runSitesDelete(t, fc, "n\n", "alpha")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(fc.deleted) != 0 {
		t.Fatalf("site deleted despite declining: %v", fc.deleted)
	}
	if !strings.Contains(out, "Aborted") {
		t.Errorf("expected abort message:\n%s", out)
	}
}

func TestSitesDelete_YesSkipsPromptAndAcceptsID(t *testing.T) {
	fc := newSitesFixture()
	// No stdin at all — --yes must not block on a prompt (the CI path).
	if _, err := runSitesDelete(t, fc, "", "s1", "--yes"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(fc.deleted) != 1 || fc.deleted[0] != "s1" {
		t.Fatalf("expected s1 deleted by id, got %v", fc.deleted)
	}
}

func TestSitesDelete_UnknownSite(t *testing.T) {
	fc := newSitesFixture()
	_, err := runSitesDelete(t, fc, "", "does-not-exist", "--yes")
	if err == nil {
		t.Fatal("expected an error for an unknown site")
	}
	if len(fc.deleted) != 0 {
		t.Fatalf("nothing should be deleted, got %v", fc.deleted)
	}
}
