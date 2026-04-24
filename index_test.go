package goapiuse

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// TestMain ensures the committed testdata/tiny_index.bin is present and
// regenerates it if the environment variable REGEN_TINY_INDEX is set. This
// keeps the binary artifact reproducible from source without forcing a
// regeneration on every run.
func TestMain(m *testing.M) {
	if os.Getenv("REGEN_TINY_INDEX") != "" {
		if err := writeTinyIndex("testdata/tiny_index.bin"); err != nil {
			panic(err)
		}
	}
	os.Exit(m.Run())
}

func TestLoad_TinyIndex(t *testing.T) {
	idx, err := Load("testdata/tiny_index.bin")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if idx.Len() < 3 {
		t.Fatalf("expected >= 3 APIs, got %d", idx.Len())
	}
	meta := idx.Meta()
	if meta.Version != currentSchemaVersion {
		t.Fatalf("meta version: got %d want %d", meta.Version, currentSchemaVersion)
	}
	if meta.CallCount <= 0 {
		t.Fatalf("meta CallCount should be positive, got %d", meta.CallCount)
	}
}

func TestUsage_Ordering(t *testing.T) {
	idx, err := Load("testdata/tiny_index.bin")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := idx.Usage("context.WithTimeout", 0)
	if len(got) == 0 {
		t.Fatal("expected usages for context.WithTimeout")
	}
	// Dominant shape should be the canonical two-result short-decl with
	// a context and a duration-typed argument.
	if !containsAny(got[0].Pattern, "short-decl[2]", "assign[2]") {
		t.Fatalf("top shape should be two-result assign, got %q", got[0].Pattern)
	}
	// Frequencies are sorted descending.
	for i := 1; i < len(got); i++ {
		if got[i-1].Frequency < got[i].Frequency {
			t.Fatalf("usages not sorted at index %d: %v", i, got)
		}
	}
	// Frequencies should sum to ~1.
	var sum float64
	for _, u := range got {
		sum += u.Frequency
	}
	if sum < 0.999 || sum > 1.001 {
		t.Fatalf("frequencies should sum to 1, got %v", sum)
	}
}

func TestUsage_TopN(t *testing.T) {
	idx, err := Load("testdata/tiny_index.bin")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	full := idx.Usage("context.WithTimeout", 0)
	top1 := idx.Usage("context.WithTimeout", 1)
	if len(top1) != 1 {
		t.Fatalf("topN=1: got %d", len(top1))
	}
	if top1[0].Pattern != full[0].Pattern || top1[0].Frequency != full[0].Frequency {
		t.Fatalf("topN=1 should return first entry; got %+v want %+v", top1[0], full[0])
	}
	// topN greater than available returns the whole list.
	big := idx.Usage("context.WithTimeout", 9999)
	if len(big) != len(full) {
		t.Fatalf("topN huge: got %d want %d", len(big), len(full))
	}
}

func TestUsage_UnknownAPI(t *testing.T) {
	idx, err := Load("testdata/tiny_index.bin")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := idx.Usage("nonexistent.Api", 5)
	if got != nil {
		t.Fatalf("unknown API should return nil, got %v", got)
	}
}

func TestUsage_NilIndex(t *testing.T) {
	var idx *Index
	if got := idx.Usage("context.WithTimeout", 1); got != nil {
		t.Fatalf("nil index: got %v", got)
	}
	if idx.Len() != 0 {
		t.Fatalf("nil index Len: got %d", idx.Len())
	}
	if idx.Meta().Version != 0 {
		t.Fatalf("nil index Meta should be zero, got %+v", idx.Meta())
	}
}

func TestLoadFromFS(t *testing.T) {
	raw, err := os.ReadFile("testdata/tiny_index.bin")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	fsys := fstest.MapFS{
		"apiuse/tiny_index.bin": &fstest.MapFile{Data: raw},
	}
	idx, err := LoadFromFS(fsys, "apiuse/tiny_index.bin")
	if err != nil {
		t.Fatalf("LoadFromFS: %v", err)
	}
	if idx.Len() == 0 {
		t.Fatalf("empty index via FS")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.bin"))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestLoad_CorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.bin")
	if err := os.WriteFile(path, []byte("not a gob stream"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestEncode_RoundTrip(t *testing.T) {
	art := artifactV1{
		Meta: Meta{Source: "synthetic", CallCount: 3},
		Entries: map[string][]Usage{
			"x.Foo": {
				{Pattern: "stmt | args=0", Frequency: 0.5},
				{Pattern: "return | args=1", Frequency: 0.5},
			},
		},
	}
	var buf bytes.Buffer
	if err := encode(&buf, art); err != nil {
		t.Fatalf("encode: %v", err)
	}
	idx, err := decode(&buf)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := idx.Meta().Version; got != currentSchemaVersion {
		t.Fatalf("version: got %d want %d", got, currentSchemaVersion)
	}
	if got := idx.Usage("x.Foo", 0); len(got) != 2 {
		t.Fatalf("round-trip usages: %+v", got)
	}
}

func TestLoad_NewerVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fut.bin")
	f, err := os.Create(path) //nolint:gosec // test tempfile
	if err != nil {
		t.Fatal(err)
	}
	art := artifactV1{
		Meta:    Meta{Version: currentSchemaVersion + 1},
		Entries: map[string][]Usage{"x.Y": {{Pattern: "stmt | args=0", Frequency: 1}}},
	}
	if err := encode(f, art); err != nil {
		t.Fatalf("encode: %v", err)
	}
	_ = f.Close()
	if _, err := Load(path); err == nil {
		t.Fatal("expected version error")
	}
}

// containsAny reports whether s contains any of the substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) == 0 {
			continue
		}
		if indexOf(s, sub) >= 0 {
			return true
		}
	}
	return false
}

// indexOf is strings.Index inlined to keep the test file import-light.
func indexOf(s, sub string) int {
	n, m := len(s), len(sub)
	if m == 0 {
		return 0
	}
	if m > n {
		return -1
	}
	for i := 0; i+m <= n; i++ {
		if s[i:i+m] == sub {
			return i
		}
	}
	return -1
}
