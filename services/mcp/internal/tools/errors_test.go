// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package tools

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/danielpang/dropway/internal/errtrack"
	"github.com/danielpang/dropway/services/mcp/internal/auth"
	"github.com/danielpang/dropway/services/mcp/internal/store"
)

// fakeReporter records CaptureException calls so tests can assert a tool failure
// was emitted to the error sink (PostHog in prod).
type fakeReporter struct {
	calls []capturedException
}

type capturedException struct {
	distinctID string
	err        error
	props      map[string]any
}

func (f *fakeReporter) CaptureException(ctx context.Context, err error, props map[string]any) {
	f.calls = append(f.calls, capturedException{distinctID: errtrack.DistinctID(ctx), err: err, props: props})
}
func (f *fakeReporter) WrapSlogHandler(base slog.Handler) slog.Handler { return base }

func tenantCtx() context.Context {
	return auth.WithTenant(context.Background(), store.Tenant{OrgID: "org-9", UserID: "user-9"})
}

func TestReportToolError_CapturesWithTenantProps(t *testing.T) {
	rep := &fakeReporter{}
	svc := &Service{Reporter: rep}
	boom := errors.New("storage exploded")

	svc.reportToolError(tenantCtx(), "read_file", boom)

	if len(rep.calls) != 1 {
		t.Fatalf("want 1 captured exception, got %d", len(rep.calls))
	}
	c := rep.calls[0]
	if !errors.Is(c.err, boom) {
		t.Errorf("captured err = %v, want %v", c.err, boom)
	}
	if c.distinctID != "user-9" {
		t.Errorf("distinct id = %q, want the acting user id", c.distinctID)
	}
	if c.props["tool"] != "read_file" || c.props["org_id"] != "org-9" ||
		c.props["user_id"] != "user-9" || c.props["surface"] != "mcp" {
		t.Errorf("props wrong: %+v", c.props)
	}
}

func TestReportToolError_NoOp(t *testing.T) {
	// Nil reporter must not panic (tests / error-tracking-disabled builds).
	(&Service{}).reportToolError(tenantCtx(), "read_file", errors.New("x"))

	// A nil error is never captured.
	rep := &fakeReporter{}
	(&Service{Reporter: rep}).reportToolError(tenantCtx(), "read_file", nil)
	if len(rep.calls) != 0 {
		t.Fatalf("a nil error must not be captured, got %d calls", len(rep.calls))
	}
}

// TestCapturingHandler_ErrorPath asserts the wrapper every tool is registered
// through captures a handler's failure AND returns it unchanged to the client.
func TestCapturingHandler_ErrorPath(t *testing.T) {
	rep := &fakeReporter{}
	svc := &Service{Reporter: rep}
	boom := errors.New("nope")

	wrapped := capturingHandler[listFilesIn, listFilesOut](svc, "list_files",
		func(context.Context, *mcpsdk.CallToolRequest, listFilesIn) (*mcpsdk.CallToolResult, listFilesOut, error) {
			return nil, listFilesOut{}, boom
		})

	_, _, err := wrapped(tenantCtx(), nil, listFilesIn{})
	if !errors.Is(err, boom) {
		t.Fatalf("wrapper must return the handler error unchanged, got %v", err)
	}
	if len(rep.calls) != 1 || rep.calls[0].props["tool"] != "list_files" {
		t.Fatalf("failure not captured for the right tool: %+v", rep.calls)
	}
}

// TestCapturingHandler_SuccessPath asserts a successful call is passed through and
// nothing is emitted.
func TestCapturingHandler_SuccessPath(t *testing.T) {
	rep := &fakeReporter{}
	svc := &Service{Reporter: rep}
	want := listSitesOut{Sites: []SiteInfo{{Slug: "docs"}}}

	wrapped := capturingHandler[listSitesIn, listSitesOut](svc, "list_sites",
		func(context.Context, *mcpsdk.CallToolRequest, listSitesIn) (*mcpsdk.CallToolResult, listSitesOut, error) {
			return nil, want, nil
		})

	_, out, err := wrapped(tenantCtx(), nil, listSitesIn{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Sites) != 1 || out.Sites[0].Slug != "docs" {
		t.Errorf("result not passed through: %+v", out)
	}
	if len(rep.calls) != 0 {
		t.Errorf("a successful call must not be captured, got %d", len(rep.calls))
	}
}
