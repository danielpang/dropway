// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package customdomains

import (
	"context"
	"fmt"
	"sync"
)

// Fake is an in-memory Provider for tests and offline/self-host. CreateCustomHostname
// returns a deterministic id + a synthetic DCV record and parks the hostname in
// `pending`; a test drives the state machine with Advance (pending→verifying→active)
// or AdvanceTo. Status reflects the current state. It is safe for concurrent use.
type Fake struct {
	mu     sync.Mutex
	nextID int
	hosts  map[string]*fakeHost
}

type fakeHost struct {
	hostname string
	state    VerifyState
	tls      bool
}

// NewFake returns an empty Fake provider.
func NewFake() *Fake {
	return &Fake{hosts: map[string]*fakeHost{}}
}

// CreateCustomHostname registers a hostname in `pending` and returns its id + a
// synthetic DCV TXT record.
func (f *Fake) CreateCustomHostname(_ context.Context, hostname string) (CreateResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	id := fmt.Sprintf("cf-fake-%d", f.nextID)
	f.hosts[id] = &fakeHost{hostname: hostname, state: StatePending}
	return CreateResult{
		ID: id,
		DCV: DCVRecord{
			Name:  "_cf-custom-hostname." + hostname,
			Type:  "TXT",
			Value: "dcv-" + id,
		},
	}, nil
}

// Status returns the current state of the hostname.
func (f *Fake) Status(_ context.Context, id string) (StatusResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.hosts[id]
	if !ok {
		return StatusResult{}, fmt.Errorf("customdomains: fake: unknown id %q", id)
	}
	res := StatusResult{State: h.state, TLSIssued: h.tls}
	if h.state != StateActive {
		res.DCV = DCVRecord{Name: "_cf-custom-hostname." + h.hostname, Type: "TXT", Value: "dcv-" + id}
	}
	return res, nil
}

// DeleteCustomHostname removes the hostname from the in-memory store. Idempotent:
// an unknown id is a no-op (matches the real provider's tolerant delete).
func (f *Fake) DeleteCustomHostname(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.hosts, id)
	return nil
}

// AdvanceTo forces a hostname to a state (test helper). Active implies TLS issued.
func (f *Fake) AdvanceTo(id string, state VerifyState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.hosts[id]
	if !ok {
		return fmt.Errorf("customdomains: fake: unknown id %q", id)
	}
	h.state = state
	h.tls = state == StateActive
	return nil
}
