// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package sprites is the remote-execution seam. Sprites run file, shell, and
// build tools only. They do not run Pi, do not receive provider keys, and do
// not receive the Fly management token. The only implementation in this slice
// is Disabled. Official isolation, egress, destroy, and budget behavior must
// be re-read before any adapter is enabled. This package does not claim those
// controls are present.
package sprites

import (
	"context"
	"errors"
)

const ProviderFly = "fly_sprites"

var ErrExecutionDisabled = errors.New("sprites: execution disabled")

// AttemptSpec identifies one fresh disposable execution. It intentionally has
// no provider key, gateway token, or Fly token field.
type AttemptSpec struct {
	OrgID           string
	RoomID          string
	SiteID          string
	VersionID       string
	AttemptID       string
	SessionID       string
	InitiatorUserID string
}

// Handle is a provider assignment. Disabled returns an empty name.
type Handle struct {
	Provider   string
	OrgID      string
	AttemptID  string
	SpriteName string
}

// Runner starts and destroys a Sprite. Implementations must use a new Sprite
// per attempt, must not reuse one across tenants, and must destroy it after
// completion or clarification rather than leaving it warm.
type Runner interface {
	Provider() string
	Start(ctx context.Context, spec AttemptSpec) (Handle, error)
	Destroy(ctx context.Context, handle Handle, reason string) error
}

// Disabled refuses to start a Sprite and treats destroy as a no-op so a
// clarification plan can release an attempt that was never created.
type Disabled struct{}

func (Disabled) Provider() string { return ProviderFly }

func (Disabled) Start(context.Context, AttemptSpec) (Handle, error) {
	return Handle{}, ErrExecutionDisabled
}

func (Disabled) Destroy(context.Context, Handle, string) error { return nil }
