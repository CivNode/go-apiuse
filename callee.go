package goapiuse

import (
	"go/ast"
	"go/types"
)

// ResolveCallee returns the fully-qualified name of the callee in call, or
// the empty string if the callee cannot be resolved (e.g. an unresolved
// function literal or a dynamic dispatch through an interface with no
// type information available).
//
// The returned form is:
//
//	pkg/path.FuncName           for package-level functions and constructors
//	pkg/path.TypeName.Method    for methods on named types (pointer or not)
//	(builtin).FuncName          for Go built-ins like len, cap, make
//
// Only the latter two use a period separator between receiver and method
// to stay compatible with the string keys the ingest tool emits.
func ResolveCallee(call *ast.CallExpr, info *types.Info) string {
	if info == nil || call == nil {
		return ""
	}
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		if obj, ok := info.Uses[fn]; ok {
			return qualifiedObject(obj)
		}
		if obj, ok := info.Defs[fn]; ok {
			return qualifiedObject(obj)
		}
	case *ast.SelectorExpr:
		if sel, ok := info.Selections[fn]; ok {
			return qualifiedSelection(sel)
		}
		// A selector may also be a package-qualified function (e.g.
		// context.WithTimeout). Those live in Uses keyed on fn.Sel.
		if obj, ok := info.Uses[fn.Sel]; ok {
			return qualifiedObject(obj)
		}
	}
	return ""
}

func qualifiedObject(obj types.Object) string {
	if obj == nil {
		return ""
	}
	if obj.Pkg() == nil {
		// Predeclared identifier (len, cap, append, make, ...).
		return "(builtin)." + obj.Name()
	}
	return obj.Pkg().Path() + "." + obj.Name()
}

func qualifiedSelection(sel *types.Selection) string {
	if sel == nil {
		return ""
	}
	obj := sel.Obj()
	if obj == nil || obj.Pkg() == nil {
		return ""
	}
	recv := sel.Recv()
	if recv == nil {
		return obj.Pkg().Path() + "." + obj.Name()
	}
	name := typeName(recv)
	if name == "" {
		return obj.Pkg().Path() + "." + obj.Name()
	}
	return obj.Pkg().Path() + "." + name + "." + obj.Name()
}

// typeName returns the bare type name of a (possibly pointer) named type.
// Returns "" for anonymous types.
func typeName(t types.Type) string {
	for {
		switch v := t.(type) {
		case *types.Pointer:
			t = v.Elem()
		case *types.Named:
			if v.Obj() == nil {
				return ""
			}
			return v.Obj().Name()
		default:
			return ""
		}
	}
}
