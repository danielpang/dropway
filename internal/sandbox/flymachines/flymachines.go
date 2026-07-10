// Package flymachines implements the sandbox.Provider seam over the Fly
// Machines REST API (the hosted build). Each builder session gets its own
// ephemeral Firecracker microVM in a dedicated sandbox Fly app, reached over Fly
// private networking (6PN / .internal DNS) — the machine has no public address.
//
// Hard VM isolation is why this is the hosted-build provider: an untrusted,
// AI-generated build runs in its own kernel, not a shared one. The machine holds
// only the user's site files + a per-machine agent token; it never sees a
// Dropway, R2, or OpenRouter credential.
package flymachines

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/danielpang/dropway/internal/sandbox"
)

// DefaultAPIBase is the Fly Machines API root.
const DefaultAPIBase = "https://api.machines.dev/v1"

// Provider boots sandbox machines in a dedicated Fly app.
type Provider struct {
	// AppName is the dedicated sandbox Fly app machines are created in. Required.
	AppName string
	// APIToken is the Fly API token (org-scoped to the sandbox app). Required.
	APIToken string
	// Image is the builder image (registry.fly.io/...:tag). Required.
	Image string
	// Region is the Fly region to boot in (empty → Fly picks).
	Region string
	// AgentPort is the port sandboxd listens on inside the machine.
	AgentPort int
	// APIBase overrides the Machines API root (tests).
	APIBase string
	// HTTP is the client for BOTH the Machines API and the agent (defaults 15m).
	HTTP *http.Client
	// TokenFn mints a per-machine agent token (defaults to a random token).
	TokenFn func() string
	// Now is injected for tests (defaults to time.Now).
	Now func() time.Time
}

const defaultAgentPort = 8090

func (p *Provider) apiBase() string {
	if p.APIBase != "" {
		return p.APIBase
	}
	return DefaultAPIBase
}

func (p *Provider) agentPort() int {
	if p.AgentPort > 0 {
		return p.AgentPort
	}
	return defaultAgentPort
}

func (p *Provider) httpClient() *http.Client {
	if p.HTTP != nil {
		return p.HTTP
	}
	return &http.Client{Timeout: 15 * time.Minute}
}

func (p *Provider) mintToken() string {
	if p.TokenFn != nil {
		return p.TokenFn()
	}
	return sandbox.RandomToken()
}

// --- Machines API wire shapes (trimmed to what we use) ---

