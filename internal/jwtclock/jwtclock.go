// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package jwtclock holds the one clock-skew tolerance shared by every JWT
// verifier in the codebase. It is a stdlib-only LEAF package on purpose: the
// user-session JWT (internal/auth) and the edge token (internal/edgetoken) are
// deliberately SEPARATE trust domains with their own keys and verifiers, so they
// must not import each other just to agree on a constant. Both import this
// instead.
package jwtclock

import "time"

// Leeway is the wall-clock drift tolerated when validating exp/iat/nbf. The
// minting host and the verifying host run on different clocks (dashboard on
// Vercel, API on Fly, edge on Cloudflare); with zero leeway a few seconds of NTP
// drift intermittently rejects valid tokens as expired (or not-yet-valid). 60s
// is small next to the 10-15 minute token lifetimes and bounds acceptance to at
// most 60s past exp. The Cloudflare Worker mirrors this as `clockTolerance`
// seconds (edge/serving-worker/src/edgetoken.ts); the edgetoken parity test
// enforces that the two stay equal.
const Leeway = 60 * time.Second
