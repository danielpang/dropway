// Package docker implements the sandbox.Provider seam over the local Docker
// daemon (self-host / dev via docker-compose). It shells out to the `docker`
// CLI rather than pulling in the full Engine SDK: self-host already has Docker
// installed, and the CLI surface we need (run / rm / ps) is tiny and stable.
//
// SECURITY: this provider is root-equivalent on the host (it talks to the Docker
// socket). It is intended ONLY for self-host, where the operator already trusts
// the machine. The hosted build uses the flymachines provider (hard VM
// isolation). This is documented loudly in deploy/README.
package docker

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/danielpang/dropway/internal/sandbox"
)

// Provider boots builder containers on the local Docker daemon.
type Provider struct {
	// Image is the builder image (SANDBOX_IMAGE). Required.
	Image string
	// AgentPort is the port sandboxd listens on inside the container.
	AgentPort int
	// TokenFn mints a per-sandbox bearer token (defaults to a random token).
	TokenFn func() string
	// HTTP is the client used to reach the agent (defaults to a 15m client).
	HTTP *http.Client
	// DockerBin overrides the docker binary name (tests).
	DockerBin string
	// Now is injected for tests (defaults to time.Now).
	Now func() time.Time
}

const (
	defaultAgentPort = 8090
	// labelSession tags containers so the reaper can find leaked sandboxes.
	labelSession = "dropway.session"
	labelOrg     = "dropway.org"
	labelMarker  = "dropway.sandbox"
)

func (p *Provider) dockerBin() string {
	if p.DockerBin != "" {
		return p.DockerBin
	}
	return "docker"
}

func (p *Provider) agentPort() int {
	if p.AgentPort > 0 {
		return p.AgentPort
	}
	return defaultAgentPort
}

func (p *Provider) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// Create runs a detached builder container, publishing the agent port to a
// random host port, then waits for the agent to become reachable.
func (p *Provider) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	if p.Image == "" && spec.Image == "" {
		return nil, fmt.Errorf("docker sandbox: no image configured")
	}
	image := spec.Image
	if image == "" {
		image = p.Image
	}
	token := p.mintToken()

	args := []string{
		"run", "-d",
		"--label", labelMarker + "=1",
		"--label", labelSession + "=" + spec.SessionID,
		"--label", labelOrg + "=" + spec.OrgID,
		"--publish", "127.0.0.1::" + strconv.Itoa(p.agentPort()),
		"--env", "SANDBOXD_TOKEN=" + token,
		"--env", "SANDBOXD_PORT=" + strconv.Itoa(p.agentPort()),
	}
	if spec.Egress == sandbox.EgressNone {
		args = append(args, "--network", "none")
	}
	// A hard memory + pid cap keeps a runaway build from taking down the host.
	args = append(args, "--memory", "1g", "--pids-limit", "512")
	args = append(args, image)

	out, err := p.run(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("docker run: %w", err)
	}
	id := strings.TrimSpace(out)
	if id == "" {
		return nil, fmt.Errorf("docker run: empty container id")
	}

	baseURL, err := p.agentBaseURL(ctx, id)
	if err != nil {
		_ = p.Destroy(ctx, id)
		return nil, err
	}
	ac := sandbox.NewAgentClient(id, baseURL, token, p.HTTP)
	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := sandbox.WaitReady(readyCtx, ac); err != nil {
		_ = p.Destroy(ctx, id)
		return nil, err
	}
	return ac, nil
}

// Get reconnects to an existing container by id.
func (p *Provider) Get(ctx context.Context, id string) (sandbox.Sandbox, error) {
	baseURL, err := p.agentBaseURL(ctx, id)
	if err != nil {
		return nil, err
	}
	// The token is per-container and not recoverable from Docker; Get is only
	// used when the API cached the handle, so we read it back from the container
	// env. (Kept simple: the env var is readable via docker inspect.)
	token, err := p.containerToken(ctx, id)
	if err != nil {
		return nil, err
	}
	return sandbox.NewAgentClient(id, baseURL, token, p.HTTP), nil
}

// Destroy force-removes the container (idempotent).
func (p *Provider) Destroy(ctx context.Context, id string) error {
	_, err := p.run(ctx, "rm", "-f", id)
	if err != nil && strings.Contains(err.Error(), "No such container") {
		return nil
	}
	return err
}

// ListSessionSandboxes lists live dropway sandbox containers with their session
// tags, for the leaked-sandbox reaper.
func (p *Provider) ListSessionSandboxes(ctx context.Context) ([]sandbox.SandboxRef, error) {
	out, err := p.run(ctx, "ps",
		"--filter", "label="+labelMarker+"=1",
		"--format", "{{.ID}}\t{{.Label \""+labelSession+"\"}}\t{{.CreatedAt}}")
	if err != nil {
		return nil, err
	}
	var refs []sandbox.SandboxRef
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		ref := sandbox.SandboxRef{ID: parts[0]}
		if len(parts) > 1 {
			ref.SessionID = parts[1]
		}
		if len(parts) > 2 {
			// Docker's CreatedAt format; best-effort parse, zero on failure.
			if t, perr := time.Parse("2006-01-02 15:04:05 -0700 MST", parts[2]); perr == nil {
				ref.CreatedAt = t
			}
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// agentBaseURL resolves the host-published address of the container's agent port.
func (p *Provider) agentBaseURL(ctx context.Context, id string) (string, error) {
	out, err := p.run(ctx, "port", id, strconv.Itoa(p.agentPort()))
	if err != nil {
		return "", fmt.Errorf("docker port: %w", err)
	}
	// e.g. "127.0.0.1:49153" (take the first line).
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(out), "\n", 2)[0])
	if line == "" {
		return "", fmt.Errorf("docker port: no mapping for %d", p.agentPort())
	}
	return "http://" + line, nil
}

func (p *Provider) containerToken(ctx context.Context, id string) (string, error) {
	out, err := p.run(ctx, "inspect", "--format",
		"{{range .Config.Env}}{{println .}}{{end}}", id)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "SANDBOXD_TOKEN=") {
			return strings.TrimPrefix(line, "SANDBOXD_TOKEN="), nil
		}
	}
	return "", fmt.Errorf("docker inspect: SANDBOXD_TOKEN not found for %s", id)
}

func (p *Provider) mintToken() string {
	if p.TokenFn != nil {
		return p.TokenFn()
	}
	return sandbox.RandomToken()
}

func (p *Provider) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, p.dockerBin(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

var _ sandbox.Provider = (*Provider)(nil)
