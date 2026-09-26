// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package main

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/danielpang/dropway/internal/embeddings"
	"github.com/danielpang/dropway/internal/openrouter"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/api/internal/ai"
	"github.com/danielpang/dropway/services/api/internal/config"
	"github.com/danielpang/dropway/services/api/internal/handlers"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// wireOrgMemory assembles org memory (chat-log extraction + content indexing)
// and attaches it to the API. The memory endpoints need an embeddings key and
// a database; extraction additionally needs an OpenRouter key. Either missing
// piece leaves that path off. The plan gate is added separately by the cloud
// build (mountCloud); the OSS default allows any org with a BYO key.
func wireOrgMemory(api *handlers.API, cfg config.Config, siteStore handlers.SiteStore, obj storage.Store, log *slog.Logger) *ai.Runner {
	st, _ := siteStore.(*store.Store)
	if cfg.EmbeddingsAPIKey == "" || st == nil {
		slog.Info("org memory disabled (no EMBEDDINGS_API_KEY or no database)")
		return nil
	}

	embedder := &embeddings.Client{
		BaseURL:    cfg.EmbeddingsBaseURL,
		APIKey:     cfg.EmbeddingsAPIKey,
		Model:      cfg.EmbeddingsModel,
		Dimensions: cfg.EmbeddingsDimensions,
	}
	api.Memory = st
	api.MemoryEmbedder = embedder
	api.MemoryMaxPerOrg = cfg.AIMemoryMaxPerOrg

	runner := &ai.Runner{
		Store:              st,
		Objects:            obj,
		Embedder:           embedder,
		MemoryExtractModel: cfg.AIMemoryModel,
		MemoryMaxPerOrg:    cfg.AIMemoryMaxPerOrg,
		Logger:             log,
	}
	if cfg.OpenRouterAPIKey != "" {
		runner.LLM = &openrouter.Client{
			APIKey:     cfg.OpenRouterAPIKey,
			AppURL:     "https://dropway.dev",
			AppTitle:   "Dropway",
			HTTPClient: &http.Client{Timeout: 2 * time.Minute},
		}
		api.MemoryExtract = runner
	} else {
		slog.Info("org memory extraction disabled (no OPENROUTER_API_KEY); content indexing still runs")
	}
	api.MemoryIndex = runner
	slog.Info("org memory enabled", "embeddings_model", cfg.EmbeddingsModel, "extract_model", cfg.AIMemoryModel)
	return runner
}
