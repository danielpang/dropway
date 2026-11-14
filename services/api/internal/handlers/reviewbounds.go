// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package handlers

import (
	"encoding/json"
	"github.com/danielpang/dropway/internal/httpx"
	"net/http"
)

const maxReviewResponseBytes = 1 << 20

func writeReviewTooLarge(w http.ResponseWriter) {
	httpx.WriteJSON(w, http.StatusRequestEntityTooLarge, httpx.ErrorBody{Error: "review_response_too_large", Message: "Review data exceeds the response size limit; request a smaller page."})
}

// Measure the actual escaped JSON before committing headers. Store paging
// normally stays well below this envelope cap and returns continuation cursors.
func writeReviewJSON(w http.ResponseWriter, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if len(raw)+1 > maxReviewResponseBytes {
		writeReviewTooLarge(w)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(raw, '\n'))
}
