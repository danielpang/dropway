// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package store

import (
	"github.com/danielpang/dropway/services/api/internal/store/db"
	"github.com/jackc/pgx/v5/pgtype"
	"testing"
	"time"
)

func TestGCReviewVersionsSurviveZeroRetention(t *testing.T) {
	rows := []db.ListVersionsForGCRow{
		{VersionID: "live", SiteID: "site", IsCurrent: pgtype.Bool{Bool: true, Valid: true}},
		{VersionID: "candidate", SiteID: "site"},
		{VersionID: "base", SiteID: "site"},
		{VersionID: "thread", SiteID: "site"},
		{VersionID: "unreferenced", SiteID: "site"},
	}
	pins := map[string]bool{"candidate": true, "base": true, "thread": true, "live": true}
	retained := retainReviewVersions(selectRetained(rows, 0, time.Now()), rows, pins)
	got := map[string]int{}
	for _, v := range retained {
		got[v.VersionID]++
	}
	for id := range pins {
		if got[id] != 1 {
			t.Fatalf("%s retained %d times", id, got[id])
		}
	}
	if got["unreferenced"] != 0 {
		t.Fatal("unreferenced version retained")
	}
}
