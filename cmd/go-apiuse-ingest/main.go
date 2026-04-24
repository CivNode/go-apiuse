// go-apiuse-ingest walks one or more Go source trees, resolves the callee
// of every function call, extracts a canonical call-shape for each, and
// writes a gob-encoded index.bin suitable for consumption by the goapiuse
// runtime library.
//
// Usage:
//
//	go-apiuse-ingest -o index.bin -source "corpus snapshot 2026-04-24" dir1 dir2 ...
//
// Each positional argument must be a directory that contains Go source.
// The tool invokes golang.org/x/tools/go/packages with NeedSyntax +
// NeedTypes + NeedTypesInfo, then walks every *ast.CallExpr and records a
// (qualified-callee, shape) pair. Shapes are clustered by identity string
// and converted to frequencies on write.
//
// When type-checking fails for a package, the tool emits a structured
// warning to stderr and continues with the packages it did resolve. The
// exit code is non-zero only if every package failed.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/types"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	goapiuse "github.com/CivNode/go-apiuse"
	"golang.org/x/tools/go/packages"
)

func main() {
	var (
		out        string
		sourceDesc string
		maxRepos   int
		verbose    bool
	)
	flag.StringVar(&out, "o", "index.bin", "output artifact path")
	flag.StringVar(&sourceDesc, "source", "", "free-form description of the corpus")
	flag.IntVar(&maxRepos, "max-examples", 5, "maximum example repos/locations per shape")
	flag.BoolVar(&verbose, "v", false, "verbose per-package logging")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr,
			"usage: %s [-o index.bin] [-source desc] dir [dir ...]\n",
			filepath.Base(os.Args[0]))
		flag.PrintDefaults()
	}
	flag.Parse()

	dirs := flag.Args()
	if len(dirs) == 0 {
		flag.Usage()
		os.Exit(2)
	}

	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	acc := newAccumulator(maxRepos)

	var (
		totalPkgs      int
		typeCheckedOK  int
		typeCheckFails int
	)
	for _, dir := range dirs {
		pkgs, err := loadDir(dir)
		if err != nil {
			log.Error("load failed", "dir", dir, "err", err)
			continue
		}
		for _, pkg := range pkgs {
			totalPkgs++
			if len(pkg.Errors) > 0 {
				typeCheckFails++
				log.Warn("type-check errors",
					"pkg", pkg.PkgPath,
					"first_error", pkg.Errors[0].Error(),
					"error_count", len(pkg.Errors))
				// We still harvest what we can; partial info is better
				// than none for a corpus job.
			} else {
				typeCheckedOK++
			}
			ingestPackage(pkg, acc)
		}
	}

	if totalPkgs > 0 && typeCheckedOK == 0 {
		log.Error("every package failed type-checking", "total", totalPkgs)
		os.Exit(1)
	}

	art := acc.build(goapiuse.Meta{
		BuiltAt:   time.Now().UTC().Format(time.RFC3339),
		Source:    sourceDesc,
		CallCount: acc.totalCalls,
	})

	f, err := os.Create(out) //nolint:gosec // caller chooses path
	if err != nil {
		log.Error("create output", "path", out, "err", err)
		os.Exit(1)
	}
	defer func() { _ = f.Close() }()

	if err := goapiuse.EncodePublic(f, art); err != nil {
		log.Error("encode", "err", err)
		os.Exit(1)
	}

	log.Info("wrote index",
		"out", out,
		"apis", len(art.Entries),
		"calls", acc.totalCalls,
		"pkgs_ok", typeCheckedOK,
		"pkgs_failed", typeCheckFails)
}

func loadDir(dir string) ([]*packages.Package, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedCompiledGoFiles |
			packages.NeedImports |
			packages.NeedDeps |
			packages.NeedTypes |
			packages.NeedSyntax |
			packages.NeedTypesInfo,
		Dir:   dir,
		Tests: false,
	}
	return packages.Load(cfg, "./...")
}

