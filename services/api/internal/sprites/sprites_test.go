// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package sprites

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestDisabledDoesNotStart(t *testing.T) {
	var r Runner = Disabled{}
	if _, err := r.Start(context.Background(), AttemptSpec{OrgID: "o", AttemptID: "a"}); err != ErrExecutionDisabled {
		t.Fatal(err)
	}
	if err := r.Destroy(context.Background(), Handle{}, "clarification"); err != nil {
		t.Fatal(err)
	}
}

func TestSpecHasNoSecrets(t *testing.T) {
	typ := reflect.TypeOf(AttemptSpec{})
	for i := 0; i < typ.NumField(); i++ {
		name := strings.ToLower(typ.Field(i).Name)
		for _, bad := range []string{"key", "token", "secret", "fly"} {
			if strings.Contains(name, bad) {
				t.Fatalf("AttemptSpec field %s looks like a credential", typ.Field(i).Name)
			}
		}
	}
}
