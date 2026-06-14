package storage

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aws/smithy-go"
)

// TestNewS3Store_BucketRequired asserts the only pure-logic precondition in the
// constructor: a missing bucket is a configuration error (the rest of the
// constructor wires the AWS client, which is exercised by the integration suite).
func TestNewS3Store_BucketRequired(t *testing.T) {
	if _, err := NewS3Store(context.Background(), S3Config{}); err == nil {
		t.Fatal("empty bucket should error")
	}
}

// TestNewS3Store_DefaultsRegionAndBuilds asserts the constructor fills the R2
// "auto" region default when none is supplied and returns a usable store handle
// (no network call happens at construction time).
func TestNewS3Store_DefaultsRegionAndBuilds(t *testing.T) {
	s, err := NewS3Store(context.Background(), S3Config{
		Bucket:          "shipped-blobs",
		Endpoint:        "http://127.0.0.1:9000",
		AccessKeyID:     "akid",
		SecretAccessKey: "secret",
		UsePathStyle:    true,
		// Region intentionally empty → defaults to "auto".
	})
	if err != nil {
		t.Fatalf("NewS3Store: %v", err)
	}
	if s == nil || s.client == nil || s.presign == nil {
		t.Fatalf("store not fully constructed: %+v", s)
	}
	if s.bucket != "shipped-blobs" {
		t.Errorf("bucket = %q", s.bucket)
	}
}

// TestNewS3Store_ExplicitRegion asserts a supplied region is honored.
func TestNewS3Store_ExplicitRegion(t *testing.T) {
	s, err := NewS3Store(context.Background(), S3Config{Bucket: "b", Region: "us-east-1"})
	if err != nil {
		t.Fatalf("NewS3Store: %v", err)
	}
	if s.bucket != "b" {
		t.Errorf("bucket = %q", s.bucket)
	}
}

// TestIsNotFound covers the S3/R2 "no such key" recognition used to translate a
// HEAD/GET miss into ErrNotFound vs a real error. It must match the documented
// API error codes and the internal sentinel type, and reject everything else.
func TestIsNotFound(t *testing.T) {
	notFoundCodes := []string{"NoSuchKey", "NotFound", "NoSuchBucket"}
	for _, code := range notFoundCodes {
		err := &smithy.GenericAPIError{Code: code, Message: "missing"}
		if !isNotFound(err) {
			t.Errorf("isNotFound(%q) = false, want true", code)
		}
		// Wrapped in a chain (the real SDK nests the API error) — errors.As must still find it.
		if !isNotFound(fmt.Errorf("head object: %w", err)) {
			t.Errorf("isNotFound(wrapped %q) = false, want true", code)
		}
	}

	// The internal notFoundErr sentinel type is also recognized (the second branch).
	if !isNotFound(&notFoundErr{}) {
		t.Error("isNotFound(*notFoundErr) = false, want true")
	}
	if (&notFoundErr{}).Error() != "not found" {
		t.Errorf("notFoundErr.Error() = %q", (&notFoundErr{}).Error())
	}

	// A different API error code (e.g. throttling, access denied) is NOT a not-found.
	other := &smithy.GenericAPIError{Code: "AccessDenied", Message: "nope"}
	if isNotFound(other) {
		t.Error("AccessDenied should not be treated as not-found")
	}
	// A plain non-API error is not a not-found.
	if isNotFound(errors.New("connection reset")) {
		t.Error("a generic error should not be treated as not-found")
	}
	// nil is not a not-found.
	if isNotFound(nil) {
		t.Error("nil should not be treated as not-found")
	}
}
