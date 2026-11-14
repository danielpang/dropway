// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReviewResponseBoundsBeforeHeaders(t *testing.T) {
	rr := httptest.NewRecorder()
	writeReviewJSON(rr, map[string]string{"body": strings.Repeat("<", maxReviewResponseBytes/6)})
	if rr.Code != http.StatusRequestEntityTooLarge || strings.Contains(rr.Body.String(), "\\u003c") {
		t.Fatalf("oversized body leaked/status=%d", rr.Code)
	}
	rr = httptest.NewRecorder()
	writeReviewJSON(rr, map[string]string{"body": "<safe>"})
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "\\u003c") {
		t.Fatalf("safe JSON missing: %s", rr.Body.String())
	}
}
