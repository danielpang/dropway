// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package main

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/danielpang/dropway/internal/openrouter"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/sandbox"
	"github.com/danielpang/dropway/internal/sandbox/docker"
	"github.com/danielpang/dropway/internal/sandbox/flymachines"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/api/internal/ai"
	"github.com/danielpang/dropway/services/api/internal/config"
	"github.com/danielpang/dropway/services/api/internal/handlers"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// wireAIBuilder assembles the AI website builder and attaches it to the API. It
// is a no-op (leaving the /v1/ai routes to 503) when no OpenRouter key is set,
// so the feature is strictly opt-in. The sandbox provider is chosen by
// SANDBOX_PROVIDER (docker for self-host, fly for the hosted build).
//
// The plan/card gate is added separately by the cloud build (mountCloud); the
// OSS default allows any org with a BYO key.
func wireAIBuilder(api *handlers.API, cfg config.Config, siteStore handlers.SiteStore, obj storage.Store, proj projection.Writer, _ *slog.Logger) (*ai.Runner, error) {
	if cfg.OpenRouterAPIKey == "" {
		slog.Info("AI builder disabled (no OPENROUTER_API_KEY)")
		return nil, nil
	}
	// The runner needs the concrete store (its own tx-per-call surface); if the
	// DB isn't wired, the AI feature can't run.
	st, ok := siteStore.(*store.Store)
	if !ok || st == nil {
		slog.Warn("AI builder disabled (no database)")
		return nil, nil
	}

	llm := &openrouter.Client{
		APIKey:     cfg.OpenRouterAPIKey,
		AppURL:     "https://dropway.dev",
		AppTitle:   "Dropway AI Builder",
		HTTPClient: &http.Client{Timeout: 10 * time.Minute},
	}

	var provider sandbox.Provider
	switch cfg.SandboxProvider {
	case "fly":
		provider = &flymachines.Provider{
			AppName:  cfg.FlySandboxApp,
			APIToken: cfg.FlyAPIToken,
			Image:    cfg.SandboxImage,
		}
	default: // "docker"
		provider = &docker.Provider{Image: cfg.SandboxImage}
	}

	runner := &ai.Runner{
		Store:         st,
		Objects:       obj,
		LLM:           llm,
		Sandboxes:     provider,
		Projection:    proj,
		SandboxTTL:    2 * time.Hour,
		MaxIterations: 50,
	}
	if cfg.AIMonthlyCapUSD > 0 {
		// Self-host cap override: enforce a flat monthly cap via the period-start
		// default (calendar month). The org_meta cap still applies per org; this
		// is the OSS env fallback surfaced through GetAISettings defaults.
		slog.Info("AI monthly spend cap (self-host default)", "usd", cfg.AIMonthlyCapUSD)
	}

	api.AI = runner
	api.AIModels = llm
	api.AIDefaultModel = cfg.AIDefaultModel
	api.AIMaxConcurrent = 2
	slog.Info("AI builder enabled", "sandbox_provider", cfg.SandboxProvider, "default_model", cfg.AIDefaultModel)
	return runner, nil
}
