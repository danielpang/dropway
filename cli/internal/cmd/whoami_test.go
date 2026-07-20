package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/danielpang/dropway/cli/internal/api"
	"github.com/danielpang/dropway/cli/internal/auth"
)

// fakeMeClient is a ReadClient that returns a canned identity for whoami.
type fakeMeClient struct{ me api.MeResponse }

func (f *fakeMeClient) ListSites(context.Context) (*api.SitesResponse, error) {
	return &api.SitesResponse{}, nil
}
func (f *fakeMeClient) Me(context.Context) (*api.MeResponse, error) { return &f.me, nil }

func TestWhoami_ReportsIdentityAndAPIKeySource(t *testing.T) {
	t.Setenv("DROPWAY_API_KEY", "dw_live_test") // auth.Token short-circuits to this
	client := &fakeMeClient{me: api.MeResponse{UserID: "user_1", OrgID: "org_1", Role: "member"}}

	cmd := newWhoamiCmd(func(string, string) api.ReadClient { return client })
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("whoami: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"user_1", "org_1", "member", "API key", tokenEnv} {
		if !strings.Contains(out, want) {
			t.Errorf("whoami output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "interactive login") {
		t.Errorf("with DROPWAY_API_KEY set, source should be the API key, not login:\n%s", out)
	}
}

func TestUsingAPIKey(t *testing.T) {
	t.Setenv("DROPWAY_API_KEY", "")
	if auth.UsingAPIKey() {
		t.Error("UsingAPIKey should be false when DROPWAY_API_KEY is empty")
	}
	t.Setenv("DROPWAY_API_KEY", "dw_live_x")
	if !auth.UsingAPIKey() {
		t.Error("UsingAPIKey should be true when DROPWAY_API_KEY is set")
	}
}
