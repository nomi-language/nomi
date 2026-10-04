package irbuild

import (
	"sort"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// TestStdlibSpellingFollowsWhereTheImplementationLives guards the rule
// docs/spec.md §24 states: every stdlib declaration whose
// implementation is Go is spelled `host fn` / `host type`, whatever Go package
// holds that implementation, and `fn f(x: T): U go pkg.Sym` is what a user
// writes to bind their own code.
//
// A `go` selector in a std module is dead text. `gopkg
// "github.com/nomi-language/nomi/rt" as go_rt` plus `fn length(data: Bytes): Int
// go go_rt.BytesLength` in std/bytes.nomi builds, runs and prints the right
// answer, and so does the same line pointed at `go_rt.NoSuchSymbolAtAll`:
// nothing generates a binding from the selector, and the existing
// internal/stdlibbindings row under the same key answers the call. A `gopkg`
// handle also makes the module a discoverable co-located adapter, which puts
// every program importing it behind a Go toolchain. So no std declaration may
// name a Go symbol, either as a selector or as a `go { }` body.
// internal/hostpair's TestNoStdlibHostDeclarationNamesAGoSymbol asserts the
// same rule over the host-declaration pairs.
func TestStdlibSpellingFollowsWhereTheImplementationLives(t *testing.T) {
	lib := std.Load()
	type spelling struct {
		host, foreign int
		rtSelectors   []string
	}
	per := map[string]*spelling{}
	get := func(f string) *spelling {
		if per[f] == nil {
			per[f] = &spelling{}
		}
		return per[f]
	}
	var files []string
	for file := range lib.Nodes {
		files = append(files, file)
	}
	sort.Strings(files)

	for _, file := range files {
		// gopkg handles this file declares, so a selector's alias resolves to
		// the Go import path it actually names.
		aliases := map[string]string{}
		for _, n := range lib.Nodes[file] {
			if ep, ok := n.(*ast.ExternPackage); ok && ep.Alias != "" {
				aliases[ep.Alias] = ep.ImportPath
			}
		}
		walkStdExternDecls(lib.Nodes[file], func(name, alias, foreign, goBody string) {
			ef := struct{ Name, ForeignAlias, ForeignName, GoBody string }{name, alias, foreign, goBody}
			s := get(file)
			if ef.ForeignName == "" && ef.GoBody == "" {
				s.host++
				return
			}
			s.foreign++
			if aliases[ef.ForeignAlias] == rtModulePath {
				s.rtSelectors = append(s.rtSelectors,
					ef.Name+" -> "+ef.ForeignAlias+"."+ef.ForeignName)
			}
		})
	}

	hostOnly, foreignOnly := 0, 0
	for _, file := range files {
		s := per[file]
		if s == nil || (s.host == 0 && s.foreign == 0) {
			continue
		}
		if s.foreign > 0 {
			foreignOnly++
			t.Errorf("%s carries %d `go`-bound declaration(s)%v. No std declaration may "+
				"name a Go symbol: the selector generates no binding, so it is dead "+
				"text, and a `gopkg` handle also makes the module a discoverable "+
				"co-located adapter, which puts every program importing it behind a "+
				"Go toolchain. Write `host fn` and add a row to internal/stdlibbindings.",
				file, s.foreign, s.rtSelectors)
			continue
		}
		hostOnly++
	}
	t.Logf("stdlib modules declaring externs: %d spelled `host fn`, %d spelled `go pkg.Sym`",
		hostOnly, foreignOnly)

	// A walk that saw nothing would report the same zero, so the `host fn`
	// population must be large. About 29 modules declare externs; the floor
	// sits well under that so an ordinary edit does not trip it.
	if hostOnly < 20 {
		t.Fatalf("only %d stdlib module(s) declare a `host fn`; the walk is not reaching "+
			"the modules and the zero above is vacuous", hostOnly)
	}
}

// walkStdExternDecls visits every declaration in a stdlib file whose body comes
// from outside Nomi, at the top level and inside impl and interface blocks, and
// reports its name plus the `go` selector it carries (empty for `host fn`).
//
// The interface arm is a different node: a host-backed default is an
// `*ast.InterfaceMethod` with `Extern` set (`Struct.update` is the one in std),
// and it carries no Foreign fields at all, so it cannot be `go`-spelled. A walk
// that missed it would report std/structs as declaring no externs.
func walkStdExternDecls(nodes []ast.Node, visit func(name, alias, foreign, goBody string)) {
	for _, n := range nodes {
		switch v := n.(type) {
		case *ast.ExternFunc:
			visit(v.Name, v.ForeignAlias, v.ForeignName, v.GoBody)
		case *ast.ImplBlock:
			walkStdExternDecls(v.Items, visit)
		case *ast.InterfaceDef:
			for i := range v.Methods {
				if v.Methods[i].Extern {
					visit(v.Methods[i].Name, "", "", "")
				}
			}
		}
	}
}
