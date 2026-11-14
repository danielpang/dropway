// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package projectroom

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"
)

const (
	AnchorPoint     = "point"
	AnchorText      = "text"
	AnchorComponent = "component"
	AnchorPage      = "page"

	AnchorAnchored = "anchored"
	AnchorOrphaned = "orphaned"
)

// ErrInvalidAnchor is a client error. Missing or ambiguous anchors are
// orphaned by an explicit call; they are never fuzzy-migrated.
var ErrInvalidAnchor = errors.New("projectroom: invalid anchor")

// Anchor is a validated, canonical anchor payload. A point is a visual
// coordinate, not a source mapping.
type Anchor struct {
	Kind string
	JSON []byte
}

type pointAnchor struct {
	X         float64     `json:"x"`
	Y         float64     `json:"y"`
	Viewport  box         `json:"viewport"`
	Rendition box         `json:"rendition"`
	Scroll    scrollPos   `json:"scroll"`
	Image     *imagePoint `json:"image,omitempty"`
}

type box struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type scrollPos struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type imagePoint struct {
	IntrinsicWidth  int `json:"intrinsic_width"`
	IntrinsicHeight int `json:"intrinsic_height"`
	X               int `json:"x"`
	Y               int `json:"y"`
}

type textAnchor struct {
	Quote         string `json:"quote"`
	ContextBefore string `json:"context_before,omitempty"`
	ContextAfter  string `json:"context_after,omitempty"`
	StartOffset   int    `json:"start_offset"`
	EndOffset     int    `json:"end_offset"`
	BlockID       string `json:"block_id,omitempty"`
}

type componentAnchor struct {
	StableID string `json:"stable_id"`
}

// ValidateAnchor checks kind and raw JSON. Unknown fields are rejected so a
// visual point cannot smuggle a source path.
func ValidateAnchor(kind string, raw json.RawMessage) (Anchor, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	switch kind {
	case AnchorPoint:
		var p pointAnchor
		if err := dec.Decode(&p); err != nil {
			return Anchor{}, fmt.Errorf("%w: %s", ErrInvalidAnchor, err.Error())
		}
		if err := rejectTrailing(dec); err != nil {
			return Anchor{}, err
		}
		if err := validatePoint(p); err != nil {
			return Anchor{}, err
		}
		buf, err := json.Marshal(p)
		if err != nil {
			return Anchor{}, err
		}
		return Anchor{Kind: kind, JSON: buf}, nil
	case AnchorText:
		var a textAnchor
		if err := dec.Decode(&a); err != nil {
			return Anchor{}, fmt.Errorf("%w: %s", ErrInvalidAnchor, err.Error())
		}
		if err := rejectTrailing(dec); err != nil {
			return Anchor{}, err
		}
		a.Quote = strings.TrimSpace(a.Quote)
		if err := validateText(a); err != nil {
			return Anchor{}, err
		}
		buf, err := json.Marshal(a)
		if err != nil {
			return Anchor{}, err
		}
		return Anchor{Kind: kind, JSON: buf}, nil
	case AnchorComponent:
		var a componentAnchor
		if err := dec.Decode(&a); err != nil {
			return Anchor{}, fmt.Errorf("%w: %s", ErrInvalidAnchor, err.Error())
		}
		if err := rejectTrailing(dec); err != nil {
			return Anchor{}, err
		}
		a.StableID = strings.TrimSpace(a.StableID)
		if a.StableID == "" || len(a.StableID) > 256 || hasControl(a.StableID) {
			return Anchor{}, fmt.Errorf("%w: stable_id", ErrInvalidAnchor)
		}
		buf, err := json.Marshal(a)
		if err != nil {
			return Anchor{}, err
		}
		return Anchor{Kind: kind, JSON: buf}, nil
	case AnchorPage:
		var obj map[string]json.RawMessage
		if err := dec.Decode(&obj); err != nil {
			return Anchor{}, fmt.Errorf("%w: %s", ErrInvalidAnchor, err.Error())
		}
		if err := rejectTrailing(dec); err != nil {
			return Anchor{}, err
		}
		if len(obj) != 0 {
			return Anchor{}, fmt.Errorf("%w: page anchor has no position", ErrInvalidAnchor)
		}
		return Anchor{Kind: kind, JSON: []byte(`{}`)}, nil
	default:
		return Anchor{}, fmt.Errorf("%w: kind", ErrInvalidAnchor)
	}
}

// ValidatePage checks the immutable page path. Unsupported HTML may still
// use a page comment; the path is not a source mapping.
func ValidatePage(page string) (string, error) {
	page = strings.TrimSpace(page)
	if page == "" || len(page) > 512 || hasControl(page) {
		return "", fmt.Errorf("%w: page", ErrInvalidAnchor)
	}
	if strings.Contains(page, "\\") {
		return "", fmt.Errorf("%w: page", ErrInvalidAnchor)
	}
	for _, seg := range strings.Split(page, "/") {
		if seg == ".." {
			return "", fmt.Errorf("%w: page", ErrInvalidAnchor)
		}
	}
	return page, nil
}

func validatePoint(p pointAnchor) error {
	if !unit(p.X) || !unit(p.Y) {
		return fmt.Errorf("%w: normalized coordinates", ErrInvalidAnchor)
	}
	if !boxOK(p.Viewport) || !boxOK(p.Rendition) {
		return fmt.Errorf("%w: viewport", ErrInvalidAnchor)
	}
	if p.Scroll.X < 0 || p.Scroll.Y < 0 {
		return fmt.Errorf("%w: scroll", ErrInvalidAnchor)
	}
	if p.Image != nil {
		if p.Image.IntrinsicWidth <= 0 || p.Image.IntrinsicHeight <= 0 ||
			p.Image.X < 0 || p.Image.Y < 0 ||
			p.Image.X > p.Image.IntrinsicWidth || p.Image.Y > p.Image.IntrinsicHeight {
			return fmt.Errorf("%w: image coordinates", ErrInvalidAnchor)
		}
	}
	return nil
}

func validateText(a textAnchor) error {
	a.Quote = strings.TrimSpace(a.Quote)
	if a.Quote == "" || len(a.Quote) > 2000 {
		return fmt.Errorf("%w: quote", ErrInvalidAnchor)
	}
	if len(a.ContextBefore) > 500 || len(a.ContextAfter) > 500 {
		return fmt.Errorf("%w: context", ErrInvalidAnchor)
	}
	if a.StartOffset < 0 || a.EndOffset < a.StartOffset || a.EndOffset-a.StartOffset > 100000 {
		return fmt.Errorf("%w: offsets", ErrInvalidAnchor)
	}
	if len(a.BlockID) > 256 || hasControl(a.BlockID) {
		return fmt.Errorf("%w: block_id", ErrInvalidAnchor)
	}
	return nil
}

func unit(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1
}

func boxOK(b box) bool {
	return b.Width > 0 && b.Height > 0 && b.Width <= 100000 && b.Height <= 100000
}

func rejectTrailing(dec *json.Decoder) error {
	if dec.More() {
		return fmt.Errorf("%w: trailing json", ErrInvalidAnchor)
	}
	return nil
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}
