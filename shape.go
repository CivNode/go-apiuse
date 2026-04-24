package goapiuse

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"strconv"
	"strings"
)

// ShapeContext is what the ingest tool feeds into ExtractShape. It carries
// everything the shape extractor needs to decide on a canonical string
// without holding a reference to a types.Package.
type ShapeContext struct {
	Call   *ast.CallExpr
	Parent ast.Node // immediate parent of Call in the AST walk

	// Info is the type-checked info produced by go/packages. Must include
	// Types + Uses + Defs. A nil Info means "types not available"; the
	// shape will fall back to syntactic classification.
	Info *types.Info
}

// ExtractShape returns a canonical, compact string describing the call.
// The goal is aggressive clustering: a corpus of 10 real-world calls to a
// given API should collapse to 3 or fewer shapes.
//
// The shape encodes three facts:
//   - the call's syntactic context (assignment arity, return statement,
//     argument position, bare statement, ...);
//   - the argument count;
//   - for each argument, a compact category (identifier, literal, call,
//     selector, duration-like, ...).
//
// Identifier names are deliberately discarded. Package-qualified selectors
// keep the selector path because it is often semantically load-bearing
// (e.g. time.Second vs http.StatusOK).
func ExtractShape(sc ShapeContext) string {
	if sc.Call == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(contextToken(sc))
	b.WriteString(" | args=")
	b.WriteString(strconv.Itoa(len(sc.Call.Args)))
	if len(sc.Call.Args) > 0 {
		b.WriteString(" | ")
		for i, a := range sc.Call.Args {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(argCategory(a, sc.Info))
		}
	}
	if sc.Call.Ellipsis != token.NoPos {
		b.WriteString(" | variadic")
	}
	return b.String()
}

// contextToken classifies how the call's result is consumed.
func contextToken(sc ShapeContext) string {
	switch p := sc.Parent.(type) {
	case *ast.AssignStmt:
		// Distinguish short-var decl from plain assign, and record arity.
		n := len(p.Lhs)
		kind := "assign"
		if p.Tok == token.DEFINE {
			kind = "short-decl"
		}
		return fmt.Sprintf("%s[%d]", kind, n)
	case *ast.ReturnStmt:
		return "return"
	case *ast.ExprStmt:
		return "stmt"
	case *ast.GoStmt:
		return "go"
	case *ast.DeferStmt:
		return "defer"
	case *ast.CallExpr:
		// The call is itself an argument to another call.
		return "arg-of-call"
	case *ast.SelectorExpr:
		// Rare: chained call like x.Foo().Bar().
		return "receiver-of-selector"
	case *ast.BinaryExpr, *ast.UnaryExpr:
		return "operand"
	case *ast.IfStmt, *ast.SwitchStmt, *ast.ForStmt:
		return "control-head"
	case *ast.ValueSpec:
		return "value-spec"
	case *ast.CompositeLit:
		return "composite-elt"
	case nil:
		return "unknown"
	default:
		return "other"
	}
}

// argCategory classifies a single argument expression. Keep the vocabulary
// small. Every new token you add fractures the clustering.
func argCategory(e ast.Expr, info *types.Info) string {
	// Type-informed categories first (strongest signal).
	if info != nil {
		if c := typedCategory(e, info); c != "" {
			return c
		}
	}
	return syntacticCategory(e)
}

func typedCategory(e ast.Expr, info *types.Info) string {
	tv, ok := info.Types[e]
	if !ok || tv.Type == nil {
		return ""
	}
	// Collapse every Duration shape (constant, variable, or expression)
	// into a single "duration" token. The distinction between
	// `5*time.Second` and `d` is not a useful cluster signal for the
	// kind of pedagogical hint Training wants to surface.
	if isDurationType(tv.Type) {
		return "duration"
	}
	if isContextType(tv.Type) {
		return "context"
	}
	if isErrorType(tv.Type) {
		return "error"
	}
	// Constant kinds further normalise: a bare constant, regardless of
	// whether it originated as a literal or a named const, looks the
	// same from the caller's perspective.
	if tv.Value != nil {
		switch tv.Value.Kind() {
		case constant.Int:
			return "int-const"
		case constant.Float:
			return "float-const"
		case constant.String:
			return "string-const"
		case constant.Bool:
			return "bool-const"
		}
	}
	switch tv.Type.Underlying().(type) {
	case *types.Chan:
		return "chan"
	case *types.Map:
		return "map"
	case *types.Slice:
		return "slice"
	case *types.Signature:
		return "func"
	case *types.Interface:
		return "interface"
	}
	return ""
}

// syntacticCategory is the fallback used when type info is unavailable.
func syntacticCategory(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.BasicLit:
		switch v.Kind {
		case token.INT:
			return "int-literal"
		case token.FLOAT:
			return "float-literal"
		case token.STRING:
			return "string-literal"
		case token.CHAR:
			return "char-literal"
		case token.IMAG:
			return "imag-literal"
		}
		return "literal"
	case *ast.Ident:
		switch v.Name {
		case "nil":
			return "nil"
		case "true", "false":
			return "bool-literal"
		}
		return "ident"
	case *ast.SelectorExpr:
		// Preserve the selector path for well-known constants like
		// time.Second, http.StatusOK. We keep only the tail selector +
		// an optional package-ish prefix to stay compact.
		if pkg, ok := v.X.(*ast.Ident); ok {
			return "sel:" + pkg.Name + "." + v.Sel.Name
		}
		return "sel:" + v.Sel.Name
	case *ast.CallExpr:
		return "call"
	case *ast.FuncLit:
		return "func-lit"
	case *ast.CompositeLit:
		return "composite-lit"
	case *ast.UnaryExpr:
		if v.Op == token.AND {
			return "addr-of"
		}
		return "unary"
	case *ast.BinaryExpr:
		// A 5*time.Second shape is common and worth surfacing.
		if v.Op == token.MUL {
			return "mul-expr"
		}
		return "binary"
	case *ast.StarExpr:
		return "deref"
	case *ast.IndexExpr:
		return "index"
	}
	return "other"
}

func isContextType(t types.Type) bool {
	named, ok := t.(*types.Named)
	if !ok {
		// context.Context is an interface; when used as a parameter the
		// type is the named interface itself.
		iface, isIface := t.Underlying().(*types.Interface)
		if !isIface {
			return false
		}
		_ = iface
		return false
	}
	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	return obj.Pkg().Path() == "context" && obj.Name() == "Context"
}

func isDurationType(t types.Type) bool {
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	return obj.Pkg().Path() == "time" && obj.Name() == "Duration"
}

func isErrorType(t types.Type) bool {
	// error is the predeclared interface type.
	iface, ok := t.Underlying().(*types.Interface)
	if !ok {
		return false
	}
	return iface.NumMethods() == 1 && iface.Method(0).Name() == "Error"
}
