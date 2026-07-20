// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// TestProtectedResourceMetadataScopes guards the RFC 9728 metadata the MCP server
// advertises. MCP clients derive the scope they send at Dynamic Client Registration
// from `scopes_supported`, so this list must include BOTH the custom "mcp" scope and
// the standard "offline_access" scope. Advertising only "mcp" is what caused the
// `invalid_scope: offline_access` connect failure: a client registered without
// offline_access, then had its refresh-token scope rejected at authorize.
func TestProtectedResourceMetadataScopes(t *testing.T) {
	const resource = "https://mcp.example.com"
	const authServer = "https://dash.example.com"

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil)
	protectedResourceMetadata(resource, authServer).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var meta struct {
		Resource               string   `json:"resource"`
		AuthorizationServers   []string `json:"authorization_servers"`
		ScopesSupported        []string `json:"scopes_supported"`
		BearerMethodsSupported []string `json:"bearer_methods_supported"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}

	if meta.Resource != resource {
		t.Errorf("resource = %q, want %q", meta.Resource, resource)
	}
	if !slices.Contains(meta.AuthorizationServers, authServer) {
		t.Errorf("authorization_servers = %v, want to contain %q", meta.AuthorizationServers, authServer)
	}
	for _, want := range []string{"mcp", "offline_access"} {
		if !slices.Contains(meta.ScopesSupported, want) {
			t.Errorf("scopes_supported = %v, want to contain %q", meta.ScopesSupported, want)
		}
	}
}
