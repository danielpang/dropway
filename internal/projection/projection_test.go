package projection

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"
)

// TestRouteValue_RoundTrip_MatchesSchema asserts the Go RouteValue serializes to
// EXACTLY the field set the cross-language contract requires
// (contracts/kv-route.schema.json + @shipped/contracts). This is the Go side of
// the §13 row-11 round-trip: the Worker (TS) parses the same JSON with
// parseKVRouteValue, which rejects unknown/missing fields and schema_version
// drift.
//
// Documented TS parse (the matching half of the round-trip, asserted in CI by the
// `web` job over contracts/):
//
//	import { parseKVRouteValue } from "@shipped/contracts";
//	const v = parseKVRouteValue(JSON.parse(goWrittenJSON));
//	// v.org_id / v.site_id / v.version_id / v.access_mode / v.schema_version
//
// parseKVRouteValue throws on any unknown field, bad UUID, bad enum, or a
// schema_version outside the accepted range — so if the Go struct here drifts
// (adds a field, renames a tag, bumps the version out of lock-step) the TS parse
// fails. As of Phase 2 the contract is at v2 (adds optional expires_at); a value
// WITHOUT expires_at must still serialize to exactly the required field set so the
// round-trip stays compact and v1↔v2 compatible.
func TestRouteValue_RoundTrip_MatchesSchema(t *testing.T) {
	v := RouteValue{
		OrgID:         "11111111-1111-1111-1111-111111111111",
		SiteID:        "22222222-2222-2222-2222-222222222222",
		VersionID:     "33333333-3333-3333-3333-333333333333",
		AccessMode:    AccessPublic,
		SchemaVersion: SchemaVersion,
	}
	if err := v.Validate(); err != nil {
		t.Fatalf("valid value rejected: %v", err)
	}

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	// The serialized object must carry EXACTLY the schema's required fields
	// (expires_at is optional and omitted here, so it must NOT appear).
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	gotKeys := keys(got)
	wantKeys := requiredKeysFromSchema(t)
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("serialized fields %v != schema required %v", gotKeys, wantKeys)
	}

	// Round-trip back into the Go struct: must be identical (no lossy field).
	var back RouteValue
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back != v {
		t.Fatalf("round-trip changed value: %+v != %+v", back, v)
	}

	// schema_version must equal the contract's SCHEMA_VERSION (=2).
	if got["schema_version"].(float64) != float64(SchemaVersion) {
		t.Fatalf("schema_version = %v, want %d", got["schema_version"], SchemaVersion)
	}
}

// TestRouteValue_RoundTrip_WithExpiry exercises the v2 optional expires_at field:
// when set it serializes as an extra key and round-trips losslessly.
func TestRouteValue_RoundTrip_WithExpiry(t *testing.T) {
	v := RouteValue{
		OrgID:         "11111111-1111-1111-1111-111111111111",
		SiteID:        "22222222-2222-2222-2222-222222222222",
		VersionID:     "33333333-3333-3333-3333-333333333333",
		AccessMode:    AccessPublic,
		SchemaVersion: SchemaVersion,
		ExpiresAt:     "2026-12-31T23:59:59Z",
	}
	if err := v.Validate(); err != nil {
		t.Fatalf("valid value rejected: %v", err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["expires_at"] != "2026-12-31T23:59:59Z" {
		t.Fatalf("expires_at not serialized: %v", got["expires_at"])
	}
	var back RouteValue
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back != v {
		t.Fatalf("round-trip changed value: %+v != %+v", back, v)
	}

	// A bad RFC3339 expires_at is rejected by Validate.
	bad := v
	bad.ExpiresAt = "not-a-timestamp"
	if err := bad.Validate(); err == nil {
		t.Fatal("expected validation error for bad expires_at")
	}
}

func TestRouteValue_Validate_Rejects(t *testing.T) {
	cases := map[string]RouteValue{
		"missing org":    {SiteID: "s", VersionID: "v", AccessMode: AccessPublic, SchemaVersion: SchemaVersion},
		"bad mode":       {OrgID: "o", SiteID: "s", VersionID: "v", AccessMode: "nope", SchemaVersion: SchemaVersion},
		"schema too new": {OrgID: "o", SiteID: "s", VersionID: "v", AccessMode: AccessPublic, SchemaVersion: SchemaVersion + 1},
		"schema too old": {OrgID: "o", SiteID: "s", VersionID: "v", AccessMode: AccessPublic, SchemaVersion: 0},
		"missing ver":    {OrgID: "o", SiteID: "s", AccessMode: AccessPublic, SchemaVersion: SchemaVersion},
		"missing site":   {OrgID: "o", VersionID: "v", AccessMode: AccessPublic, SchemaVersion: SchemaVersion},
	}
	for name, v := range cases {
		if err := v.Validate(); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

// TestRouteValue_Validate_AcceptsV1 asserts a stored v1 value (no expires_at) is
// still accepted on the way in, so a rebuild that replays old projection rows
// isn't rejected (backward-compatible parse).
func TestRouteValue_Validate_AcceptsV1(t *testing.T) {
	v := RouteValue{
		OrgID: "o", SiteID: "s", VersionID: "v",
		AccessMode: AccessPublic, SchemaVersion: MinSchemaVersion,
	}
	if err := v.Validate(); err != nil {
		t.Fatalf("v1 value rejected: %v", err)
	}
}

// keys returns the sorted keys of a map.
func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// requiredKeysFromSchema reads contracts/kv-route.schema.json and returns its
// sorted "required" list — the single source of truth both sides bind to.
func requiredKeysFromSchema(t *testing.T) []string {
	t.Helper()
	// The contracts schema lives at repo-root/contracts; this test runs from
	// internal/projection, so walk up two dirs.
	b, err := os.ReadFile("../../contracts/kv-route.schema.json")
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(b, &schema); err != nil {
		t.Fatal(err)
	}
	sort.Strings(schema.Required)
	return schema.Required
}
