// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package edgetoken

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"testing"

	"github.com/danielpang/dropway/internal/auth"
)

// The edge-token verification contract is implemented twice — here in Go (this
// package + services/serve/edgeverify) and in TypeScript in the Cloudflare
// Worker — with NO compile-time link between them. These tests pin the two
// values that would silently break every gated-content view if they drifted:
// the fixed `iss` claim and the clock-skew tolerance. A drift becomes a test
// failure instead of a production 302 loop.

// workerSource reads a file from edge/serving-worker/src, located relative to
// this test file so it works from any `go test` working directory.
func workerSource(t *testing.T, name string) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// internal/edgetoken/ → repo root → edge/serving-worker/src/<name>
	path := filepath.Join(filepath.Dir(self), "..", "..", "edge", "serving-worker", "src", name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read worker source %s: %v", name, err)
	}
	return string(b)
}

func TestEdgeIssuerMatchesWorkerConst(t *testing.T) {
	src := workerSource(t, "config.ts")
	re := regexp.MustCompile(`EDGE_TOKEN_ISSUER\s*=\s*"([^"]+)"`)
	m := re.FindStringSubmatch(src)
	if m == nil {
		t.Fatal("EDGE_TOKEN_ISSUER not found in edge/serving-worker/src/config.ts")
	}
	if m[1] != Issuer {
		t.Fatalf("edge issuer drift: Go edgetoken.Issuer=%q, Worker EDGE_TOKEN_ISSUER=%q — these must be byte-identical or every edge token is rejected", Issuer, m[1])
	}
}

func TestClockToleranceMatchesWorker(t *testing.T) {
	src := workerSource(t, "edgetoken.ts")
	re := regexp.MustCompile(`clockTolerance:\s*(\d+)`)
	m := re.FindStringSubmatch(src)
	if m == nil {
		t.Fatal("clockTolerance not found in edge/serving-worker/src/edgetoken.ts")
	}
	workerSecs, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("parse worker clockTolerance: %v", err)
	}
	goSecs := int(auth.ClockSkewLeeway.Seconds())
	if workerSecs != goSecs {
		t.Fatalf("clock-skew drift: Go auth.ClockSkewLeeway=%ds, Worker clockTolerance=%ds — a token near expiry would be judged differently at the edge vs the API", goSecs, workerSecs)
	}
}