type machineConfig struct {
	Image       string            `json:"image"`
	Env         map[string]string `json:"env,omitempty"`
	Guest       machineGuest      `json:"guest"`
	AutoDestroy bool              `json:"auto_destroy"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Restart     *machineRestart   `json:"restart,omitempty"`
}

type machineGuest struct {
	CPUKind  string `json:"cpu_kind"`
	CPUs     int    `json:"cpus"`
	MemoryMB int    `json:"memory_mb"`
}

type machineRestart struct {
	Policy string `json:"policy"` // "no" — a sandbox is single-shot
}

type createMachineReq struct {
	Region string        `json:"region,omitempty"`
	Config machineConfig `json:"config"`
}

type machine struct {
	ID        string        `json:"id"`
	PrivateIP string        `json:"private_ip"`
	State     string        `json:"state"`
	CreatedAt string        `json:"created_at"`
	Config    machineConfig `json:"config"`
}

// Create boots a sandbox machine and waits for its agent to answer.
func (p *Provider) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	if p.AppName == "" || p.APIToken == "" {
		return nil, fmt.Errorf("flymachines: AppName and APIToken are required")
	}
	image := spec.Image
	if image == "" {
		image = p.Image
	}
	if image == "" {
		return nil, fmt.Errorf("flymachines: no image configured")
	}
	token := p.mintToken()

	cfg := machineConfig{
		Image: image,
		Env: map[string]string{
			"SANDBOXD_TOKEN": token,
			"SANDBOXD_PORT":  strconv.Itoa(p.agentPort()),
		},
		Guest:       machineGuest{CPUKind: "shared", CPUs: 2, MemoryMB: 1024},
		AutoDestroy: true, // reaped when it stops; the agent exits on idle TTL
		Restart:     &machineRestart{Policy: "no"},
		Metadata: map[string]string{
			"dropway_sandbox": "1",
			"dropway_session": spec.SessionID,
			"dropway_org":     spec.OrgID,
		},
	}
	var created machine
	if err := p.apiJSON(ctx, http.MethodPost, "/apps/"+p.AppName+"/machines",
		createMachineReq{Region: p.Region, Config: cfg}, &created); err != nil {
		return nil, err
	}
	if created.PrivateIP == "" {
		_ = p.Destroy(ctx, created.ID)
		return nil, fmt.Errorf("flymachines: machine %s has no private ip", created.ID)
	}

	// Reach the agent over 6PN private networking by the machine's private IPv6.
	baseURL := fmt.Sprintf("http://[%s]:%d", created.PrivateIP, p.agentPort())
	ac := sandbox.NewAgentClient(created.ID, baseURL, token, p.httpClient())
	readyCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := sandbox.WaitReady(readyCtx, ac); err != nil {
		_ = p.Destroy(ctx, created.ID)
		return nil, err
	}
	return ac, nil
}

// Get reconnects to an existing machine. The agent token is stored in the
// machine's env; we read it back so a cached handle can be re-materialized.
func (p *Provider) Get(ctx context.Context, id string) (sandbox.Sandbox, error) {
	var m machine
	if err := p.apiJSON(ctx, http.MethodGet, "/apps/"+p.AppName+"/machines/"+id, nil, &m); err != nil {
		return nil, err
	}
	if m.PrivateIP == "" {
		return nil, fmt.Errorf("flymachines: machine %s has no private ip", id)
	}
	token := m.Config.Env["SANDBOXD_TOKEN"]
	baseURL := fmt.Sprintf("http://[%s]:%d", m.PrivateIP, p.agentPort())
	return sandbox.NewAgentClient(id, baseURL, token, p.httpClient()), nil
}

// Destroy force-deletes the machine (idempotent — a 404 is success).
func (p *Provider) Destroy(ctx context.Context, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		p.apiBase()+"/apps/"+p.AppName+"/machines/"+id+"?force=true", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.APIToken)
	resp, err := p.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusOK {
		return nil
	}
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	return fmt.Errorf("flymachines: destroy %s: %s: %s", id, resp.Status, string(msg))
}

// ListSessionSandboxes lists live sandbox machines with their session tags.
func (p *Provider) ListSessionSandboxes(ctx context.Context) ([]sandbox.SandboxRef, error) {
	var machines []machine
	if err := p.apiJSON(ctx, http.MethodGet, "/apps/"+p.AppName+"/machines", nil, &machines); err != nil {
		return nil, err
	}
	var refs []sandbox.SandboxRef
	for _, m := range machines {
		if m.Config.Metadata["dropway_sandbox"] != "1" {
			continue
		}
		ref := sandbox.SandboxRef{ID: m.ID, SessionID: m.Config.Metadata["dropway_session"]}
		if t, err := time.Parse(time.RFC3339, m.CreatedAt); err == nil {
			ref.CreatedAt = t
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func (p *Provider) apiJSON(ctx context.Context, method, path string, reqBody, respBody any) error {
	var buf bytes.Buffer
	if reqBody != nil {
		if err := json.NewEncoder(&buf).Encode(reqBody); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, p.apiBase()+path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.APIToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return fmt.Errorf("flymachines: %s %s: %s: %s", method, path, resp.Status, string(msg))
	}
	if respBody != nil {
		return json.NewDecoder(resp.Body).Decode(respBody)
	}
	return nil
}

var _ sandbox.Provider = (*Provider)(nil)
