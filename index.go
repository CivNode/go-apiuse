// Package goapiuse is part of the CivNode Training semantic engine.
//
// It loads an offline index of dominant call patterns ("shapes") for Go
// stdlib and popular third-party APIs. The index is built offline by the
// companion cmd/go-apiuse-ingest tool and consumed at runtime by Training
// to show "used in production" sidebars and hover popovers.
//
// See https://github.com/CivNode/go-apiuse for details.
package goapiuse

import (
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
)

// Usage is one call-shape entry for a given fully-qualified API name.
type Usage struct {
	// Pattern is a canonical, compact description of the call shape. For
	// example: "two-result assign, 2 args, arg[1] is duration literal".
	Pattern string

	// Frequency is the fraction (0..1) of all observed calls to the API
	// that match this shape. A value of 1.0 means every call in the
	// corpus used this shape.
	Frequency float64

	// ExampleRepos lists up to five source locations (repo or file paths)
	// where the shape was observed. Purely informational.
	ExampleRepos []string
}

// Index is an opaque handle over a loaded artifact. Safe for concurrent
// reads once Load / LoadFromFS has returned.
type Index struct {
	entries map[string][]Usage
	// meta is optional and set by the ingest tool. Not part of the public
	// contract yet; exposed via String() for diagnostics.
	meta Meta
}

// Meta describes the artifact that was loaded. Stored as the first value in
// the gob stream so that older consumers can still read the entries map.
type Meta struct {
	Version   int    // artifact schema version; current = 1
	BuiltAt   string // RFC3339 timestamp
	Source    string // free-form description of the corpus
	CallCount int    // total number of resolved calls ingested
}

// ArtifactV1 is the on-disk layout, gob-encoded. A nested struct means
// future fields can be added without breaking older consumers as long as we
// only ever add new trailing fields. Exported so the ingest command in
// cmd/go-apiuse-ingest can construct it directly without duplicating the
// definition.
type ArtifactV1 struct {
	Meta    Meta
	Entries map[string][]Usage
}

// artifactV1 is the legacy alias kept so older test helpers continue to
// compile. New code should use ArtifactV1 directly.
type artifactV1 = ArtifactV1

const currentSchemaVersion = 1

// Load reads an index from the filesystem. It is a thin wrapper over
// LoadFromFS using os.DirFS on the parent directory.
func Load(path string) (*Index, error) {
	f, err := os.Open(path) //nolint:gosec // caller chooses path
	if err != nil {
		return nil, fmt.Errorf("goapiuse: open %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return decode(f)
}

// LoadFromFS reads an index from an fs.FS. This is the preferred form when
// the artifact is shipped via embed.FS.
func LoadFromFS(fsys fs.FS, path string) (*Index, error) {
	f, err := fsys.Open(path)
	if err != nil {
		return nil, fmt.Errorf("goapiuse: open %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return decode(f)
}

func decode(r io.Reader) (*Index, error) {
	dec := gob.NewDecoder(r)
	var art artifactV1
	if err := dec.Decode(&art); err != nil {
		return nil, fmt.Errorf("goapiuse: decode artifact: %w", err)
	}
	if art.Meta.Version == 0 {
		// Treat missing version as v1 for forward compatibility with
		// hand-built test fixtures that omit it.
		art.Meta.Version = currentSchemaVersion
	}
	if art.Meta.Version > currentSchemaVersion {
		return nil, fmt.Errorf(
			"goapiuse: artifact version %d newer than supported %d",
			art.Meta.Version, currentSchemaVersion,
		)
	}
	if art.Entries == nil {
		return nil, errors.New("goapiuse: artifact has no entries")
	}
	// Ensure each slice is sorted by descending frequency. We sort on load
	// so that a hand-built fixture or a future ingest bug does not leak
	// through as an unsorted query result.
	for k, list := range art.Entries {
		sortUsages(list)
		art.Entries[k] = list
	}
	return &Index{entries: art.Entries, meta: art.Meta}, nil
}

// Usage returns the top-N shapes for qualName, sorted by descending
// frequency. If qualName is unknown, it returns nil. If topN <= 0 the full
// list is returned.
func (i *Index) Usage(qualName string, topN int) []Usage {
	if i == nil {
		return nil
	}
	list, ok := i.entries[qualName]
	if !ok {
		return nil
	}
	if topN <= 0 || topN >= len(list) {
		out := make([]Usage, len(list))
		copy(out, list)
		return out
	}
	out := make([]Usage, topN)
	copy(out, list[:topN])
	return out
}

// Meta returns the artifact metadata. Zero value if the artifact did not
// carry metadata.
func (i *Index) Meta() Meta {
	if i == nil {
		return Meta{}
	}
	return i.meta
}

// Len returns the number of distinct qualified API names in the index.
func (i *Index) Len() int {
	if i == nil {
		return 0
	}
	return len(i.entries)
}

// sortUsages sorts in place by Frequency descending, then Pattern ascending
// for stable output when frequencies tie.
func sortUsages(u []Usage) {
	sort.SliceStable(u, func(a, b int) bool {
		if u[a].Frequency != u[b].Frequency {
			return u[a].Frequency > u[b].Frequency
		}
		return u[a].Pattern < u[b].Pattern
	})
}

// encode writes an artifact to w. Kept unexported so the public surface is
// the wrapper EncodePublic below, which is also what the ingest cmd uses.
func encode(w io.Writer, art ArtifactV1) error {
	if art.Meta.Version == 0 {
		art.Meta.Version = currentSchemaVersion
	}
	enc := gob.NewEncoder(w)
	if err := enc.Encode(art); err != nil {
		return fmt.Errorf("goapiuse: encode artifact: %w", err)
	}
	return nil
}

// EncodePublic writes an artifact to w. It is the stable entry point the
// ingest command uses to serialise the corpus result. External callers
// outside the CivNode Training toolchain should not rely on this; the gob
// format may evolve between artifact schema versions.
func EncodePublic(w io.Writer, art ArtifactV1) error {
	return encode(w, art)
}
