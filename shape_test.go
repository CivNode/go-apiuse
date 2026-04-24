package goapiuse

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

// parseAndCheck is a test helper that type-checks a single-file package so
// shape tests can exercise typedCategory with real types.Info values.
func parseAndCheck(t *testing.T, src string) (*token.FileSet, *ast.File, *types.Info) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info := &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
	}
	conf := types.Config{Importer: importer.Default()}
	if _, err := conf.Check("demo", fset, []*ast.File{file}, info); err != nil {
		t.Fatalf("type-check: %v", err)
	}
	return fset, file, info
}

// findCallAndParent walks the AST and returns the first CallExpr matching
// pred, along with its immediate parent node.
func findCallAndParent(f *ast.File, pred func(*ast.CallExpr) bool) (call *ast.CallExpr, parent ast.Node) {
	var stack []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			return true
		}
		if c, ok := n.(*ast.CallExpr); ok && pred(c) && call == nil {
			call = c
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
		}
		stack = append(stack, n)
		return true
	})
	return
}

func TestExtractShape_ContextWithTimeoutShortDecl(t *testing.T) {
	src := `package demo
import (
	"context"
	"time"
)
func F(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	_ = ctx; _ = cancel
}`
	_, file, info := parseAndCheck(t, src)
	call, parent := findCallAndParent(file, isWithTimeout)
	if call == nil {
		t.Fatal("no call found")
	}
	shape := ExtractShape(ShapeContext{Call: call, Parent: parent, Info: info})
	wantSubs := []string{"short-decl[2]", "args=2", "context", "duration"}
	for _, w := range wantSubs {
		if !strings.Contains(shape, w) {
			t.Fatalf("shape %q missing %q", shape, w)
		}
	}
}

func TestExtractShape_ContextWithTimeoutAssign(t *testing.T) {
	src := `package demo
import (
	"context"
	"time"
)
func F(parent context.Context) {
	var ctx context.Context
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(parent, 2*time.Second)
	_ = ctx; _ = cancel
}`
	_, file, info := parseAndCheck(t, src)
	call, parent := findCallAndParent(file, isWithTimeout)
	shape := ExtractShape(ShapeContext{Call: call, Parent: parent, Info: info})
	if !strings.Contains(shape, "assign[2]") {
		t.Fatalf("expected assign[2], got %q", shape)
	}
	if !strings.Contains(shape, "duration") {
		t.Fatalf("expected duration, got %q", shape)
	}
}

func TestExtractShape_BareStmt(t *testing.T) {
	src := `package demo
import "fmt"
func F() { fmt.Println("hi") }`
	_, file, info := parseAndCheck(t, src)
	call, parent := findCallAndParent(file, func(c *ast.CallExpr) bool {
		sel, ok := c.Fun.(*ast.SelectorExpr)
		return ok && sel.Sel.Name == "Println"
	})
	shape := ExtractShape(ShapeContext{Call: call, Parent: parent, Info: info})
	if !strings.HasPrefix(shape, "stmt") {
		t.Fatalf("expected stmt prefix, got %q", shape)
	}
}

func TestExtractShape_Return(t *testing.T) {
	src := `package demo
import "errors"
func F() error { return errors.New("boom") }`
	_, file, info := parseAndCheck(t, src)
	call, parent := findCallAndParent(file, func(c *ast.CallExpr) bool {
		sel, ok := c.Fun.(*ast.SelectorExpr)
		return ok && sel.Sel.Name == "New"
	})
	shape := ExtractShape(ShapeContext{Call: call, Parent: parent, Info: info})
	if !strings.HasPrefix(shape, "return") {
		t.Fatalf("expected return prefix, got %q", shape)
	}
}

func TestExtractShape_Variadic(t *testing.T) {
	src := `package demo
import "fmt"
func F(xs []any) { fmt.Println(xs...) }`
	_, file, info := parseAndCheck(t, src)
	call, parent := findCallAndParent(file, func(c *ast.CallExpr) bool { return c.Ellipsis != token.NoPos })
	if call == nil {
		t.Fatal("variadic call not found")
	}
	shape := ExtractShape(ShapeContext{Call: call, Parent: parent, Info: info})
	if !strings.Contains(shape, "variadic") {
		t.Fatalf("expected variadic, got %q", shape)
	}
}

func TestExtractShape_WithoutTypes(t *testing.T) {
	// When Info is nil the extractor falls back to syntactic classification.
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "x.go", `package demo
func F(x []int) int { return len(x) }`, 0)
	if err != nil {
		t.Fatal(err)
	}
	call, parent := findCallAndParent(file, func(c *ast.CallExpr) bool {
		id, ok := c.Fun.(*ast.Ident)
		return ok && id.Name == "len"
	})
	shape := ExtractShape(ShapeContext{Call: call, Parent: parent, Info: nil})
	if !strings.Contains(shape, "ident") {
		t.Fatalf("expected syntactic ident category, got %q", shape)
	}
}

func TestExtractShape_NilCall(t *testing.T) {
	if got := ExtractShape(ShapeContext{}); got != "" {
		t.Fatalf("nil call: got %q", got)
	}
}

