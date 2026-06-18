// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package customdomains

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// CloudflareProvider is the real Cloudflare-for-SaaS custom-hostnames Provider. It
// talks to the CF v4 REST API:
//
//	POST   /zones/{zone}/custom_hostnames           — create
//	GET    /zones/{zone}/custom_hostnames/{id}      — status
//
// Config comes from the environment (deploy/.env.example): CF_API_TOKEN,
// CF_ZONE_ID. Origin/edge cert settings use CF defaults (TXT DCV, HTTP-managed
// cert). Only the fields the state machine needs are parsed.
type CloudflareProvider struct {
	ZoneID   string
	APIToken string
	// Origin is the fallback origin the custom hostname routes to (the content
	// domain). CF requires custom_origin_server on some plans; we set it to the
	// PSL content domain so the serving Worker handles the request.
	Origin  string
	HTTP    *http.Client // nil → 10s client
	BaseURL string       // "" → https://api.cloudflare.com/client/v4
}

// NewCloudflareProvider builds a CloudflareProvider.
func NewCloudflareProvider(zoneID, apiToken, origin string) *CloudflareProvider {
	return &CloudflareProvider{
		ZoneID:   zoneID,
		APIToken: apiToken,
		Origin:   origin,
		HTTP:     &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *CloudflareProvider) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return "https://api.cloudflare.com/client/v4"
}

func (c *CloudflareProvider) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// --- CF wire shapes (only the fields we use) ---

type cfCreateReq struct {
	Hostname string `json:"hostname"`
	SSL      cfSSL  `json:"ssl"`
	// CustomOriginServer routes the validated hostname to the content domain.
	CustomOriginServer string `json:"custom_origin_server,omitempty"`
}

type cfSSL struct {
	Method string `json:"method"` // "txt"
	Type   string `json:"type"`   // "dv"
}

type cfEnvelope struct {
	Success bool         `json:"success"`
	Errors  []cfError    `json:"errors"`
	Result  cfCustomHost `json:"result"`
}

type cfError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cfCustomHost struct {
	ID     string `json:"id"`
	Status string `json:"status"` // pending/active/...
	SSL    struct {
		Status            string `json:"status"` // pending_validation/active/...
		ValidationRecords []struct {
			TxtName  string `json:"txt_name"`
			TxtValue string `json:"txt_value"`
		} `json:"validation_records"`
	} `json:"ssl"`
	OwnershipVerification struct {
		Name  string `json:"name"`
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"ownership_verification"`
}

// CreateCustomHostname POSTs a new custom hostname with TXT DCV.
func (c *CloudflareProvider) CreateCustomHostname(ctx context.Context, hostname string) (CreateResult, error) {
	body, err := json.Marshal(cfCreateReq{
		Hostname:           hostname,
		SSL:                cfSSL{Method: "txt", Type: "dv"},
		CustomOriginServer: c.Origin,
	})
	if err != nil {
		return CreateResult{}, err
	}
	url := fmt.Sprintf("%s/zones/%s/custom_hostnames", c.baseURL(), c.ZoneID)
	env, err := c.doJSON(ctx, http.MethodPost, url, body)
	if err != nil {
		return CreateResult{}, err
	}
	return CreateResult{
		ID:  env.Result.ID,
		DCV: dcvFrom(env.Result),
	}, nil
}

// Status GETs the custom hostname and normalizes its state.
func (c *CloudflareProvider) Status(ctx context.Context, id string) (StatusResult, error) {
	url := fmt.Sprintf("%s/zones/%s/custom_hostnames/%s", c.baseURL(), c.ZoneID, id)
	env, err := c.doJSON(ctx, http.MethodGet, url, nil)
	if err != nil {
		return StatusResult{}, err
	}
	return StatusResult{
		State:     normalizeState(env.Result),
		TLSIssued: env.Result.SSL.Status == "active",
		DCV:       dcvFrom(env.Result),
	}, nil
}

// DeleteCustomHostname DELETEs the custom hostname from the zone. A 404 (or CF's
// "custom hostname not found" code 1436) is treated as success so removal is
// idempotent — re-deleting an already-gone hostname is not an error.
func (c *CloudflareProvider) DeleteCustomHostname(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	url := fmt.Sprintf("%s/zones/%s/custom_hostnames/%s", c.baseURL(), c.ZoneID, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIToken)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("customdomains: cloudflare delete: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil // already gone → idempotent
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var env cfEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("customdomains: cloudflare returned %d: %s", resp.StatusCode, bytes.TrimSpace(raw))
	}
	if !env.Success {
		for _, e := range env.Errors {
			if e.Code == 1436 { // custom hostname not found → idempotent success
				return nil
			}
		}
		return fmt.Errorf("customdomains: cloudflare delete error (%d): %+v", resp.StatusCode, env.Errors)
	}
	return nil
}

// dcvFrom extracts the DCV record CF expects (ownership verification or the SSL
// validation TXT record).
func dcvFrom(h cfCustomHost) DCVRecord {
	if h.OwnershipVerification.Name != "" {
		return DCVRecord{Name: h.OwnershipVerification.Name, Type: h.OwnershipVerification.Type, Value: h.OwnershipVerification.Value}
	}
	if len(h.SSL.ValidationRecords) > 0 {
		r := h.SSL.ValidationRecords[0]
		return DCVRecord{Name: r.TxtName, Type: "TXT", Value: r.TxtValue}
	}
	return DCVRecord{}
}

// normalizeState maps the CF status + SSL status onto our VerifyState machine.
func normalizeState(h cfCustomHost) VerifyState {
	switch h.Status {
	case "active":
		if h.SSL.Status == "active" {
			return StateActive
		}
		return StateVerifying
	case "pending":
		return StatePending
	case "pending_validation", "pending_deployment":
		return StateVerifying
	case "":
		return StatePending
	default:
		// blocked / moved / deleted / etc.
		return StateFailed
	}
}

// doJSON performs a CF API request and decodes the envelope, mapping !success to
// an error including the CF error messages.
func (c *CloudflareProvider) doJSON(ctx context.Context, method, url string, body []byte) (cfEnvelope, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return cfEnvelope{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return cfEnvelope{}, fmt.Errorf("customdomains: cloudflare request: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var env cfEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return cfEnvelope{}, fmt.Errorf("customdomains: cloudflare returned %d: %s", resp.StatusCode, bytes.TrimSpace(raw))
	}
	if !env.Success {
		return cfEnvelope{}, fmt.Errorf("customdomains: cloudflare error (%d): %+v", resp.StatusCode, env.Errors)
	}
	return env, nil
}

// Ensure CloudflareProvider satisfies Provider.
var _ Provider = (*CloudflareProvider)(nil)
