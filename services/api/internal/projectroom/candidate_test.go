// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package projectroom

import "testing"

func pending() Candidate {
	return Candidate{
		ID: "cand", State: CandidatePending,
		ContentHash: "ab", BaseVersionID: "ver", BaseContentHash: "cd", DiffHash: "ef",
	}
}

func req(human bool) ApprovalRequest {
	return ApprovalRequest{
		CandidateID: "cand", ContentHash: "ab", BaseVersionID: "ver", BaseContentHash: "cd",
		LiveVersionID: "ver", LiveContentHash: "cd", HumanSession: human,
	}
}

func TestDecideApprovalHumanAndExact(t *testing.T) {
	got, err := DecideApproval(pending(), req(true))
	if err != nil || !got.Changed || got.State != CandidateApproved {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestDecideApprovalRejectsAgent(t *testing.T) {
	if _, err := DecideApproval(pending(), req(false)); err != ErrHumanRequired {
		t.Fatalf("err = %v", err)
	}
}

func TestDecideApprovalConflictWhenBaselineMoves(t *testing.T) {
	r := req(true)
	r.LiveVersionID = "other"
	got, err := DecideApproval(pending(), r)
	if err != ErrCandidateConflict || got.State != CandidateConflict || !got.Changed {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestDecideApprovalIdempotentCannotReplay(t *testing.T) {
	c := pending()
	c.State = CandidateApproved
	got, err := DecideApproval(c, req(true))
	if err != nil || !got.Idempotent || got.Changed {
		t.Fatalf("idempotent got %+v err %v", got, err)
	}
	other := req(true)
	other.CandidateID = "other"
	if _, err := DecideApproval(c, other); err != ErrCandidateMismatch {
		t.Fatalf("replay err = %v", err)
	}
	other = req(true)
	other.BaseVersionID = "ver-2"
	if _, err := DecideApproval(c, other); err != ErrCandidateMismatch {
		t.Fatalf("cross-version err = %v", err)
	}
}

func TestRejectIsTerminalAndDefaultDeny(t *testing.T) {
	got, err := DecideReject(pending(), req(true))
	if err != nil || got.State != CandidateRejected {
		t.Fatalf("reject got %+v %v", got, err)
	}
	c := pending()
	c.State = CandidateRejected
	if err := CanApply(c, req(true)); err != ErrCandidateTerminal {
		t.Fatalf("apply rejected = %v", err)
	}
	unknown := pending()
	unknown.State = "mystery"
	if err := CanApply(unknown, req(true)); err != ErrCandidateDenied {
		t.Fatalf("unknown apply = %v", err)
	}
	if _, err := DecideApproval(unknown, req(true)); err != ErrCandidateDenied {
		t.Fatalf("unknown approve = %v", err)
	}
	if _, err := DecideApproval(c, req(true)); err != ErrCandidateTerminal {
		t.Fatalf("approve after reject = %v", err)
	}
}
