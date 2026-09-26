// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ai

import (
	"context"
	"errors"
	"log/slog"

	"github.com/danielpang/dropway/internal/openrouter"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// Runner is the org-memory worker. It extracts durable facts from shared chat
// logs and indexes published site and skill content for later retrieval. It
// does not edit sites.
type Runner struct {
	Store   *store.Store
	Objects storage.Store
	// LLM is the OpenRouter client used for chat-log extraction. Nil skips
	// extraction; content indexing still runs from Embedder alone.
	LLM                *openrouter.Client
	Embedder           Embedder
	MemoryExtractModel string
	MemoryMaxPerOrg    int
	// MemoryGate plan-gates memory (cloud: Pro+; nil → allow all).
	MemoryGate MemoryGate
	Logger     *slog.Logger
}

func (r *Runner) logger() *slog.Logger {
	if r == nil || r.Logger == nil {
		return slog.Default()
	}
	return r.Logger
}

// complete runs one non-streaming generation and returns the assistant message.
func (r *Runner) complete(ctx context.Context, model string, messages []openrouter.Message) (openrouter.Message, error) {
	if r.LLM == nil {
		return openrouter.Message{}, errors.New("memory: no language model configured")
	}
	ch, err := r.LLM.ChatStream(ctx, openrouter.ChatRequest{Model: model, Messages: messages})
	if err != nil {
		return openrouter.Message{}, err
	}
	var done openrouter.Event
	for ev := range ch {
		switch ev.Type {
		case openrouter.EventDone:
			done = ev
		case openrouter.EventError:
			if ev.Err != nil {
				return openrouter.Message{}, ev.Err
			}
			return openrouter.Message{}, errors.New("memory: generation failed")
		}
	}
	if done.Type != openrouter.EventDone {
		return openrouter.Message{}, errors.New("memory: generation ended without a result")
	}
	return done.Message, nil
}
