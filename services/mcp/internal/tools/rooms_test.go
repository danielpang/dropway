// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package tools

import (
	"context"
	"encoding/json"
	mcpauth "github.com/danielpang/dropway/services/mcp/internal/auth"
	"net/url"
	"strings"
	"testing"

	"github.com/danielpang/dropway/services/mcp/internal/apiclient"
)

func TestRoomReadsForwardTokenAndCursor(t *testing.T) {
	api := &fakeAPI{
		listSitesResp: []apiclient.SiteSummary{{ID: "site-1", Slug: "docs"}},
		jsonDoc:       json.RawMessage(`{"items":[{"id":"t1","untrusted":true}],"next_cursor":"p2","comment_content":"untrusted_data"}`),
	}
	svc := &Service{API: api}
	out, err := svc.ListSiteComments(context.Background(), readTok, "docs", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if api.readToken != readTok || !strings.Contains(api.jsonPath, "/v1/sites/site-1/comment-pages") || out.CommentContent != "untrusted_data" {
		t.Fatalf("path=%s out=%s", api.jsonPath, out.Document)
	}
	if _, err := svc.ListSiteComments(context.Background(), readTok, "docs", "p2", 1); err != nil || !strings.Contains(api.jsonPath, "cursor=p2") {
		t.Fatalf("second path=%s err=%v", api.jsonPath, err)
	}
	if _, err := svc.readJSON(context.Background(), readTok, "/v1/project-rooms/room-1/candidates/cand-1"); err != nil || api.jsonPath != "/v1/project-rooms/room-1/candidates/cand-1" {
		t.Fatalf("candidate path=%s err=%v", api.jsonPath, err)
	}
}

func TestCommentThreadForwardsIndependentHistoryCursor(t *testing.T) {
	api := &fakeAPI{jsonDoc: json.RawMessage(`{"thread":{"next_cursor":"replies","next_revision_cursor":"history"}}`)}
	svc := &Service{API: api}
	ctx := mcpauth.WithToken(context.Background(), readTok)
	_, out, err := svc.getCommentThreadHandler(ctx, nil, threadReadIn{ThreadID: "thread", Cursor: "reply+/=", RevisionsCursor: "history+/=", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(api.jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("cursor") != "reply+/=" || parsed.Query().Get("revisions_cursor") != "history+/=" || api.readToken != readTok || out.CommentContent != "untrusted_data" {
		t.Fatalf("bad forwarded request %s", api.jsonPath)
	}
}
