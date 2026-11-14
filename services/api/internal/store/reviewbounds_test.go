// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package store

import (
	"errors"
	"strings"
	"testing"
)

func TestBoundedReviewRowsPreservesEveryItem(t *testing.T) {
	// '<' expands sixfold in encoded JSON. Measure escaped bytes, not source size.
	type row struct {
		ID   int
		Body string
	}
	rows := make([]row, 100)
	for i := range rows {
		rows[i] = row{ID: i, Body: strings.Repeat("<", 4000)}
	}
	rest := rows
	seen := 0
	pages := 0
	for len(rest) > 0 {
		page, more, err := boundedReviewRows(rest, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 || len(page) >= 100 {
			t.Fatalf("byte budget failed: %d", len(page))
		}
		for _, r := range page {
			if r.ID != seen {
				t.Fatalf("missing/duplicate item %d want %d", r.ID, seen)
			}
			seen++
		}
		rest = rest[len(page):]
		pages++
		if more != (len(rest) > 0) {
			t.Fatal("incorrect continuation flag")
		}
	}
	if seen != 100 || pages < 2 {
		t.Fatalf("incomplete traversal %d rows in %d pages", seen, pages)
	}
	if _, _, err := boundedReviewRows([]row{{Body: strings.Repeat("x", reviewArrayBytes)}}, 100); !errors.Is(err, ErrReviewPageTooLarge) {
		t.Fatalf("oversized row silently accepted: %v", err)
	}
}
