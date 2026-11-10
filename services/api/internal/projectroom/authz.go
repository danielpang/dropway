// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package projectroom is the authorization and anchor contract for same-org
// project rooms. Site reads in Dropway are org-wide (RLS is org_id only);
// this package does not invent a finer per-site read ACL. Content edits
// follow the existing site rule: allow_member_edits, the site creator, or
// an org admin/owner.
package projectroom

// Role is a room member's role. It is not an org role.
type Role string

const (
	RoleOwner    Role = "owner"
	RoleEditor   Role = "editor"
	RoleReviewer Role = "reviewer"
	RoleViewer   Role = "viewer"
)

// SiteRights is the caller's rights on the underlying site. CanRead is true
// for any live org member because site SELECT is org-wide. CanEditContent
// is the existing content-edit gate (deploy/publish), which run and accept
// intersect with.
type SiteRights struct {
	CanRead        bool
	CanEditContent bool
}

// ParseRole accepts the four room roles.
func ParseRole(s string) (Role, bool) {
	switch Role(s) {
	case RoleOwner, RoleEditor, RoleReviewer, RoleViewer:
		return Role(s), true
	default:
		return "", false
	}
}

// CanManageMembers is the room owner. Org admins do not manage a room they
// are not the owner of.
func CanManageMembers(role Role) bool { return role == RoleOwner }

// CanReadRoom is any member.
func CanReadRoom(role Role) bool {
	switch role {
	case RoleOwner, RoleEditor, RoleReviewer, RoleViewer:
		return true
	default:
		return false
	}
}

// CanComment is owner, editor, or reviewer, and only when the caller can
// already read the site. Viewers are read-only. Posting a comment does not
// start an agent.
func CanComment(role Role, site SiteRights) bool {
	if !site.CanRead {
		return false
	}
	switch role {
	case RoleOwner, RoleEditor, RoleReviewer:
		return true
	default:
		return false
	}
}

// CanResolve matches comment rights: reviewers resolve; viewers do not.
func CanResolve(role Role, site SiteRights) bool { return CanComment(role, site) }

// CanApprove is approval of a specific site version, not publish.
func CanApprove(role Role, site SiteRights) bool { return CanComment(role, site) }

// CanReanchor is an explicit anchor replacement. It does not fuzzy-migrate.
func CanReanchor(role Role, site SiteRights) bool { return CanComment(role, site) }

// CanRun is owner or editor intersecting site content-edit rights.
// Reviewers and viewers cannot run.
func CanRun(role Role, site SiteRights) bool {
	if !site.CanEditContent {
		return false
	}
	return role == RoleOwner || role == RoleEditor
}

// CanAccept matches CanRun. Accept is not publish and is not reviewer approval.
func CanAccept(role Role, site SiteRights) bool { return CanRun(role, site) }

// CanAttachSite allows room owners and editors to reference sites they can read.
// Attaching never changes the underlying site's ownership or edit rights.
func CanAttachSite(role Role, site SiteRights) bool {
	return site.CanRead && (role == RoleOwner || role == RoleEditor)
}
