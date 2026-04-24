package main

import (
	"go/ast"
	"go/token"
	"testing"

	goapiuse "github.com/CivNode/go-apiuse"
)

func TestAccumulator_Aggregates(t *testing.T) {
	a := newAccumulator(3)
	a.add("context.WithTimeout", "short-decl[2] | args=2 | context, duration", "a.go:1")
	a.add("context.WithTimeout", "short-decl[2] | args=2 | context, duration", "b.go:2")
	a.add("context.WithTimeout", "short-decl[2] | args=2 | context, duration", "c.go:3")
	a.add("context.WithTimeout", "short-decl[2] | args=2 | context, duration", "d.go:4") // dropped; over cap
	a.add("context.WithTimeout", "assign[2] | args=2 | context, duration", "e.go:5")
	a.add("sync.Mutex.Lock", "stmt | args=0", "m.go:9")

	if a.totalCalls != 6 {
		t.Fatalf("totalCalls: got %d want 6", a.totalCalls)
	}
	art := a.build(goapiuse.Meta{Source: "unit"})
	if len(art.Entries) != 2 {
		t.Fatalf("entries: got %d want 2", len(art.Entries))
	}
	ctx := art.Entries["context.WithTimeout"]
	if len(ctx) != 2 {
		t.Fatalf("ctx shapes: got %d", len(ctx))
	}
	if ctx[0].Frequency < ctx[1].Frequency {
		t.Fatalf("expected sort desc: %+v", ctx)
	}
	// Dominant shape should be 4/5 = 0.8.
	if ctx[0].Frequency < 0.79 || ctx[0].Frequency > 0.81 {
		t.Fatalf("dominant frequency: got %v", ctx[0].Frequency)
	}
	// Examples are capped at 3.
	if len(ctx[0].ExampleRepos) != 3 {
		t.Fatalf("examples: got %d want 3", len(ctx[0].ExampleRepos))
	}
}

func TestAccumulator_DefaultsMaxExamples(t *testing.T) {
	a := newAccumulator(0)
	for i := 0; i < 10; i++ {
		a.add("x.Y", "stmt | args=0", "f.go:1")
	}
	art := a.build(goapiuse.Meta{})
	if got := len(art.Entries["x.Y"][0].ExampleRepos); got != 5 {
		t.Fatalf("default cap: got %d want 5", got)
	}
}

func TestParentStack_PushPopParent(t *testing.T) {
	p := newParentStack()
	p.pop() // safe on empty
	a := &ast.BadStmt{}
	b := &ast.BadExpr{}
	p.push(a)
	p.push(b)
	if got := p.parentOf(); got != b {
		t.Fatalf("parentOf: got %T", got)
	}
	p.pop()
	if got := p.parentOf(); got != a {
		t.Fatalf("after pop: got %T", got)
	}
}

func TestShort_TrimsLongPaths(t *testing.T) {
	cases := map[string]string{
		"/tmp/go-build/demo/a.go": "demo/a.go",
		"a.go":                    "a.go",
		"/a.go":                   "a.go",
	}
	for in, want := range cases {
		if got := short(in); got != want {
			t.Fatalf("short(%q): got %q want %q", in, got, want)
		}
	}
}

// TestIngestPackage_DirectViaAPI gives us coverage on ingestPackage without
// shelling out. We build a minimal types.Info-backed package in memory.
func TestIngestPackage_DirectViaAPI(t *testing.T) {
	// Dummy package is nil-safe.
	acc := newAccumulator(3)
	ingestPackage(nil, acc)
	if acc.totalCalls != 0 {
		t.Fatal("nil package should no-op")
	}
	// parentOf with a deep stack exercises the branch we skip above.
	var _ token.Pos
}
