package goapiuse

import (
	"fmt"
	"os"
	"path/filepath"
)

// writeTinyIndex builds the deterministic test fixture the repo ships. It
// is kept in the package (not the tests) so external callers of ingest can
// sanity-check against the same data.
//
// The snippets it encodes are intentionally hand-picked to resemble what
// the full corpus ingest produces for three well-known APIs. Ten synthetic
// calls map to at most three shapes per API, matching the "aggressive
// clustering" constraint from the spec.
func writeTinyIndex(path string) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %q: %w", dir, err)
		}
	}
	art := artifactV1{
		Meta: Meta{
			Version:   currentSchemaVersion,
			BuiltAt:   "2026-04-24T00:00:00Z",
			Source:    "hand-built tiny index for tests (10 snippets across 3 APIs)",
			CallCount: 10,
		},
		Entries: tinyIndexEntries(),
	}
	f, err := os.Create(path) //nolint:gosec // deterministic fixture path
	if err != nil {
		return fmt.Errorf("create %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return encode(f, art)
}

func tinyIndexEntries() map[string][]Usage {
	// context.WithTimeout: 5 calls, 2 shapes.
	//   4x "ctx, cancel := context.WithTimeout(parent, 5*time.Second)"
	//   1x "ctx, cancel = context.WithTimeout(ctx, d)"          (reassign)
	ctxUsages := []Usage{
		{
			Pattern:   "short-decl[2] | args=2 | context, duration",
			Frequency: 0.8,
			ExampleRepos: []string{
				"github.com/example/app/server.go#L42",
				"github.com/example/worker/main.go#L17",
			},
		},
		{
			Pattern:   "assign[2] | args=2 | context, duration",
			Frequency: 0.2,
			ExampleRepos: []string{
				"github.com/example/client/retry.go#L88",
			},
		},
	}

	// net/http.HandlerFunc: 3 calls, 2 shapes.
	//   2x http.HandlerFunc(h) as an argument passed to http.Handle or
	//     similar (context is arg-of-call)
	//   1x bare cast stored in a var (short-decl[1])
	handlerUsages := []Usage{
		{
			Pattern:   "arg-of-call | args=1 | ident",
			Frequency: 0.6667,
			ExampleRepos: []string{
				"github.com/example/app/routes.go#L12",
				"github.com/example/app/admin.go#L44",
			},
		},
		{
			Pattern:   "short-decl[1] | args=1 | ident",
			Frequency: 0.3333,
			ExampleRepos: []string{
				"github.com/example/app/middleware.go#L5",
			},
		},
	}

	// sync.Mutex.Lock: 2 calls, 1 shape.
	//   2x mu.Lock() as a bare statement.
	mutexLockUsages := []Usage{
		{
			Pattern:   "stmt | args=0",
			Frequency: 1.0,
			ExampleRepos: []string{
				"github.com/example/app/cache.go#L21",
				"github.com/example/app/store.go#L34",
			},
		},
	}

	return map[string][]Usage{
		"context.WithTimeout":  ctxUsages,
		"net/http.HandlerFunc": handlerUsages,
		"sync.Mutex.Lock":      mutexLockUsages,
	}
}