func TestArgCategory_Literals(t *testing.T) {
	cases := map[string]string{
		`package demo; var _ = foo("hi")`:     "string",
		`package demo; var _ = foo(42)`:       "int",
		`package demo; var _ = foo(3.14)`:     "float",
		`package demo; var _ = foo(nil)`:      "nil",
		`package demo; var _ = foo(true)`:     "bool-literal",
		`package demo; var _ = foo(&x)`:       "addr-of",
		`package demo; var _ = foo(x * y)`:    "mul-expr",
		`package demo; var _ = foo(x + y)`:    "binary",
		`package demo; var _ = foo(xs[0])`:    "index",
		`package demo; var _ = foo(*p)`:       "deref",
		`package demo; var _ = foo(func(){})`: "func-lit",
		`package demo; var _ = foo(T{})`:      "composite-lit",
	}
	for src, want := range cases {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "x.go", src, 0)
		if err != nil {
			t.Fatalf("parse %q: %v", src, err)
		}
		var call *ast.CallExpr
		ast.Inspect(file, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok && call == nil {
				call = c
			}
			return true
		})
		if call == nil || len(call.Args) == 0 {
			t.Fatalf("%q: no call arg", src)
		}
		got := argCategory(call.Args[0], nil)
		if !strings.Contains(got, want) {
			t.Fatalf("%q: got %q want contains %q", src, got, want)
		}
	}
}

func TestContextToken_AllBranches(t *testing.T) {
	cases := []struct {
		parent ast.Node
		want   string
	}{
		{&ast.AssignStmt{Tok: token.ASSIGN, Lhs: []ast.Expr{nil, nil}}, "assign[2]"},
		{&ast.AssignStmt{Tok: token.DEFINE, Lhs: []ast.Expr{nil}}, "short-decl[1]"},
		{&ast.ReturnStmt{}, "return"},
		{&ast.ExprStmt{}, "stmt"},
		{&ast.GoStmt{}, "go"},
		{&ast.DeferStmt{}, "defer"},
		{&ast.CallExpr{}, "arg-of-call"},
		{&ast.SelectorExpr{}, "receiver-of-selector"},
		{&ast.BinaryExpr{}, "operand"},
		{&ast.UnaryExpr{}, "operand"},
		{&ast.IfStmt{}, "control-head"},
		{&ast.SwitchStmt{}, "control-head"},
		{&ast.ForStmt{}, "control-head"},
		{&ast.ValueSpec{}, "value-spec"},
		{&ast.CompositeLit{}, "composite-elt"},
		{nil, "unknown"},
		{&ast.BadStmt{}, "other"},
	}
	for _, tc := range cases {
		sc := ShapeContext{Call: &ast.CallExpr{}, Parent: tc.parent}
		got := contextToken(sc)
		if got != tc.want {
			t.Fatalf("parent %T: got %q want %q", tc.parent, got, tc.want)
		}
	}
}

func TestResolveCallee_PackageFunction(t *testing.T) {
	src := `package demo
import "context"
func F() { _ = context.Background() }`
	_, file, info := parseAndCheck(t, src)
	call, _ := findCallAndParent(file, func(c *ast.CallExpr) bool {
		sel, ok := c.Fun.(*ast.SelectorExpr)
		return ok && sel.Sel.Name == "Background"
	})
	got := ResolveCallee(call, info)
	if got != "context.Background" {
		t.Fatalf("got %q want context.Background", got)
	}
}

func TestResolveCallee_Method(t *testing.T) {
	src := `package demo
import "sync"
func F() { var mu sync.Mutex; mu.Lock() }`
	_, file, info := parseAndCheck(t, src)
	call, _ := findCallAndParent(file, func(c *ast.CallExpr) bool {
		sel, ok := c.Fun.(*ast.SelectorExpr)
		return ok && sel.Sel.Name == "Lock"
	})
	got := ResolveCallee(call, info)
	if got != "sync.Mutex.Lock" {
		t.Fatalf("got %q want sync.Mutex.Lock", got)
	}
}

func TestResolveCallee_Builtin(t *testing.T) {
	src := `package demo
func F(x []int) int { return len(x) }`
	_, file, info := parseAndCheck(t, src)
	call, _ := findCallAndParent(file, func(c *ast.CallExpr) bool {
		id, ok := c.Fun.(*ast.Ident)
		return ok && id.Name == "len"
	})
	got := ResolveCallee(call, info)
	if got != "(builtin).len" {
		t.Fatalf("got %q want (builtin).len", got)
	}
}

func TestResolveCallee_LocalFunc(t *testing.T) {
	src := `package demo
func g() int { return 0 }
func F() int { return g() }`
	_, file, info := parseAndCheck(t, src)
	call, _ := findCallAndParent(file, func(c *ast.CallExpr) bool {
		id, ok := c.Fun.(*ast.Ident)
		return ok && id.Name == "g"
	})
	got := ResolveCallee(call, info)
	if got != "demo.g" {
		t.Fatalf("got %q want demo.g", got)
	}
}

func TestResolveCallee_NilSafe(t *testing.T) {
	if ResolveCallee(nil, nil) != "" {
		t.Fatal("nil call should return empty")
	}
	if ResolveCallee(&ast.CallExpr{}, nil) != "" {
		t.Fatal("nil info should return empty")
	}
}

func isWithTimeout(c *ast.CallExpr) bool {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return sel.Sel.Name == "WithTimeout"
}
