// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package projectroom

import "errors"

// Candidate states. Pending is the only non-terminal state. There is no
// timeout and no path that treats unknown as approved.
const (
	CandidatePending  = "pending"
	CandidateApproved = "approved"
	CandidateRejected = "rejected"
	CandidateConflict = "conflict"
)

var (
	// ErrCandidateDenied is the default for pending, unknown, or non-human callers.
	ErrCandidateDenied = errors.New("projectroom: candidate application denied")
	// ErrCandidateTerminal means reject or conflict already closed the row.
	ErrCandidateTerminal = errors.New("projectroom: candidate decision is terminal")
	// ErrCandidateMismatch means the id, hash, or base version is not this candidate.
	ErrCandidateMismatch = errors.New("projectroom: candidate identity mismatch")
	// ErrCandidateConflict means the live baseline moved.
	ErrCandidateConflict = errors.New("projectroom: live baseline changed")
	// ErrApprovalIdempotent means this candidate was already approved. Callers
	// must not record a second effect or reuse the decision for another candidate.
	ErrApprovalIdempotent = errors.New("projectroom: approval already recorded")
	// ErrHumanRequired means the caller is not a human session.
	ErrHumanRequired = errors.New("projectroom: human session required")
)

// Candidate is the immutable proposal the server stores.
type Candidate struct {
	ID              string
	State           string
	ContentHash     string
	BaseVersionID   string
	BaseContentHash string
	DiffHash        string
}

// ApprovalRequest is the exact artifact a human approves. It cannot be
// replayed against another candidate or version: every field must match.
type ApprovalRequest struct {
	CandidateID     string
	ContentHash     string
	BaseVersionID   string
	BaseContentHash string
	// LiveVersionID and LiveContentHash are the site's current baseline.
	// Empty live baseline is a conflict, not an implicit approval.
	LiveVersionID   string
	LiveContentHash string
	HumanSession    bool
	// VerifiedVersionID is set only by the server after verifying the complete
	// manifest and blobs. It is never decoded from the approval payload.
	VerifiedVersionID string
}

// ApprovalResult is a single effect. Idempotent is true when the row was
// already approved and no second audit effect should be written. Changed is
// true only on the pending → approved transition.
type ApprovalResult struct {
	State      string
	Changed    bool
	Idempotent bool
}

// DecideApproval applies the human approve-change rules. Reviewer version
// sign-off is a different operation and must not call this.
func DecideApproval(c Candidate, req ApprovalRequest) (ApprovalResult, error) {
	if !req.HumanSession {
		return ApprovalResult{}, ErrHumanRequired
	}
	if !sameArtifact(c, req) {
		return ApprovalResult{}, ErrCandidateMismatch
	}
	switch c.State {
	case CandidateApproved:
		return ApprovalResult{State: CandidateApproved, Idempotent: true}, nil
	case CandidateRejected, CandidateConflict:
		return ApprovalResult{}, ErrCandidateTerminal
	case CandidatePending:
		if req.LiveVersionID == "" || req.LiveContentHash == "" ||
			req.LiveVersionID != c.BaseVersionID || req.LiveContentHash != c.BaseContentHash {
			return ApprovalResult{State: CandidateConflict, Changed: true}, ErrCandidateConflict
		}
		return ApprovalResult{State: CandidateApproved, Changed: true}, nil
	default:
		// Unknown state rejects. Nothing is approved by default.
		return ApprovalResult{}, ErrCandidateDenied
	}
}

// DecideReject terminally blocks application. A second reject of the same
// candidate is idempotent and must not be recorded as a second effect.
func DecideReject(c Candidate, req ApprovalRequest) (ApprovalResult, error) {
	if !req.HumanSession {
		return ApprovalResult{}, ErrHumanRequired
	}
	if !sameArtifact(c, req) {
		return ApprovalResult{}, ErrCandidateMismatch
	}
	switch c.State {
	case CandidateRejected:
		return ApprovalResult{State: CandidateRejected, Idempotent: true}, nil
	case CandidateApproved, CandidateConflict:
		return ApprovalResult{}, ErrCandidateTerminal
	case CandidatePending:
		return ApprovalResult{State: CandidateRejected, Changed: true}, nil
	default:
		return ApprovalResult{}, ErrCandidateDenied
	}
}

// CanApply reports whether a candidate may be applied. Approved is necessary
// and not sufficient: application also checks the locked live baseline. Pending, rejected,
// conflict, and unknown are denied.
func CanApply(c Candidate, req ApprovalRequest) error {
	if !req.HumanSession {
		return ErrHumanRequired
	}
	if !sameArtifact(c, req) {
		return ErrCandidateMismatch
	}
	switch c.State {
	case CandidateApproved:
		return nil
	case CandidateRejected, CandidateConflict:
		return ErrCandidateTerminal
	default:
		return ErrCandidateDenied
	}
}

func sameArtifact(c Candidate, req ApprovalRequest) bool {
	return c.ID == req.CandidateID && c.ID != "" &&
		c.ContentHash == req.ContentHash && c.ContentHash != "" &&
		c.BaseVersionID == req.BaseVersionID && c.BaseVersionID != "" &&
		c.BaseContentHash == req.BaseContentHash && c.BaseContentHash != ""
}
