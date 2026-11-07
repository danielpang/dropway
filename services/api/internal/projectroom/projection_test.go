// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package projectroom

import (
	"reflect"
	"strings"
	"testing"

	"github.com/danielpang/dropway/internal/projection"
)

func TestPublicRouteHasNoReviewFields(t *testing.T) {
	typ := reflect.TypeOf(projection.RouteValue{})
	for i := 0; i < typ.NumField(); i++ {
		name := strings.ToLower(typ.Field(i).Name + typ.Field(i).Tag.Get("json"))
		for _, bad := range []string{"review", "comment", "candidate", "room"} {
			if strings.Contains(name, bad) {
				t.Fatalf("public route field %s", typ.Field(i).Name)
			}
		}
	}
}
