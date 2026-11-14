package main

import (
	"strings"
	"testing"
)

func TestSchemaCheckConfigurationDoesNotExposeCredentials(t *testing.T) {
	if err := checkDeploymentSchema(""); err == nil {
		t.Fatal("missing database accepted")
	}
	secret := "postgres://user:private-secret@localhost:notaport/db"
	if err := checkDeploymentSchema(secret); err == nil || strings.Contains(err.Error(), "private-secret") {
		t.Fatalf("bad URL must fail without leaking it: %v", err)
	}
}
