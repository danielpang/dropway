// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package tools

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/danielpang/dropway/services/mcp/internal/apiclient"
	"github.com/danielpang/dropway/services/mcp/internal/auth"
	"github.com/danielpang/dropway/services/mcp/internal/store"
)

func unconfiguredMemoryErr() error {
	return &apiclient.Error{Status: http.StatusServiceUnavailable, Message: "org memory is not configured"}
}

func TestSearchMemoryForwardsTokenAndArgs(t *testing.T) {
	d := 0.2
	api := &fakeAPI{memSearchResp: []apiclient.Memory{
		{ID: "m1", Kind: "style", Content: "Navy palette", Pinned: true},
		{ID: "m2", Kind: "preference", Content: "Demo CTA everywhere", Distance: &d},
	}}
	svc := &Service{API: api}
	out, err := svc.SearchMemory(context.Background(), "tok-123", "landing page colors", 5)
	if err != nil {
		t.Fatal(err)
	}
	if api.memSearchToken != "tok-123" || api.memSearchQuery != "landing page colors" || api.memSearchK != 5 {
		t.Errorf("forwarded (%q,%q,%d)", api.memSearchToken, api.memSearchQuery, api.memSearchK)
	}
	if len(out.Memories) != 2 || !out.Memories[0].Pinned || out.Memories[1].Distance == nil {
		t.Errorf("out = %+v", out)
	}
}

func TestSearchMemoryRequiresQuery(t *testing.T) {
	svc := &Service{API: &fakeAPI{}}
	if _, err := svc.SearchMemory(context.Background(), "tok", "   ", 0); err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestListMemoriesForwards(t *testing.T) {
	api := &fakeAPI{memListResp: []apiclient.Memory{{ID: "m1", Kind: "fact", Content: "Acme sells rockets"}}}
	svc := &Service{API: api}
	out, err := svc.ListMemories(context.Background(), "tok", 25)
	if err != nil {
		t.Fatal(err)
	}
	if api.memListToken != "tok" || api.memListLimit != 25 || len(out.Memories) != 1 {
		t.Errorf("forwarded (%q,%d), out %+v", api.memListToken, api.memListLimit, out)
	}
}

func TestAddMemoryForwardsAndReportsDedupe(t *testing.T) {
	api := &fakeAPI{memAddResp: apiclient.Memory{ID: "m9"}, memAddCreated: false}
	svc := &Service{API: api}
	out, err := svc.AddMemory(context.Background(), "tok", "Prod domain is acme.dev", "fact", "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if api.memAddToken != "tok" || api.memAddContent != "Prod domain is acme.dev" || api.memAddKind != "fact" || api.memAddSource != "claude-code" {
		t.Errorf("forwarded %+v", api)
	}
	if out.ID != "m9" || out.Created {
		t.Errorf("out = %+v", out)
	}
}

func TestAddMemoryRequiresContent(t *testing.T) {
	svc := &Service{API: &fakeAPI{}}
	if _, err := svc.AddMemory(context.Background(), "tok", " ", "", ""); err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestSearchMemoryUnconfiguredFailsOpen(t *testing.T) {
	api := &fakeAPI{memSearchErr: unconfiguredMemoryErr()}
	svc := &Service{API: api}
	out, err := svc.SearchMemory(context.Background(), "tok", "brand colors", 8)
	if err != nil {
		t.Fatalf("unconfigured memory must not fail the tool, got %v", err)
	}
	if !out.Unavailable || out.Note == "" || len(out.Memories) != 0 {
		t.Fatalf("want empty unavailable result, got %+v", out)
	}
	if api.memSearchQuery != "brand colors" || api.memSearchK != 8 {
		t.Errorf("query was not forwarded before the 503: (%q, %d)", api.memSearchQuery, api.memSearchK)
	}
}

func TestListMemoriesUnconfiguredFailsOpen(t *testing.T) {
	svc := &Service{API: &fakeAPI{memListErr: unconfiguredMemoryErr()}}
	out, err := svc.ListMemories(context.Background(), "tok", 10)
	if err != nil {
		t.Fatalf("unconfigured memory must not fail the tool, got %v", err)
	}
	if !out.Unavailable || len(out.Memories) != 0 || out.Note != orgMemoryUnconfiguredNote {
		t.Fatalf("want empty unavailable result, got %+v", out)
	}
}

func TestAddMemoryUnconfiguredIsClearToolError(t *testing.T) {
	svc := &Service{API: &fakeAPI{memAddErr: unconfiguredMemoryErr()}}
	out, err := svc.AddMemory(context.Background(), "tok", "Navy is the brand color", "style", "cursor")
	if err == nil {
		t.Fatal("add must not pretend the fact was stored")
	}
	if out.ID != "" || out.Created {
		t.Fatalf("nothing should be stored, got %+v", out)
	}
	if !orgMemoryUnconfigured(err) {
		t.Fatalf("error should still identify the unconfigured 503, got %v", err)
	}
	var apiErr *apiclient.Error
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusServiceUnavailable {
		t.Fatalf("wrapped error = %v", err)
	}
	if apiErr.Message != "org memory is not configured" {
		t.Fatalf("message = %q", apiErr.Message)
	}
}

func TestMemoryToolsPropagateOtherAPIErrors(t *testing.T) {
	other := &apiclient.Error{Status: http.StatusServiceUnavailable, Message: "upstream timeout"}
	svc := &Service{API: &fakeAPI{memSearchErr: other, memListErr: other, memAddErr: other}}
	if _, err := svc.SearchMemory(context.Background(), "tok", "q", 1); err == nil || orgMemoryUnconfigured(err) {
		t.Fatalf("unrelated 503 must propagate, got %v", err)
	}
	if _, err := svc.ListMemories(context.Background(), "tok", 1); err == nil || orgMemoryUnconfigured(err) {
		t.Fatalf("unrelated 503 must propagate, got %v", err)
	}
	if _, err := svc.AddMemory(context.Background(), "tok", "fact", "fact", ""); err == nil || orgMemoryUnconfigured(err) {
		t.Fatalf("unrelated 503 must propagate, got %v", err)
	}
}

func TestSearchMemoryHandlerUnconfiguredSkipsErrorTracking(t *testing.T) {
	rep := &fakeReporter{}
	svc := &Service{API: &fakeAPI{memSearchErr: unconfiguredMemoryErr()}, Reporter: rep}
	ctx := auth.WithToken(auth.WithTenant(context.Background(), store.Tenant{OrgID: "org-9", UserID: "user-9"}), "tok")
	wrapped := capturingHandler[searchMemoryIn, memoryListOut](svc, "search_memory", svc.searchMemoryHandler)

	_, out, err := wrapped(ctx, nil, searchMemoryIn{Query: "tone", K: 3})
	if err != nil {
		t.Fatalf("handler must fail open, got %v", err)
	}
	if !out.Unavailable || len(out.Memories) != 0 {
		t.Fatalf("out = %+v", out)
	}
	if len(rep.calls) != 0 {
		t.Fatalf("unconfigured memory must not be captured, got %+v", rep.calls)
	}
}
