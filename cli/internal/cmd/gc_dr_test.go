package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/danielpang/shipped/services/api/ops"
)

// fakeOps is an in-memory opsRunner for the gc/dr command tests (no live DB/R2).
type fakeOps struct {
	gcParams   ops.GCParams
	gcResults  []ops.GCResult
	gcErr      error
	rebuild    ops.RebuildResult
	rebuildErr error
	closed     bool
}

func (f *fakeOps) GC(_ context.Context, p ops.GCParams) ([]ops.GCResult, error) {
	f.gcParams = p
	return f.gcResults, f.gcErr
}

func (f *fakeOps) RebuildProjection(_ context.Context) (ops.RebuildResult, error) {
	return f.rebuild, f.rebuildErr
}

func (f *fakeOps) Close() { f.closed = true }

func factoryFor(f *fakeOps) func(context.Context) (opsRunner, error) {
	return func(context.Context) (opsRunner, error) { return f, nil }
}

func TestGCCmd_PrintsSummaryAndPassesFlags(t *testing.T) {
	f := &fakeOps{gcResults: []ops.GCResult{
		{OrgID: "org-1", ScannedBlobs: 10, RetainedVersions: 3, ReferencedBlobs: 7, OrphanCount: 3, Deleted: 3},
	}}
	cmd := newGCCmd(factoryFor(f))
	cmd.SetArgs([]string{"--org", "org-1", "--keep", "2", "--dry-run"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if f.gcParams.OrgID != "org-1" || f.gcParams.KeepLastN != 2 || !f.gcParams.DryRun {
		t.Fatalf("flags not passed through: %+v", f.gcParams)
	}
	if !f.closed {
		t.Error("gc must Close the runner")
	}
	s := out.String()
	if !strings.Contains(s, "org org-1") || !strings.Contains(s, "would delete (dry run) 3 orphan") {
		t.Fatalf("unexpected gc output:\n%s", s)
	}
}

func TestGCCmd_ErrorSurfaces(t *testing.T) {
	f := &fakeOps{gcErr: errors.New("boom")}
	cmd := newGCCmd(factoryFor(f))
	cmd.SetArgs(nil)
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected gc error, got %v", err)
	}
}

func TestDRRebuildCmd_PrintsSummary(t *testing.T) {
	f := &fakeOps{rebuild: ops.RebuildResult{Orgs: 2, Routes: 5}}
	cmd := newDRCmd(factoryFor(f))
	cmd.SetArgs([]string{"rebuild"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !f.closed {
		t.Error("dr rebuild must Close the runner")
	}
	if s := out.String(); !strings.Contains(s, "5 route(s) across 2 org(s)") {
		t.Fatalf("unexpected dr output:\n%s", s)
	}
}

func TestDRRebuildCmd_ErrorSurfaces(t *testing.T) {
	f := &fakeOps{rebuildErr: errors.New("kaboom")}
	cmd := newDRCmd(factoryFor(f))
	cmd.SetArgs([]string{"rebuild"})
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "kaboom") {
		t.Fatalf("expected rebuild error, got %v", err)
	}
}
