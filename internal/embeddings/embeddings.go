// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package embeddings is a minimal client for an OpenAI-compatible
// POST /v1/embeddings endpoint. It is the org-memory feature's second vendor
// seam (OpenRouter has no embeddings endpoint): the hosted build points it at
// OpenAI; a self-host can point EMBEDDINGS_BASE_URL at any compatible server
// (Ollama, a proxy, …). Like internal/openrouter, it is deliberately tiny and
// dependency-free so a different provider drops in behind the same Client.
package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultModel matches the vector(1536) column width in migration 0017. A
// different model requires the migration procedure in
// docs/org-memory-scope.md §3.5.
const (
	DefaultBaseURL    = "https://api.openai.com/v1"
	DefaultModel      = "text-embedding-3-small"
	DefaultDimensions = 1536
)

// maxBatch bounds inputs per request (OpenAI allows 2048; stay well under).
const maxBatch = 256

// maxInputBytes truncates any single input (memory contents are ~2 KB;
// content chunks ~1 KB — this is a defensive bound, not a working limit).
const maxInputBytes = 16 << 10

// Client calls the embeddings endpoint. Zero value is not usable; construct
// with the fields set (the composition root reads them from config).
type Client struct {
	BaseURL string // e.g. https://api.openai.com/v1 (no trailing slash needed)
	APIKey  string
	Model   string
	// Dimensions, when > 0 and different from the model default, is passed
	// through (OpenAI's -3 models support Matryoshka truncation).
	Dimensions int
	// HTTPClient defaults to a 30s-timeout client.
	HTTPClient *http.Client
}

// Embed returns one vector per input, in order. Inputs beyond the per-request
// batch bound are sent in successive requests; a transient 429/5xx is retried
// once with a short backoff before failing.
func (c *Client) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	out := make([][]float32, 0, len(inputs))
	for start := 0; start < len(inputs); start += maxBatch {
		end := min(start+maxBatch, len(inputs))
		vecs, err := c.embedBatch(ctx, inputs[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

func (c *Client) embedBatch(ctx context.Context, inputs []string) ([][]float32, error) {
	clean := make([]string, len(inputs))
	for i, s := range inputs {
		if len(s) > maxInputBytes {
			s = s[:maxInputBytes]
		}
		// The API rejects empty strings; a lone space keeps positions aligned.
		if strings.TrimSpace(s) == "" {
			s = " "
		}
		clean[i] = s
	}

	body := map[string]any{"model": c.model(), "input": clean}
	if c.Dimensions > 0 && c.Dimensions != DefaultDimensions {
		body["dimensions"] = c.Dimensions
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Second):
			}
		}
		vecs, retryable, err := c.post(ctx, payload, len(clean))
		if err == nil {
			return vecs, nil
		}
		lastErr = err
		if !retryable {
			break
		}
	}
	return nil, lastErr
}

func (c *Client) post(ctx context.Context, payload []byte, wantN int) (vecs [][]float32, retryable bool, err error) {
	url := strings.TrimRight(c.baseURL(), "/") + "/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, true, fmt.Errorf("embeddings: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return nil, retry, fmt.Errorf("embeddings: %s: %s", resp.Status, strings.TrimSpace(string(snippet)))
	}

	var parsed struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 256<<20)).Decode(&parsed); err != nil {
		return nil, false, fmt.Errorf("embeddings: decode: %w", err)
	}
	if len(parsed.Data) != wantN {
		return nil, false, fmt.Errorf("embeddings: got %d vectors for %d inputs", len(parsed.Data), wantN)
	}
	// The API documents order but indexes defensively; place by index.
	out := make([][]float32, wantN)
	for _, d := range parsed.Data {
		if d.Index < 0 || d.Index >= wantN {
			return nil, false, fmt.Errorf("embeddings: vector index %d out of range", d.Index)
		}
		out[d.Index] = d.Embedding
	}
	return out, false, nil
}

// ModelID reports the configured model (stored per row as embedding_model).
func (c *Client) ModelID() string { return c.model() }

func (c *Client) model() string {
	if c.Model != "" {
		return c.Model
	}
	return DefaultModel
}

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return DefaultBaseURL
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}
