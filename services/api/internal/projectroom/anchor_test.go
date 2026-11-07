// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package projectroom

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestValidateAnchorPoint(t *testing.T) {
	raw := json.RawMessage(`{
		"x": 0.5, "y": 0,
		"viewport": {"width": 1280, "height": 720},
		"rendition": {"width": 640, "height": 360},
		"scroll": {"x": 0, "y": 10}
	}`)
	a, err := ValidateAnchor(AnchorPoint, raw)
	if err != nil {
		t.Fatal(err)
	}
	if a.Kind != AnchorPoint || !json.Valid(a.JSON) {
		t.Fatalf("anchor = %+v", a)
	}
}

func TestValidateAnchorRejectsSourceMapping(t *testing.T) {
	raw := json.RawMessage(`{
		"x": 0.1, "y": 0.2,
		"viewport": {"width": 10, "height": 10},
		"rendition": {"width": 10, "height": 10},
		"scroll": {"x": 0, "y": 0},
		"source_file": "app.tsx",
		"line": 4
	}`)
	_, err := ValidateAnchor(AnchorPoint, raw)
	if !errors.Is(err, ErrInvalidAnchor) {
		t.Fatalf("err = %v, want invalid anchor", err)
	}
}

func TestValidateAnchorTextAndPage(t *testing.T) {
	text := json.RawMessage(`{"quote":"hello","context_before":"a","context_after":"b","start_offset":1,"end_offset":6,"block_id":"p1"}`)
	if _, err := ValidateAnchor(AnchorText, text); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateAnchor(AnchorPage, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateAnchor(AnchorPage, json.RawMessage(`{"x":1}`)); err == nil {
		t.Fatal("page anchor must not carry a position")
	}
	if _, err := ValidateAnchor(AnchorComponent, json.RawMessage(`{"stable_id":"hero"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAnchorImageBounds(t *testing.T) {
	raw := json.RawMessage(`{
		"x": 0.2, "y": 0.2,
		"viewport": {"width": 10, "height": 10},
		"rendition": {"width": 10, "height": 10},
		"scroll": {"x": 0, "y": 0},
		"image": {"intrinsic_width": 5, "intrinsic_height": 5, "x": 9, "y": 1}
	}`)
	if _, err := ValidateAnchor(AnchorPoint, raw); err == nil {
		t.Fatal("image point outside intrinsic bounds must fail")
	}
}

func TestValidatePage(t *testing.T) {
	if _, err := ValidatePage("/docs/index.html"); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidatePage("../etc/passwd"); err == nil {
		t.Fatal("parent segment must fail")
	}
	if _, err := ValidatePage("  "); err == nil {
		t.Fatal("empty page must fail")
	}
	if _, err := ValidatePage(strings.Repeat("a", 513)); err == nil {
		t.Fatal("long page must fail")
	}
}
