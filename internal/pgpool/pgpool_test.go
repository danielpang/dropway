// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pgpool

import "testing"

// TestMaxConnsFromEnv covers the DB_MAX_CONNS override: a valid positive integer
// wins, and every invalid/absent form falls back to the service's default.
func TestMaxConnsFromEnv(t *testing.T) {
	const def int32 = 8
	cases := []struct {
		name string
		set  bool
		val  string
		want int32
	}{
		{"unset falls back", false, "", def},
		{"empty falls back", true, "", def},
		{"valid override", true, "3", 3},
		{"zero falls back", true, "0", def},
		{"negative falls back", true, "-2", def},
		{"non-numeric falls back", true, "lots", def},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv("DB_MAX_CONNS", tc.val)
			} else {
				// t.Setenv can't unset; the test binary's env has no DB_MAX_CONNS,
				// so the unset case relies on it being absent by default.
			}
			if got := maxConnsFromEnv(def); got != tc.want {
				t.Errorf("maxConnsFromEnv(%d) with DB_MAX_CONNS=%q = %d, want %d", def, tc.val, got, tc.want)
			}
		})
	}
}
