// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package orchestrate

import "testing"

func TestToolsAreRemoteAndPiIsUnverified(t *testing.T) {
	if PiSDKCompatibilityVerified {
		t.Fatal("Pi SDK compatibility must not be claimed")
	}
	if LocalFallbackAllowed() || AutoloadExtensions() {
		t.Fatal("local fallback and extension autoload must stay off")
	}
	p, err := Place("bash")
	if err != nil || p.Backend != "sprite" {
		t.Fatalf("bash = %+v %v", p, err)
	}
	p, err = Place("get_thread")
	if err != nil || p.Backend != "dropway_api" {
		t.Fatalf("thread tool = %+v %v", p, err)
	}
	if _, err := Place("local_shell"); err == nil {
		t.Fatal("unknown tool must fail")
	}
}

func TestClarificationKeepsPinAndReleases(t *testing.T) {
	snap := SessionSnapshot{InitiatorUserID: "u", CredentialRef: "c", ModelID: "m", HarnessID: HarnessPi, HarnessVersion: "0"}
	plan := PlanClarification(snap)
	if !plan.PersistSessionInDropway || !plan.ReleaseWorker || !plan.ReleaseSprite || !plan.ContinuationFreshAttempt {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.Pinned != snap {
		t.Fatal("continuation must keep the pinned snapshot")
	}
}

func TestNoTeammateKeyReuse(t *testing.T) {
	if CredentialOwnedBy("initiator", "owner") {
		t.Fatal("room owner key must not be reused")
	}
	if !CredentialOwnedBy("initiator", "initiator") {
		t.Fatal("initiator may use their own credential reference")
	}
}
