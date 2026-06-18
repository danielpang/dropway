// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// TestVersionCmd prints the build summary to stdout (the install script runs
// `dropway version` to confirm a good install).
func TestVersionCmd(t *testing.T) {
	cmd := newVersionCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("version: %v", err)
	}
	got := out.String()
	if !strings.HasPrefix(got, "dropway ") {
		t.Errorf("version output = %q, want it to start with %q", got, "dropway ")
	}
	// The default dev build embeds these markers; a release overrides them.
	for _, want := range []string{version, commit} {
		if !strings.Contains(got, want) {
			t.Errorf("version output %q missing %q", got, want)
		}
	}
}
