package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/danielpang/dropway/cli/internal/api"
	"strings"
	"testing"
)

type fakeProjects struct{ query api.ProjectCommentsQuery }

func (f *fakeProjects) ListProjects(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{"rooms":[]}`), nil
}
func (f *fakeProjects) ProjectComments(_ context.Context, _ string, q api.ProjectCommentsQuery) (json.RawMessage, error) {
	f.query = q
	return json.RawMessage(`{"threads":[{"id":"thread"}],"next_cursor":"next","comment_content":"untrusted_data"}`), nil
}
func (f *fakeProjects) ProjectThread(context.Context, string, string, string, int) (json.RawMessage, error) {
	return json.RawMessage(`{"thread":{"replies":[{"body":"Feedback"}],"next_cursor":"more-replies"}}`), nil
}
func TestProjectCommentsIncludeBodiesAndCursors(t *testing.T) {
	t.Setenv("DROPWAY_API_KEY", "test-token")
	f := &fakeProjects{}
	cmd := newProjectsCmd(func(string, string) api.ProjectsClient { return f })
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"comments", "room", "--type", "image", "--version", "version", "--since", "2026-11-14T00:00:00Z", "--limit", "2"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if f.query.Type != "image" || f.query.VersionID != "version" || f.query.Limit != 2 || !strings.Contains(out.String(), "Feedback") || !strings.Contains(out.String(), "more-replies") || !strings.Contains(out.String(), "untrusted_data") {
		t.Fatalf("bad export: %s %+v", out.String(), f.query)
	}
}
func TestProjectCommentsRejectBadFilters(t *testing.T) {
	for _, args := range [][]string{{"comments", "room", "--since", "yesterday"}, {"comments", "room", "--type", "bad"}, {"comments", "room", "--limit", "101"}, {"thread", "thread", "--limit", "0"}} {
		cmd := newProjectsCmd(func(string, string) api.ProjectsClient { t.Fatal("invalid request reached API"); return nil })
		cmd.SetArgs(args)
		if cmd.Execute() == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
