package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielpang/dropway/cli/internal/api"
)

func runRead(t *testing.T, client api.ReadClient, args ...string) (string, error) {
	t.Helper()
	t.Setenv("DROPWAY_API_KEY", "test-token")
	cmd := newReadCmd(readFactoryOf(client))
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

func TestRead_DirectURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<h1>hello</h1>"))
	}))
	defer srv.Close()

	// A full URL is fetched directly; no ReadClient call is needed.
	out, err := runRead(t, nil, srv.URL)
	if err != nil {
		t.Fatalf("read url: %v", err)
	}
	if out != "<h1>hello</h1>" {
		t.Errorf("body not streamed verbatim: %q", out)
	}
}

func TestRead_ResolvesSlug(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("SLUG BODY"))
	}))
	defer srv.Close()

	client := &fakeReadClient{
		userID: "user_me",
		sites:  []api.Site{{Slug: "demo", OwnerID: "user_me", LiveURL: srv.URL}},
	}
	out, err := runRead(t, client, "demo")
	if err != nil {
		t.Fatalf("read slug: %v", err)
	}
	if out != "SLUG BODY" {
		t.Errorf("slug not resolved + fetched: %q", out)
	}
}

func TestRead_UnknownSlug(t *testing.T) {
	client := &fakeReadClient{userID: "user_me", sites: []api.Site{{Slug: "other", LiveURL: "u"}}}
	_, err := runRead(t, client, "missing")
	if err == nil || !strings.Contains(err.Error(), "no site with slug") {
		t.Errorf("expected unknown-slug error, got %v", err)
	}
}

func TestRead_Non2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := runRead(t, nil, srv.URL)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("expected 404 error, got %v", err)
	}
}

func TestIsHTTPURL(t *testing.T) {
	for _, s := range []string{"http://x", "https://x/y"} {
		if !isHTTPURL(s) {
			t.Errorf("%q should be a URL", s)
		}
	}
	for _, s := range []string{"demo", "my-site", "ftp://x"} {
		if isHTTPURL(s) {
			t.Errorf("%q should be treated as a slug", s)
		}
	}
}
