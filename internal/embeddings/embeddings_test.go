// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package embeddings

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func serve(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, APIKey: "test-key", HTTPClient: srv.Client()}
}

func TestEmbedOrdersByIndexAndSendsAuth(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		var body struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != DefaultModel {
			t.Errorf("model = %q", body.Model)
		}
		// Reply out of order; the client must place by index.
		fmt.Fprintf(w, `{"data":[{"index":1,"embedding":[2]},{"index":0,"embedding":[1]}]}`)
	})
	vecs, err := c.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 || vecs[0][0] != 1 || vecs[1][0] != 2 {
		t.Errorf("vectors misordered: %v", vecs)
	}
}

func TestEmbedRetriesOn429(t *testing.T) {
	var calls atomic.Int32
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `{"data":[{"index":0,"embedding":[3]}]}`)
	})
	vecs, err := c.Embed(context.Background(), []string{"x"})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", calls.Load())
	}
	if len(vecs) != 1 || vecs[0][0] != 3 {
		t.Errorf("vecs = %v", vecs)
	}
}

func TestEmbedDoesNotRetryOn400(t *testing.T) {
	var calls atomic.Int32
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, `{"error":"bad input"}`, http.StatusBadRequest)
	})
	if _, err := c.Embed(context.Background(), []string{"x"}); err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1 (no retry on 4xx)", calls.Load())
	}
}

func TestEmbedCountMismatchFails(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"index":0,"embedding":[1]}]}`)
	})
	if _, err := c.Embed(context.Background(), []string{"a", "b"}); err == nil {
		t.Fatal("expected count-mismatch error")
	}
}

func TestEmbedEmptyInputIsNoop(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1"} // would fail if dialed
	vecs, err := c.Embed(context.Background(), nil)
	if err != nil || vecs != nil {
		t.Errorf("Embed(nil) = %v, %v", vecs, err)
	}
}
