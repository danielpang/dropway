package hosted

import (
	"bytes"
	"testing"
)

func TestVaultIsolationAndTampering(t *testing.T) {
	v, e := NewVault(bytes.Repeat([]byte{1}, 32))
	if e != nil {
		t.Fatal(e)
	}
	raw, e := v.Seal("secret-provider-key", "org:owner:id:anthropic")
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(raw, []byte("secret-provider-key")) {
		t.Fatal("plaintext persisted")
	}
	if _, e := v.Open(raw, "org:other:id:anthropic"); e == nil {
		t.Fatal("AAD owner not bound")
	}
	raw[len(raw)-1] ^= 1
	if _, e := v.Open(raw, "org:owner:id:anthropic"); e == nil {
		t.Fatal("tamper accepted")
	}
}
func TestCapabilitiesHaveDistinctClasses(t *testing.T) {
	token, hash, e := Token("attempt", "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222")
	if e != nil || hash == token {
		t.Fatal(e)
	}
	if _, _, e := ParseToken(token, "gateway"); e == nil {
		t.Fatal("attempt accepted as gateway")
	}
	if _, _, e := ParseToken(token, "attempt"); e != nil {
		t.Fatal(e)
	}
}
func TestBudgetAndPinnedModels(t *testing.T) {
	c := Config{Provider: "openrouter", ModelID: "claude-sonnet-4-5", Prompt: "edit"}
	if c.Normalize() == nil {
		t.Fatal("missing provider namespace accepted")
	}
	c.ModelID = "anthropic/claude-sonnet-4-5"
	if e := c.Normalize(); e != nil {
		t.Fatal(e)
	}
	c.Budget.MaxCalls = 101
	if c.Normalize() == nil {
		t.Fatal("unbounded calls")
	}
}

func TestOpenAIPinnedModelsAndRedaction(t *testing.T) {
	for _, model := range []string{"gpt-4.1", "gpt-4.1-mini"} {
		c := Config{Provider: "openai", ModelID: model, Prompt: "edit"}
		if err := c.Normalize(); err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []string{"gpt-4.1-nano", "gpt-5", "anthropic/claude-sonnet-4-5"} {
		if ModelAllowed("openai", model) {
			t.Fatalf("unpinned model accepted: %s", model)
		}
	}
	if Redact("sk-proj-private-provider-secret") != "[redacted]" {
		t.Fatal("OpenAI project secret not redacted")
	}
}
