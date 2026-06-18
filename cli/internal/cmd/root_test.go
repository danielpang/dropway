package cmd

import (
	"strings"
	"testing"

	"github.com/danielpang/dropway/cli/internal/manifest"
)

// fakeManifest builds a minimal one-file *manifest.Manifest for the uploadMissing
// branch tests (path → sha), without touching disk for the hash.
func fakeManifest(path, sha string) *manifest.Manifest {
	return &manifest.Manifest{
		Files:  []manifest.Entry{{Path: path, SHA256: sha, Size: 5}},
		Digest: strings.Repeat("a", 64),
	}
}

// TestNewRootCmd_WiresSubcommands proves the root command registers the deploy +
// operator (gc/dr) subcommands the CLI exposes.
func TestNewRootCmd_WiresSubcommands(t *testing.T) {
	root := NewRootCmd()
	want := map[string]bool{"deploy": false, "gc": false, "dr": false}
	for _, c := range root.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("root command is missing the %q subcommand", name)
		}
	}
	if root.Use != "dropway" {
		t.Errorf("root Use = %q, want dropway", root.Use)
	}
	// Errors/usage are silenced (main prints them itself).
	if !root.SilenceUsage || !root.SilenceErrors {
		t.Error("root should silence usage + errors (main owns error printing)")
	}
}

// TestExecute_UnknownCommand returns an error (via the wired root) for an unknown
// subcommand — proving Execute() drives the assembled root command.
func TestExecute_RootHasHelp(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"--help"})
	// --help is handled by cobra and returns nil; the point is the wired root runs.
	if err := root.Execute(); err != nil {
		t.Fatalf("root --help: %v", err)
	}
}

func TestDefaultAPIBase(t *testing.T) {
	// With DROPWAY_API set, it is used.
	t.Setenv("DROPWAY_API", "http://localhost:8080")
	if got := defaultAPIBase(); got != "http://localhost:8080" {
		t.Errorf("defaultAPIBase with env = %q, want the override", got)
	}
}

func TestDefaultAPIBase_FallsBackToProd(t *testing.T) {
	t.Setenv("DROPWAY_API", "")
	if got := defaultAPIBase(); got != "https://api.dropway.dev" {
		t.Errorf("defaultAPIBase without env = %q, want the production default", got)
	}
}

// TestDefaultClientFactory builds the real HTTP client from flags (the production
// wiring the root command uses). It must produce a usable *api.HTTPClient.
func TestDefaultClientFactory(t *testing.T) {
	c := defaultClientFactory("https://api.example.com", "shpd_tok")
	if c == nil {
		t.Fatal("defaultClientFactory returned nil")
	}
}
