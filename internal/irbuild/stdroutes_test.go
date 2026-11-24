package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/std"
)

// TestEveryStdRouteNamesAStdFunction: every std function the builder
// lowers by a route of its own (stdRoutes), and the `check` the checker
// types as a keyword form (analysis.StdTestingCheck), is one a std module
// declares, by the identity the checker gives that module's functions
// (analysis.FileAnalysis.StdFuncs), so a renamed or moved std function,
// or a key whose Impl names no impl of it, fails here instead of reaching
// the general call route.
func TestEveryStdRouteNamesAStdFunction(t *testing.T) {
	lib := std.All()
	declared := map[analysis.StdFunc]bool{}
	for _, fa := range lib.Files {
		for _, id := range fa.StdFuncs() {
			declared[id] = true
		}
	}
	if len(declared) == 0 {
		t.Fatal("read no std function definitions, so this test checks nothing")
	}
	keys := []analysis.StdFunc{analysis.StdTestingCheck}
	for id := range stdRoutes {
		keys = append(keys, id)
	}
	for _, id := range keys {
		if !declared[id] {
			t.Errorf("the builder routes %+v, which no std module declares", id)
		}
	}
}

// TestEveryStdQualRouteNamesAStdFunction: every key of stdQualRoutes names
// a type or interface its std module declares, and a function of it: for
// an interface, its own method or a function of its own impl block
// (`impl Iter<T>`); for a type, a function some std impl declares for it.
// An owner-wide key, with no method, names a declared owner. A renamed or moved type or method fails here instead of reaching the
// general call route.
func TestEveryStdQualRouteNamesAStdFunction(t *testing.T) {
	lib := std.All()
	owners := map[analysis.StdType]analysis.SymbolKind{}
	ifaceMethods := map[stdQualKey]bool{}
	typeFuncs := map[[2]string]bool{}
	ownFuncs := map[stdQualKey]bool{}
	for _, fa := range lib.Files {
		if fa == nil {
			continue
		}
		for _, sym := range fa.Definitions {
			if sym == nil {
				continue
			}
			switch sym.Kind {
			case analysis.SymbolInterface, analysis.SymbolStruct, analysis.SymbolEnum, analysis.SymbolType:
				owners[analysis.StdType{Module: fa.Origin, Name: sym.Name}] = sym.Kind
			case analysis.SymbolInterfaceMethod:
				ifaceMethods[stdQualKey{analysis.StdType{Module: fa.Origin, Name: sym.OwningInterface}, sym.Name}] = true
			case analysis.SymbolFunction:
				if sym.OwningType != "" {
					typeFuncs[[2]string{sym.OwningType, sym.Name}] = true
					ownFuncs[stdQualKey{analysis.StdType{Module: fa.Origin, Name: sym.OwningType}, sym.Name}] = true
				}
			}
		}
	}
	if len(owners) == 0 || len(ifaceMethods) == 0 || len(typeFuncs) == 0 {
		t.Fatal("read no std types, interface methods or impl functions, so this test checks nothing")
	}
	for key := range stdQualRoutes {
		kind, declared := owners[key.Owner]
		switch {
		case !declared:
			t.Errorf("the builder routes %+v, whose owner no std module declares", key)
		case key.Method == "":
			// An owner-wide route names the owner alone.
		case kind == analysis.SymbolInterface && !ifaceMethods[key] && !ownFuncs[key]:
			t.Errorf("the builder routes %+v, which is no method of that std interface", key)
		case kind != analysis.SymbolInterface && !typeFuncs[[2]string{key.Owner.Name, key.Method}]:
			t.Errorf("the builder routes %+v, which no std impl declares for that type", key)
		}
	}
}
