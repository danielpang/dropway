// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package apikeygrant

import "testing"

func TestLegacyKeyUnchanged(t *testing.T) {
	if err := Decide("GET", "/v1/sites", []string{"sites:*"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := Decide("POST", "/v1/sites/abc/publish", []string{"sites:*"}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestSiteRestriction(t *testing.T) {
	site := "site-a"
	if err := Decide("GET", "/v1/sites/site-a/files", []string{"sites:*"}, &site); err != nil {
		t.Fatal(err)
	}
	if err := Decide("GET", "/v1/sites/site-b", []string{"sites:*"}, &site); err == nil {
		t.Fatal("other site must be denied")
	}
	if err := Decide("GET", "/v1/sites", []string{"sites:*"}, &site); err == nil {
		t.Fatal("collection must be denied for a site-restricted key")
	}
	if err := Decide("GET", "/v1/chats", []string{"sites:*"}, &site); err == nil {
		t.Fatal("site-restricted key must not use other routes")
	}
}

func TestRoomsAndCustomScopes(t *testing.T) {
	if err := Decide("GET", "/v1/project-rooms", []string{"sites:*"}, nil); err == nil {
		t.Fatal("keys cannot open project rooms")
	}
	if err := Decide("POST", "/v1/sites/s/publish", []string{"sites:read"}, nil); err == nil {
		t.Fatal("read scope cannot publish")
	}
	if err := Decide("GET", "/v1/skills", []string{"sites:read"}, nil); err == nil {
		t.Fatal("narrow scope is not a full key")
	}
}

func TestMeAlways(t *testing.T) {
	if err := Decide("GET", "/v1/me", nil, nil); err != nil {
		t.Fatal(err)
	}
}
