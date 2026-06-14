// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package edgerevoke

import (
	"encoding/json"
	"testing"
)

func TestKey(t *testing.T) {
	cases := []struct {
		kind Kind
		id   string
		want string
	}{
		{KindUser, "u1", "revoked:user:u1"},
		{KindSite, "s1", "revoked:site:s1"},
		{KindOrg, "o1", "revoked:org:o1"},
	}
	for _, c := range cases {
		if got := Key(c.kind, c.id); got != c.want {
			t.Errorf("Key(%q,%q) = %q, want %q", c.kind, c.id, got, c.want)
		}
	}
}

func TestKindValid(t *testing.T) {
	for _, k := range []Kind{KindUser, KindSite, KindOrg} {
		if !k.Valid() {
			t.Errorf("%q should be valid", k)
		}
	}
	if Kind("bogus").Valid() {
		t.Error("bogus kind should be invalid")
	}
}

func TestValueValidate(t *testing.T) {
	if err := (Value{MinIAT: 1700000000}).Validate(); err != nil {
		t.Errorf("valid value rejected: %v", err)
	}
	if err := (Value{MinIAT: 0}).Validate(); err == nil {
		t.Error("zero min_iat should be rejected")
	}
	if err := (Value{MinIAT: -1}).Validate(); err == nil {
		t.Error("negative min_iat should be rejected")
	}
}

// TestValueJSONContract pins the wire shape the Worker/authz parse: a single
// `min_iat` integer field. A drift here breaks cross-language revocation.
func TestValueJSONContract(t *testing.T) {
	b, err := json.Marshal(Value{MinIAT: 1700000000})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"min_iat":1700000000}` {
		t.Fatalf("denylist value JSON drift: %s", b)
	}
	var v Value
	if err := json.Unmarshal([]byte(`{"min_iat":42}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.MinIAT != 42 {
		t.Fatalf("parse: got %d", v.MinIAT)
	}
}
