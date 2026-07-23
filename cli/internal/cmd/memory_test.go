// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/danielpang/dropway/cli/internal/api"
)

type fakeMemoryClient struct {
	searchQuery string
	searchK     int
	searchResp  []api.Memory
	searchErr   error

	listKind   string
	listPinned bool
	listLimit  int
	listResp   []api.Memory

	addContent, addKind string
	addResp             api.Memory
	addCreated          bool

	patchID    string
	patchPatch api.MemoryPatch
	patchResp  api.Memory

	deleteID string
}

func (f *fakeMemoryClient) SearchMemories(_ context.Context, query string, k int) ([]api.Memory, error) {
	f.searchQuery, f.searchK = query, k
	return f.searchResp, f.searchErr
}
func (f *fakeMemoryClient) ListMemories(_ context.Context, kind string, pinned bool, limit int) ([]api.Memory, error) {
	f.listKind, f.listPinned, f.listLimit = kind, pinned, limit
	return f.listResp, nil
}
func (f *fakeMemoryClient) AddMemory(_ context.Context, content, kind string) (*api.Memory, bool, error) {
	f.addContent, f.addKind = content, kind
	return &f.addResp, f.addCreated, nil
}
func (f *fakeMemoryClient) PatchMemory(_ context.Context, id string, patch api.MemoryPatch) (*api.Memory, error) {
	f.patchID, f.patchPatch = id, patch
	return &f.patchResp, nil
}
func (f *fakeMemoryClient) DeleteMemory(_ context.Context, id string) error {
	f.deleteID = id
	return nil
}

func runMemory(t *testing.T, client api.MemoryClient, args ...string) string {
	t.Helper()
	t.Setenv("DROPWAY_API_KEY", "test-token")
	cmd := newMemoryCmd(func(_, _ string) api.MemoryClient { return client })
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("memory %v: %v\noutput: %s", args, err, buf.String())
	}
	return buf.String()
}

func TestMemorySearchPrintsTable(t *testing.T) {
	d := 0.31
	client := &fakeMemoryClient{searchResp: []api.Memory{
		{ID: "m1", Kind: "style", Content: "Navy palette", Pinned: true},
		{ID: "m2", Kind: "preference", Content: "Demo CTA", Distance: &d},
	}}
	out := runMemory(t, client, "search", "landing page", "-k", "5")
	if client.searchQuery != "landing page" || client.searchK != 5 {
		t.Errorf("client got (%q, %d)", client.searchQuery, client.searchK)
	}
	for _, want := range []string{"Navy palette", "pinned", "0.31", "m2"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestMemoryListFilters(t *testing.T) {
	client := &fakeMemoryClient{}
	out := runMemory(t, client, "list", "--kind", "style", "--pinned", "--limit", "10")
	if client.listKind != "style" || !client.listPinned || client.listLimit != 10 {
		t.Errorf("client got (%q, %v, %d)", client.listKind, client.listPinned, client.listLimit)
	}
	if !strings.Contains(out, "No memories yet") {
		t.Errorf("empty list message missing:\n%s", out)
	}
}

func TestMemoryAddReportsDedupe(t *testing.T) {
	client := &fakeMemoryClient{addResp: api.Memory{Kind: "preference", Content: "Demo CTA"}, addCreated: false}
	out := runMemory(t, client, "add", "Demo CTA", "--kind", "preference")
	if client.addContent != "Demo CTA" || client.addKind != "preference" {
		t.Errorf("client got (%q, %q)", client.addContent, client.addKind)
	}
	if !strings.Contains(out, "Already known") {
		t.Errorf("dedupe message missing:\n%s", out)
	}
}

func TestMemoryContextRendersBlock(t *testing.T) {
	client := &fakeMemoryClient{searchResp: []api.Memory{
		{ID: "m1", Kind: "style", Content: "Navy palette"},
		{ID: "m2", Kind: "fact", Content: "Disabled fact", Disabled: true},
	}}
	out := runMemory(t, client, "context", "pricing page")
	if !strings.Contains(out, "<company_memory>") || !strings.Contains(out, "- [style] Navy palette") {
		t.Errorf("block malformed:\n%s", out)
	}
	if strings.Contains(out, "Disabled fact") {
		t.Errorf("disabled memory leaked into context:\n%s", out)
	}
}

func TestMemoryPinUnpinRm(t *testing.T) {
	client := &fakeMemoryClient{patchResp: api.Memory{Content: "Navy palette"}}
	runMemory(t, client, "pin", "m1")
	if client.patchID != "m1" || client.patchPatch.Pinned == nil || !*client.patchPatch.Pinned {
		t.Errorf("pin sent %+v", client.patchPatch)
	}
	runMemory(t, client, "unpin", "m1")
	if client.patchPatch.Pinned == nil || *client.patchPatch.Pinned {
		t.Errorf("unpin sent %+v", client.patchPatch)
	}
	runMemory(t, client, "rm", "m2")
	if client.deleteID != "m2" {
		t.Errorf("rm sent %q", client.deleteID)
	}
}
