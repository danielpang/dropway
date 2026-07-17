//go:build cloud

package quota

import (
	"testing"

	corequota "github.com/danielpang/dropway/internal/quota"
)

// TestChatMessageBands asserts the chat-log depth bands: free mirrors the
// window as a defensive hard cap, pro hard-caps at 100 → business, and
// business/enterprise are unlimited.
func TestChatMessageBands(t *testing.T) {
	p := NewProvider(nil, false)

	if err := p.Allow("free", corequota.ResourceChatMessagePerLog, freeChatMessagesWindow-1); err != nil {
		t.Errorf("free under window: %v", err)
	}
	err := p.Allow("free", corequota.ResourceChatMessagePerLog, freeChatMessagesWindow)
	ex, ok := corequota.AsExceeded(err)
	if !ok || ex.NextTier != string(TierPro) {
		t.Errorf("free at window = %v, want 402 next_tier pro", err)
	}

	if err := p.Allow("pro", corequota.ResourceChatMessagePerLog, proChatMessagesCap-1); err != nil {
		t.Errorf("pro under cap: %v", err)
	}
	err = p.Allow("pro", corequota.ResourceChatMessagePerLog, proChatMessagesCap)
	ex, ok = corequota.AsExceeded(err)
	if !ok || ex.NextTier != string(TierBusiness) {
		t.Errorf("pro at cap = %v, want 402 next_tier business", err)
	}

	for _, tier := range []string{"business", "enterprise"} {
		if err := p.Allow(tier, corequota.ResourceChatMessagePerLog, 1_000_000); err != nil {
			t.Errorf("%s should be unlimited: %v", tier, err)
		}
	}
}

// TestChatLogPerOrgDormant asserts the per-org log count seam is uncapped on
// every tier.
func TestChatLogPerOrgDormant(t *testing.T) {
	p := NewProvider(nil, false)
	for _, tier := range []string{"free", "pro", "business", "enterprise"} {
		if err := p.Allow(tier, corequota.ResourceChatLogPerOrg, 1_000_000); err != nil {
			t.Errorf("%s chat logs should be uncapped: %v", tier, err)
		}
	}
}

// TestRetentionWindow asserts window semantics exist ONLY for (free,
// chat_messages_per_log) — and that the empty tier defaults to free, matching
// AllowN.
func TestRetentionWindow(t *testing.T) {
	p := NewProvider(nil, false)

	if n, ok := p.RetentionWindow("free", corequota.ResourceChatMessagePerLog); !ok || n != freeChatMessagesWindow {
		t.Errorf("free window = (%d,%v), want (%d,true)", n, ok, freeChatMessagesWindow)
	}
	if n, ok := p.RetentionWindow("", corequota.ResourceChatMessagePerLog); !ok || n != freeChatMessagesWindow {
		t.Errorf("empty tier window = (%d,%v), want free default", n, ok)
	}
	for _, tier := range []string{"pro", "business", "enterprise"} {
		if _, ok := p.RetentionWindow(tier, corequota.ResourceChatMessagePerLog); ok {
			t.Errorf("%s should have NO window (paid tiers never auto-delete)", tier)
		}
	}
	if _, ok := p.RetentionWindow("free", corequota.ResourceSitePerOrg); ok {
		t.Error("sites should have no window")
	}

	// The core helper resolves through the interface; Unlimited has no window.
	if n, ok := corequota.RetentionWindow(p, "free", corequota.ResourceChatMessagePerLog); !ok || n != freeChatMessagesWindow {
		t.Errorf("core helper via cloud provider = (%d,%v)", n, ok)
	}
	if _, ok := corequota.RetentionWindow(corequota.Unlimited{}, "free", corequota.ResourceChatMessagePerLog); ok {
		t.Error("Unlimited must report no window (self-host never prunes)")
	}
}
