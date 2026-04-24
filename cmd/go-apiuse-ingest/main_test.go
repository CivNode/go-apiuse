package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	goapiuse "github.com/CivNode/go-apiuse"
)

// TestIngest_ThreeShapesOfWithTimeout builds a tiny module on disk with
// three canonical uses of context.WithTimeout, runs the ingest binary, and
// asserts the resulting index captures each shape with the expected counts.
func TestIngest_ThreeShapesOfWithTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows: tempdir go/packages paths differ")
	}

	dir := t.TempDir()
	modDir := filepath.Join(dir, "demo")
	if err := os.MkdirAll(modDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// A minimal module. Each file is a different canonical call shape.
	writeFile(t, modDir, "go.mod", `module demo

go 1.21
`)

	// Shape A: short-decl[2] | args=2 | context, duration-expr
	writeFile(t, modDir, "a.go", `package demo

import (
	"context"
	"time"
)

func A(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	_ = ctx
}
`)

	// Shape B: assign[2] | args=2 | context, duration-expr
	writeFile(t, modDir, "b.go", `package demo

import (
	"context"
	"time"
)

func B(parent context.Context) {
	var ctx context.Context
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(parent, 2*time.Second)
	_ = ctx
	_ = cancel
}
`)

	// Shape C: short-decl[2] | args=2 | context, duration-expr (same shape
	// as A, intentional, to prove we aggregate).
	writeFile(t, modDir, "c.go", `package demo

import (
	"context"
	"time"
)

func C(parent context.Context, d time.Duration) {
	ctx, cancel := context.WithTimeout(parent, d)
	defer cancel()
	_ = ctx
}
`)

	// Build the ingest binary into the tempdir and run it.
	bin := filepath.Join(dir, "go-apiuse-ingest")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = thisDir(t)
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build ingest: %v\n%s", err, out)
	}

	outPath := filepath.Join(dir, "out.bin")
	run := exec.Command(bin, "-o", outPath, "-source", "test-corpus", modDir)
	run.Env = os.Environ()
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("run ingest: %v\n%s", err, out)
	}

	idx, err := goapiuse.Load(outPath)
	if err != nil {
		t.Fatalf("load result: %v", err)
	}

	usages := idx.Usage("context.WithTimeout", 0)
	if len(usages) == 0 {
		t.Fatal("no usages for context.WithTimeout")
	}

	// We expect the dominant shape to appear twice (A + C) and the
	// assign[2] shape once (B). Check relative frequency, not absolute.
	var shortDecl, assign float64
	for _, u := range usages {
		switch {
		case contains(u.Pattern, "short-decl[2]"):
			shortDecl = u.Frequency
		case contains(u.Pattern, "assign[2]"):
			assign = u.Frequency
		}
	}
	if shortDecl <= assign {
		t.Fatalf("short-decl[2] should dominate assign[2]; got short-decl=%v assign=%v\nall: %+v",
			shortDecl, assign, usages)
	}
	if shortDecl < 0.6 || shortDecl > 0.7 {
		t.Fatalf("expected short-decl frequency ~0.667, got %v", shortDecl)
	}
	if assign < 0.3 || assign > 0.4 {
		t.Fatalf("expected assign frequency ~0.333, got %v", assign)
	}

	// Meta sanity.
	if idx.Meta().Source != "test-corpus" {
		t.Fatalf("meta source: got %q", idx.Meta().Source)
	}
	if idx.Meta().CallCount < 3 {
		t.Fatalf("meta CallCount: got %d", idx.Meta().CallCount)
	}
}

// TestIngest_NoArgs asserts the ingest tool exits with a helpful message
// when no source directories are given.
func TestIngest_NoArgs(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "go-apiuse-ingest")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = thisDir(t)
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	run := exec.Command(bin)
	out, err := run.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit; output: %s", out)
	}
	if !contains(string(out), "usage:") {
		t.Fatalf("expected usage banner; got: %s", out)
	}
}

func writeFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

// thisDir returns the absolute path of the directory holding this test
// file, i.e. cmd/go-apiuse-ingest. We use it as the build directory so
// `go build .` picks up main.go regardless of where go test is invoked.
func thisDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine caller file")
	}
	return filepath.Dir(file)
}

func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	n, m := len(s), len(sub)
	if m > n {
		return false
	}
	for i := 0; i+m <= n; i++ {
		if s[i:i+m] == sub {
			return true
		}
	}
	return false
}
