// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package projection

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/danielpang/shipped/internal/edgerevoke"
)

// Local is a Writer that keeps the projection in-memory and, optionally, mirrors
// it to a JSON file on disk. It backs local/offline dev (the self-host serving
// path stands in for Cloudflare KV) and unit/integration tests, where a test can
// read Snapshot() to assert exactly what the Go API projected. It is safe for
// concurrent use.
//
// When Path is non-empty, every mutation rewrites the file so an out-of-process
// reader (a local serving shim) can pick it up. When empty, it is purely
// in-memory.
//
// NOT PERSISTED ACROSS RESTARTS (dev/self-host only): the denylist (revoked:*) and
// the org-status (org_status:*) projections are kept ONLY in memory — they are NOT
// written to Path (only the route map is mirrored to disk). After a process restart
// they start empty. This is acceptable by design: both projections fail CLOSED
// (a missing denylist entry forces an extra re-auth; a missing org_status is
// re-derived on the next billing webhook) and are fully REBUILDABLE from Postgres
// (the source of truth), so a dev restart never opens access or loses durable
// state. Production uses CloudflareKV, which is durable.
type Local struct {
	Path string // optional: JSON file the ROUTE projection is mirrored to (not denylist/org-status)

	mu        sync.RWMutex
	routes    map[string]RouteValue
	revoked   map[string]edgerevoke.Value // denylist key (edgerevoke.Key) → value (in-memory only)
	orgStatus map[string]string           // orgID → status (in-memory only); absent/"active" = served
}

// NewLocal returns an empty in-memory Local writer. Pass Path via the struct
// literal (or NewLocalFile) to also mirror to disk.
func NewLocal() *Local {
	return &Local{routes: map[string]RouteValue{}, revoked: map[string]edgerevoke.Value{}, orgStatus: map[string]string{}}
}

// NewLocalFile returns a Local writer that mirrors to the given JSON file,
// loading any existing routes from it first.
func NewLocalFile(path string) (*Local, error) {
	l := &Local{Path: path, routes: map[string]RouteValue{}, revoked: map[string]edgerevoke.Value{}, orgStatus: map[string]string{}}
	if err := l.load(); err != nil {
		return nil, err
	}
	return l, nil
}

// PutRoute upserts a route.
func (l *Local) PutRoute(_ context.Context, host string, val RouteValue) error {
	if err := val.Validate(); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.routes[host] = val
	return l.flushLocked()
}

// DeleteRoute removes a route.
func (l *Local) DeleteRoute(_ context.Context, host string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.routes, host)
	return l.flushLocked()
}

// RebuildFromDB replaces the entire projection with routes (the rebuildable-
// from-Postgres invariant: a wipe + replay restores serving exactly).
func (l *Local) RebuildFromDB(_ context.Context, routes map[string]RouteValue) error {
	for host, v := range routes {
		if err := v.Validate(); err != nil {
			return fmt.Errorf("rebuild: route %s: %w", host, err)
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.routes = make(map[string]RouteValue, len(routes))
	for host, v := range routes {
		l.routes[host] = v
	}
	return l.flushLocked()
}

// Revoke records (or tightens) a denylist entry for (kind, id). Idempotent: an
// existing entry with a LATER min_iat is preserved (max wins), so the denylist
// only ever tightens (the edgerevoke contract).
func (l *Local) Revoke(_ context.Context, kind edgerevoke.Kind, id string, minIAT int64) error {
	if !kind.Valid() {
		return fmt.Errorf("projection: invalid revoke kind %q", kind)
	}
	if id == "" {
		return fmt.Errorf("projection: revoke id is empty")
	}
	v := edgerevoke.Value{MinIAT: minIAT}
	if err := v.Validate(); err != nil {
		return err
	}
	key := edgerevoke.Key(kind, id)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.revoked == nil {
		l.revoked = map[string]edgerevoke.Value{}
	}
	if cur, ok := l.revoked[key]; ok && cur.MinIAT >= v.MinIAT {
		return nil // existing entry is at least as tight; keep it (max wins)
	}
	l.revoked[key] = v
	return nil
}

// GetRevoked returns the denylist entry for (kind, id) (test/serving-shim helper).
func (l *Local) GetRevoked(kind edgerevoke.Kind, id string) (edgerevoke.Value, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	v, ok := l.revoked[edgerevoke.Key(kind, id)]
	return v, ok
}

// LookupRevoked is GetRevoked with the ctx+error signature the API's mint-path
// RevocationReader expects (H2). Local reads from memory and never errors.
func (l *Local) LookupRevoked(_ context.Context, kind edgerevoke.Kind, id string) (edgerevoke.Value, bool, error) {
	v, ok := l.GetRevoked(kind, id)
	return v, ok, nil
}

// SetOrgStatus projects the org's suspension/over-limit signal in memory. A blocking
// status records org_status:<orgID>; "active" (or "") CLEARS it (the org is served).
// In-memory only and not persisted across restarts (see the Local doc comment) —
// acceptable because it fails closed and is rebuildable from the DB.
func (l *Local) SetOrgStatus(_ context.Context, orgID, status string) error {
	if orgID == "" {
		return fmt.Errorf("projection: SetOrgStatus with empty orgID")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.orgStatus == nil {
		l.orgStatus = map[string]string{}
	}
	if status == "" || status == OrgStatusActive {
		delete(l.orgStatus, orgID)
		return nil
	}
	l.orgStatus[orgID] = status
	return nil
}

// GetOrgStatus returns the projected status for an org (test/serving-shim helper).
// ok=false when no blocking status is set (the org is served).
func (l *Local) GetOrgStatus(orgID string) (string, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	v, ok := l.orgStatus[orgID]
	return v, ok
}

// Get returns the route for a host (test/serving-shim helper).
func (l *Local) Get(host string) (RouteValue, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	v, ok := l.routes[host]
	return v, ok
}

// Snapshot returns a copy of the full projection (test helper).
func (l *Local) Snapshot() map[string]RouteValue {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make(map[string]RouteValue, len(l.routes))
	for k, v := range l.routes {
		out[k] = v
	}
	return out
}

// load reads the mirror file into memory (no-op when Path is empty or absent).
func (l *Local) load() error {
	if l.Path == "" {
		return nil
	}
	b, err := os.ReadFile(l.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, &l.routes)
}

// flushLocked writes the in-memory map to the mirror file. Caller holds l.mu.
func (l *Local) flushLocked() error {
	if l.Path == "" {
		return nil
	}
	// Stable, sorted output so the file diffs cleanly and tests are deterministic.
	type kv struct {
		Host string     `json:"host"`
		Val  RouteValue `json:"value"`
	}
	hosts := make([]string, 0, len(l.routes))
	for h := range l.routes {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)

	// Marshal as the host→value map (matches load()'s shape).
	b, err := json.MarshalIndent(l.routes, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(l.Path, b, 0o644)
}

// Ensure Local satisfies Writer, Revoker, and OrgStatusWriter.
var (
	_ Writer          = (*Local)(nil)
	_ Revoker         = (*Local)(nil)
	_ OrgStatusWriter = (*Local)(nil)
)
