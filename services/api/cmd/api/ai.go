// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package main

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/danielpang/dropway/internal/embeddings"
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
func wireAIBuilder(api *handlers.API, cfg config.Config, siteStore handlers.SiteStore, obj storage.Store, proj projection.Writer, log *slog.Logger) (*ai.Runner, error) {
	// The runner (and the memory store) need the concrete store (its own
	// tx-per-call surface); if the DB isn't wired, neither feature can run.
	st, _ := siteStore.(*store.Store)

	// Org memory is strictly opt-in twice over: the deployment must configure
	// an embeddings key AND each org must flip memory_enabled. The memory
	// ENDPOINTS (list/search/add — the MCP and CLI surface) need only the
	// embedder + DB, so they are wired before the OpenRouter gate below;
	// in-loop retrieval/extraction additionally need the AI builder.
	var embedder *embeddings.Client
	if cfg.EmbeddingsAPIKey != "" && st != nil {
		embedder = &embeddings.Client{
			BaseURL:    cfg.EmbeddingsBaseURL,
			APIKey:     cfg.EmbeddingsAPIKey,
			Model:      cfg.EmbeddingsModel,
			Dimensions: cfg.EmbeddingsDimensions,
		}
		api.Memory = st
		api.MemoryEmbedder = embedder
		api.MemoryMaxPerOrg = cfg.AIMemoryMaxPerOrg
		slog.Info("org memory enabled", "embeddings_model", cfg.EmbeddingsModel, "extract_model", cfg.AIMemoryModel)
	} else {
		slog.Info("org memory disabled (no EMBEDDINGS_API_KEY or no database)")
	}

	if cfg.OpenRouterAPIKey == "" {
		slog.Info("AI builder disabled (no OPENROUTER_API_KEY)")
		return nil, nil
	}
	if st == nil {
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
		Logger:        log,
	}

	// In-loop memory (retrieval + post-turn extraction) rides the embedder
	// wired above; nil leaves every memory path in the loop a no-op. The
	// runner also serves as the chat-log extractor and the publish-time
	// content indexer for the handlers.
	if embedder != nil {
		runner.Embedder = embedder
		runner.MemoryExtractModel = cfg.AIMemoryModel
		runner.MemoryTopK = cfg.AIMemoryTopK
		runner.MemoryMaxPerOrg = cfg.AIMemoryMaxPerOrg
		api.MemoryExtract = runner
		api.MemoryIndex = runner
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
