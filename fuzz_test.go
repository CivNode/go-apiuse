package goapiuse

import (
	"bytes"
	"os"
	"testing"
)

// FuzzDecode runs random byte streams through the decoder. A correctly
// implemented decoder must never panic; it must either decode the input or
// return an error. Seed with a few valid artifacts so the fuzzer has a
// template to mutate.
func FuzzDecode(f *testing.F) {
	// Seed 1: a synthetic artifact built on the fly.
	var buf bytes.Buffer
	_ = encode(&buf, ArtifactV1{
		Meta:    Meta{Source: "seed"},
		Entries: map[string][]Usage{"x.Y": {{Pattern: "stmt | args=0", Frequency: 1}}},
	})
	f.Add(buf.Bytes())

	// Seed 2: the shipped tiny index.
	if raw, err := os.ReadFile("testdata/tiny_index.bin"); err == nil { //nolint:gosec // test fixture path
		f.Add(raw)
	}

	// Seed 3: a few edge-case byte streams.
	f.Add([]byte{})
	f.Add([]byte{0, 0, 0})
	f.Add([]byte("not a gob stream"))

	f.Fuzz(func(_ *testing.T, data []byte) {
		_, _ = decode(bytes.NewReader(data))
		// Panics are the failure mode; no assertion needed.
	})
}
