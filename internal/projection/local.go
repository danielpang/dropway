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
type Local struct {
	Path string // optional: JSON file the projection is mirrored to

	mu     sync.RWMutex
	routes map[string]RouteValue
}

// NewLocal returns an empty in-memory Local writer. Pass Path via the struct
// literal (or NewLocalFile) to also mirror to disk.
func NewLocal() *Local {
	return &Local{routes: map[string]RouteValue{}}
}

// NewLocalFile returns a Local writer that mirrors to the given JSON file,
// loading any existing routes from it first.
func NewLocalFile(path string) (*Local, error) {
	l := &Local{Path: path, routes: map[string]RouteValue{}}
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

// Ensure Local satisfies Writer.
var _ Writer = (*Local)(nil)
