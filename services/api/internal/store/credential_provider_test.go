package store

import (
	"context"
	"errors"
	"github.com/danielpang/dropway/services/api/internal/hosted"
	"testing"
)

func TestWrongKeyProviderRejectedBeforeStorage(t *testing.T) {
	s := &Store{}
	for _, tc := range []struct{ provider, key string }{{"openai", "sk-ant-example"}, {"anthropic", "sk-proj-example"}, {"openai", "sk-or-v1-example"}} {
		_, err := s.SaveProviderCredential(WithHumanSession(context.Background(), true), Tenant{}, "", tc.provider, "Example", tc.key, nil)
		if !errors.Is(err, hosted.ErrInvalid) {
			t.Fatalf("mismatched key reached storage: %v", err)
		}
	}
}
