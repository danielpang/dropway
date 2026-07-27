// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package tools

import (
	"context"
	"testing"

	"github.com/danielpang/dropway/services/mcp/internal/apiclient"
)

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
