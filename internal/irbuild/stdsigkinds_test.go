package irbuild

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// The classes of std signature position whose recorded type has no shared
// kind (projectShared answers kindInvalid) and names no type parameter, and
// the most positions each may hold. Each maximum only goes down: a position
// that starts to project lowers the number, and a refusal in no class fails.
var maxSigPositionRefusals = map[string]int{
	// A position of a function in a module declared with each program
	// (programStdModules). Its types are the program's, and a program's gen
	// lowers the function with its own kinds, so the shared row is never
	// lowered.
	"program-std module": 2077,
}

// TestStdSignaturePositionsProject holds every parameter and written result
// of every std function the index files, and every `once`, to a type the
// checker recorded, and counts what the projection with no gen
// (projectShared), which is the position's kind, makes of it: a shared kind,
// a position that names a type parameter, or a refusal, which must fall in a
// class of maxSigPositionRefusals within its maximum. -v lists the refusals.
func TestStdSignaturePositionsProject(t *testing.T) {
	lib := std.All()
	modules := make([]string, 0, len(lib.Nodes))
	for name := range lib.Nodes {
		modules = append(modules, name)
	}
	sort.Strings(modules)
	answered, generic := 0, 0
	byClass := map[string][]string{}
	for _, module := range modules {
		fa := lib.Files[module]
		check := func(key, where, written string, rec analysis.Type) {
			switch {
			case rec == nil:
				t.Errorf("%s %s `%s`: the checker recorded no type", key, where, written)
			case projectShared(rec) != kindInvalid:
				answered++
			case analysis.ContainsTypeParam(rec):
				generic++
			default:
				class := sigRefusalClass(module, rec)
				byClass[class] = append(byClass[class], fmt.Sprintf("%s %s `%s` (%s)", key, where, written, rec))
			}
		}
		for _, c := range collectStdCandidates(module, lib.Nodes[module], fa) {
			params, ret := stdSigOf(c.f.node)
			for i, p := range params {
				written := "(unannotated)"
				if p.TypeAnnotation != nil {
					written = typeText(p.TypeAnnotation)
				}
				check(c.f.key, fmt.Sprintf("param %d", i), written, stdParamType(fa, p))
			}
			if ret != nil {
				check(c.f.key, "result", typeText(ret), stdTypeExprType(fa, ret))
			}
		}
		for _, o := range collectStdOnces(module, lib.Nodes[module], fa) {
			if o.decl.Value == nil {
				continue
			}
			if o.decl.TypeAnnotation != nil {
				check(o.key, "once", typeText(o.decl.TypeAnnotation), stdTypeExprType(fa, o.decl.TypeAnnotation))
			} else {
				check(o.key, "once", "(unannotated)", onceSymbolType(fa, o.decl))
			}
		}
	}
	classes := make([]string, 0, len(byClass))
	for class := range byClass {
		classes = append(classes, class)
	}
	sort.Strings(classes)
	refused := 0
	for _, class := range classes {
		sites := byClass[class]
		refused += len(sites)
		t.Logf("%s: %d\n  %s", class, len(sites), strings.Join(sites, "\n  "))
		max, listed := maxSigPositionRefusals[class]
		switch {
		case !listed:
			t.Errorf("%d positions refused in no listed class (%s); see the log", len(sites), class)
		case len(sites) > max:
			t.Errorf("%s: %d positions, the maximum is %d", class, len(sites), max)
		case len(sites) < max:
			t.Errorf("%s: %d positions, below the maximum %d; lower it", class, len(sites), max)
		}
	}
	for class, max := range maxSigPositionRefusals {
		if _, seen := byClass[class]; !seen && max > 0 {
			t.Errorf("%s: no positions, the maximum is %d; delete the class", class, max)
		}
	}
	t.Logf("%d positions with a shared kind, %d naming a type parameter, %d refused", answered, generic, refused)
	if answered == 0 {
		t.Fatal("no position projected, so the classes above say nothing")
	}
}

// sigRefusalClass names why a std position in module has no shared kind for
// rec, or says that it is no listed class.
func sigRefusalClass(module string, rec analysis.Type) string {
	if isProgramStd(module) {
		return "program-std module"
	}
	return sharedRefusalCause(rec)
}

// sharedRefusalCause names why rec, a type with no shared kind that names
// no type parameter, has none: the first part of it with none. A type a
// std module declares that the registry built and did not admit names its
// reason (stdTypeWhy).
func sharedRefusalCause(rec analysis.Type) string {
	parts := func(ts ...analysis.Type) string {
		for _, p := range ts {
			if p != nil && projectShared(p) == kindInvalid && !analysis.ContainsTypeParam(p) {
				return sharedRefusalCause(p)
			}
		}
		return ""
	}
	nominal := func(origin, name string, args []analysis.Type) string {
		if cause := parts(args...); cause != "" {
			return cause
		}
		if !strings.HasPrefix(origin, "std/") {
			return ""
		}
		if len(args) > 0 {
			return "generic std type instance"
		}
		if why := stdTypeWhy(stdTypeKey{origin: origin, name: name}); why != "" {
			return "std type with no shared def: " + why
		}
		return "std type with no shared def"
	}
	cause := ""
	switch ty := analysis.ResolveClosed(rec).(type) {
	case *analysis.InterfaceType:
		if ty.Name == "Iter" && ty.Origin == "std/iter" {
			return "iter"
		}
		if cause := parts(ty.TypeArgs...); cause != "" {
			return cause
		}
		if len(ty.TypeArgs) > 0 {
			return "generic std interface instance"
		}
		// An interface the registry did not admit names its reason.
		if why := stdTypeWhy(stdTypeKey{origin: ty.Origin, name: ty.Name}); why != "" {
			return "interface value: " + why
		}
		return "interface value"
	case *analysis.ConjunctionType:
		return "interface value"
	case *analysis.StructType:
		cause = nominal(ty.Origin, ty.Name, ty.TypeArgs)
	case *analysis.EnumType:
		cause = nominal(ty.Origin, ty.Name, ty.TypeArgs)
	case *analysis.DistinctType:
		cause = nominal(ty.Origin, ty.Name, ty.TypeArgs)
	case *analysis.ListType:
		cause = parts(ty.Elem)
	case *analysis.MapType:
		cause = parts(ty.Key, ty.Val)
	case *analysis.TupleType:
		cause = parts(ty.Elems...)
	case *analysis.FuncType:
		cause = parts(append(append([]analysis.Type(nil), ty.Params...), ty.Return)...)
	case *analysis.AnonStructType:
		for _, f := range ty.Fields {
			if cause = parts(f.Type); cause != "" {
				break
			}
		}
	}
	if cause != "" {
		return cause
	}
	return fmt.Sprintf("unlisted: %T %s", rec, rec)
}

// stdSigOf is a std function declaration's parameters and written result.
func stdSigOf(n ast.Node) ([]ast.Param, ast.TypeExpr) {
	switch d := n.(type) {
	case *ast.FuncDef:
		return d.Params, d.ReturnTypeExpr
	case *ast.ExternFunc:
		return d.Params, d.ReturnTypeExpr
	case *ast.InterfaceMethod:
		return d.Params, d.ReturnTypeExpr
	}
	return nil, nil
}
