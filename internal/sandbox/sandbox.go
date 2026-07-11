// Package sandbox is the vendor-neutral seam for running untrusted, AI-generated
// build commands in an isolated container. The AI website builder seeds a
// sandbox with a site's files, lets the model run shell commands and edit files
// inside it, then exports the result — the sandbox holds ONLY a copy of the
// user's own site files and a per-machine auth token, never any Dropway, R2, or
// OpenRouter credential.
//
// Two providers implement the seam (selected at runtime by SANDBOX_PROVIDER):
//   - flymachines: ephemeral Fly Machines (the hosted build).
//   - docker:      local Docker containers (self-host / dev via docker-compose).
//
// Both run the SAME builder image, whose entrypoint is services/sandboxd (a tiny
// HTTP agent), so Exec / file semantics never diverge between hosted and
// self-host. The seam lives in internal/ (open-source): self-host gets the same
// sandbox, just backed by Docker instead of Fly.
package sandbox

import (
	"context"
	"io"
	"time"
)

// Spec configures a new sandbox.
type Spec struct {
	OrgID     string
	SessionID string
	// Image is the builder image to boot. Empty → the provider's default.
	Image string
	// TTL is the hard machine lifetime; the provider auto-destroys the sandbox
	// past it regardless of activity (a runaway-agent backstop). Zero → the
	// provider default.
	TTL time.Duration
	// Egress selects the network policy: EgressFull (npm install works) or
	// EgressNone (fully offline). Empty → the provider default (full).
	Egress EgressPolicy
}

// EgressPolicy is the sandbox network posture.
type EgressPolicy string

const (
	// EgressFull permits outbound internet (npm/pip installs). The sandbox holds
	// no secrets, so the only exfiltration surface is the user's own site files.
	EgressFull EgressPolicy = "full"
	// EgressNone blocks all networking (Docker: --network none).
	EgressNone EgressPolicy = "none"
)

// ExecRequest runs a command in the sandbox working tree.
type ExecRequest struct {
	Cmd     []string
	Cwd     string
	Timeout time.Duration
	Stdin   []byte
}

// ExecResult is a finished command's outcome. Stdout/Stderr are truncated by the
// agent to a bound before returning (Truncated reports whether that happened) so
// a chatty build can't blow up the agent-loop context.
type ExecResult struct {
	ExitCode  int
	Stdout    []byte
	Stderr    []byte
	Truncated bool
}

// FileInfo is one entry from ListFiles.
type FileInfo struct {
	Path  string
	Size  int64
	IsDir bool
}

// Sandbox is a live, isolated build environment. All methods are remote calls to
// the in-container agent; a dead sandbox surfaces errors the caller treats as
// "recreate + reseed".
type Sandbox interface {
	// ID is the provider handle (Fly machine id / Docker container id).
	ID() string
	// Exec runs a command and returns its captured result.
	Exec(ctx context.Context, req ExecRequest) (ExecResult, error)
	// ImportTar unpacks a tar stream into dir (seeding site files).
	ImportTar(ctx context.Context, dir string, r io.Reader) error
	// ExportTar streams dir back as a tar (collecting the built output).
	ExportTar(ctx context.Context, dir string) (io.ReadCloser, error)
	// ReadFile / WriteFile / ListFiles are the model's fine-grained file tools.
	ReadFile(ctx context.Context, path string) ([]byte, error)
	WriteFile(ctx context.Context, path string, data []byte) error
	ListFiles(ctx context.Context, dir string) ([]FileInfo, error)
}

// Provider creates, looks up, and destroys sandboxes.
type Provider interface {
	// Create boots a new sandbox and waits for its agent to become reachable.
	Create(ctx context.Context, spec Spec) (Sandbox, error)
	// Get returns a handle to an existing sandbox by id (no boot). It does NOT
	// guarantee the sandbox is still alive; the first call surfaces a dead one.
	Get(ctx context.Context, id string) (Sandbox, error)
	// Destroy tears a sandbox down (idempotent: destroying an already-gone
	// sandbox is not an error).
	Destroy(ctx context.Context, id string) error
	// ListSessionSandboxes returns the ids of live sandboxes this provider owns,
	// tagged with a session id, for the leaked-sandbox reaper. A provider that
	// cannot enumerate returns an empty slice + nil.
	ListSessionSandboxes(ctx context.Context) ([]SandboxRef, error)
}

// SandboxRef pairs a live sandbox id with the session it was created for (from
// the provider's own tags/metadata), for the reaper.
type SandboxRef struct {
	ID        string
	SessionID string
	CreatedAt time.Time
}

// DefaultWorkdir is the in-sandbox path the builder seeds site files into and
// exports from. Providers and sandboxd agree on it.
const DefaultWorkdir = "/workspace"
