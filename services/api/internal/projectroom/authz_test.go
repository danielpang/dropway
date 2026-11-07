// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package projectroom

import "testing"

func TestPermissionMatrix(t *testing.T) {
	read := SiteRights{CanRead: true}
	edit := SiteRights{CanRead: true, CanEditContent: true}
	none := SiteRights{}

	cases := []struct {
		name string
		role Role
		site SiteRights
		ok   func(Role, SiteRights) bool
		want bool
	}{
		{"owner comments", RoleOwner, read, CanComment, true},
		{"editor comments", RoleEditor, read, CanComment, true},
		{"reviewer comments", RoleReviewer, read, CanComment, true},
		{"viewer cannot comment", RoleViewer, read, CanComment, false},
		{"comment requires site read", RoleOwner, none, CanComment, false},
		{"reviewer resolves", RoleReviewer, read, CanResolve, true},
		{"viewer cannot resolve", RoleViewer, read, CanResolve, false},
		{"reviewer approves", RoleReviewer, read, CanApprove, true},
		{"viewer cannot approve", RoleViewer, read, CanApprove, false},
		{"owner runs with edit", RoleOwner, edit, CanRun, true},
		{"editor runs with edit", RoleEditor, edit, CanRun, true},
		{"editor cannot run without edit", RoleEditor, read, CanRun, false},
		{"reviewer cannot run", RoleReviewer, edit, CanRun, false},
		{"viewer cannot run", RoleViewer, edit, CanRun, false},
		{"accept matches run", RoleEditor, edit, CanAccept, true},
		{"reviewer cannot accept", RoleReviewer, edit, CanAccept, false},
		{"only owner manages members", RoleOwner, read, func(r Role, _ SiteRights) bool { return CanManageMembers(r) }, true},
		{"editor cannot manage members", RoleEditor, read, func(r Role, _ SiteRights) bool { return CanManageMembers(r) }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.ok(tc.role, tc.site); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseRole(t *testing.T) {
	if _, ok := ParseRole("admin"); ok {
		t.Fatal("org admin is not a room role")
	}
	if r, ok := ParseRole("reviewer"); !ok || r != RoleReviewer {
		t.Fatalf("reviewer parse = %q %v", r, ok)
	}
}