// ingestPackage walks every call expression in pkg's syntax trees and
// records (callee, shape) into acc. It tolerates missing types info
// gracefully: calls with unresolved callees are skipped, not counted.
func ingestPackage(pkg *packages.Package, acc *accumulator) {
	if pkg == nil || pkg.TypesInfo == nil {
		return
	}
	fset := pkg.Fset
	for _, file := range pkg.Syntax {
		parents := newParentStack()
		ast.Inspect(file, func(n ast.Node) bool {
			if n == nil {
				parents.pop()
				return true
			}
			defer parents.push(n)

			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			callee := goapiuse.ResolveCallee(call, pkg.TypesInfo)
			if callee == "" {
				return true
			}
			parent := parents.parentOf()
			shape := goapiuse.ExtractShape(goapiuse.ShapeContext{
				Call:   call,
				Parent: parent,
				Info:   pkg.TypesInfo,
			})
			pos := fset.Position(call.Pos())
			// Skip synthetic callers (e.g. compiler builtins show up
			// with no file name).
			if pos.Filename == "" {
				return true
			}
			acc.add(callee, shape, fmt.Sprintf("%s:%d", short(pos.Filename), pos.Line))
			return true
		})
	}
}

// short trims a filename down to its last two path segments so example
// strings stay compact when the corpus lives in a deeply nested tempdir.
func short(path string) string {
	dir, file := filepath.Split(path)
	dir = filepath.Clean(dir)
	if dir == "." || dir == "/" || dir == "" {
		return file
	}
	return filepath.Base(dir) + "/" + file
}

// parentStack tracks the ancestor chain during ast.Inspect. ast.Inspect
// calls the visit func with nil on post-order exit, which we use as the
// pop signal.
type parentStack struct {
	stack []ast.Node
}

func newParentStack() *parentStack { return &parentStack{} }

func (p *parentStack) push(n ast.Node) { p.stack = append(p.stack, n) }

func (p *parentStack) pop() {
	if len(p.stack) == 0 {
		return
	}
	p.stack = p.stack[:len(p.stack)-1]
}

// parentOf returns the node just above the current one (the call itself is
// already on the stack when we query).
func (p *parentStack) parentOf() ast.Node {
	// stack layout at query time: [..., grandparent, parent].
	// We want parent.
	if len(p.stack) < 1 {
		return nil
	}
	return p.stack[len(p.stack)-1]
}

// accumulator collects (callee, shape) counts and example locations.
type accumulator struct {
	maxExamples int
	totalCalls  int
	byCallee    map[string]*calleeStats
}

type calleeStats struct {
	total  int
	shapes map[string]*shapeStats
}

type shapeStats struct {
	count    int
	examples []string
}

func newAccumulator(maxExamples int) *accumulator {
	if maxExamples <= 0 {
		maxExamples = 5
	}
	return &accumulator{
		maxExamples: maxExamples,
		byCallee:    make(map[string]*calleeStats),
	}
}

func (a *accumulator) add(callee, shape, example string) {
	a.totalCalls++
	cs := a.byCallee[callee]
	if cs == nil {
		cs = &calleeStats{shapes: make(map[string]*shapeStats)}
		a.byCallee[callee] = cs
	}
	cs.total++
	ss := cs.shapes[shape]
	if ss == nil {
		ss = &shapeStats{}
		cs.shapes[shape] = ss
	}
	ss.count++
	if len(ss.examples) < a.maxExamples {
		ss.examples = append(ss.examples, example)
	}
}

func (a *accumulator) build(meta goapiuse.Meta) goapiuse.ArtifactV1 {
	entries := make(map[string][]goapiuse.Usage, len(a.byCallee))
	for callee, cs := range a.byCallee {
		usages := make([]goapiuse.Usage, 0, len(cs.shapes))
		for pattern, ss := range cs.shapes {
			freq := 0.0
			if cs.total > 0 {
				freq = float64(ss.count) / float64(cs.total)
			}
			usages = append(usages, goapiuse.Usage{
				Pattern:      pattern,
				Frequency:    freq,
				ExampleRepos: append([]string(nil), ss.examples...),
			})
		}
		sort.SliceStable(usages, func(i, j int) bool {
			if usages[i].Frequency != usages[j].Frequency {
				return usages[i].Frequency > usages[j].Frequency
			}
			return usages[i].Pattern < usages[j].Pattern
		})
		entries[callee] = usages
	}
	meta.CallCount = a.totalCalls
	return goapiuse.ArtifactV1{Meta: meta, Entries: entries}
}

// unused vars guard: keep reference to types so go vet is happy in case the
// ingest stops using one of them during refactoring.
var _ = types.Universe
