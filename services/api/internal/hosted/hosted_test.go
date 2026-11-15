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

func TestCurrentClaudeModelsAndKeyProviders(t *testing.T) {
	for _, model := range []string{"claude-opus-5-5", "claude-fable-5-2", "claude-sonnet-5-5", "claude-haiku-5-5"} {
		if !ModelAllowed("anthropic", model) {
			t.Errorf("missing model %s", model)
		}
	}
	for _, model := range []string{"anthropic/claude-opus-5.5", "anthropic/claude-fable-5.2"} {
		if !ModelAllowed("openrouter", model) {
			t.Errorf("missing router model %s", model)
		}
	}
	for key, want := range map[string]string{"sk-ant-example": "anthropic", "sk-or-v1-example": "openrouter", "sk-proj-example": "openai", "sk-svcacct-example": "openai", "sk-ambiguous": ""} {
		if got := KeyProvider(key); got != want {
			t.Errorf("provider = %q, want %q", got, want)
		}
	}
	if ModelAllowed("openai", "claude-opus-5-5") {
		t.Fatal("provider mismatch accepted")
	}
}
