package hostpair

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// stdHostDeclarations counts, across every stdlib module, how many host
// declarations NAME a Go symbol and how many do not.
//
// The silent count is how many stdlib host declarations have no pairing in
// source, so this derivation cannot supply one for them.
type stdHostDeclarations struct {
	// Bound declarations carry a `go alias.Symbol` selector or an inline `go
	// { }` body, so the pairing is IN the source and hostpair derives it.
	Bound []string
	// Silent declarations are `host fn` / `host type` with no Go selector at
	// all. Their pairing exists nowhere in `.nomi` source: for these,
	// internal/stdlibbindings' hand-written row is not a third copy of a derivable
	// fact, it is the ONLY place the fact exists.
	Silent []string
	// SilentFuncs is the `host fn` half of Silent. Split out because a
	// count of functions and a total that folds in `host type` declarations
	// are not comparable.
	SilentFuncs []string
	// BoundFuncs is the `host fn` half of Bound, for the same reason.
	BoundFuncs []string
}

func measureStdHostDeclarations(t *testing.T) stdHostDeclarations {
	t.Helper()
	var out stdHostDeclarations
	for _, logicalPath := range stdModulePaths(t) {
		src, ok := std.ReadFile(strings.TrimPrefix(logicalPath, "std/"))
		if !ok {
			continue
		}
		nodes, err := parser.Parse(lexer.Lex(string(src)))
		if err != nil {
			nodes, _ = parser.ParseWithRecovery(lexer.Lex(string(src)))
		}
		module := strings.TrimPrefix(logicalPath, "std/")
		bound := map[string]bool{}
		for _, p := range DeriveNodes(logicalPath+".nomi", module, nodes) {
			bound[p.BuilderKey()] = true
			out.Bound = append(out.Bound, p.BuilderKey())
			if p.Kind == KindFunc {
				out.BoundFuncs = append(out.BoundFuncs, p.BuilderKey())
			}
		}
		for _, decl := range hostDecls(module, "", "", nodes) {
			if bound[decl.key()] {
				continue
			}
			out.Silent = append(out.Silent, decl.key())
			if decl.kind == KindFunc {
				out.SilentFuncs = append(out.SilentFuncs, decl.key())
			}
		}
	}
	sort.Strings(out.Bound)
	sort.Strings(out.BoundFuncs)
	sort.Strings(out.Silent)
	sort.Strings(out.SilentFuncs)
	return out
}

// hostDecl is one enumerated host declaration. The whole Pairing is carried
// rather than only a pre-joined key, because which JOIN a consumer needs
// depends on the consumer: this file's scope measurement wants BuilderKey and
// registered_test.go wants the Keys() candidate list.
type hostDecl struct {
	kind Kind
	p    Pairing
}

func (d hostDecl) key() string { return d.p.BuilderKey() }

// hostDecls enumerates EVERY host declaration in a file — selector-bound or
// not — under irbuild.stdKey's spelling, so the two populations are counted
// in one vocabulary.
//
// It also descends an `interface { host fn ... }` body, because a default
// declared there is a host declaration the binding table has to answer for:
// `Struct.update` is exactly that shape, and irbuild's own stdlib index missed
// it for the same reason a walk that skips InterfaceDef would.
func hostDecls(module, receiver, iface string, nodes []ast.Node) []hostDecl {
	var out []hostDecl
	for _, n := range nodes {
		switch v := n.(type) {
		case *ast.ImplBlock:
			recv := receiverBaseName(v.Receiver)
			if recv == "" {
				continue
			}
			out = append(out, hostDecls(module, recv, interfaceInstantiation(v.Interface), v.Items)...)
		case *ast.InterfaceDef:
			// A host-backed interface default is an InterfaceMethod with
			// Extern set, not an *ast.ExternFunc, so it is invisible to a walk
			// that only type-switches on nodes. `Struct.update` is that shape,
			// and irbuild's own stdlib index missed the whole family for
			// exactly this reason.
			for _, m := range v.Methods {
				if m.Extern {
					out = append(out, hostDecl{KindFunc, Pairing{Kind: KindFunc, Module: module, Receiver: v.Name, Name: m.Name}})
				}
			}
		case *ast.ExternType:
			out = append(out, hostDecl{KindType, Pairing{Kind: KindType, Module: module, Receiver: receiver, Name: v.Name}})
			if v.HasBody {
				out = append(out, hostDecls(module, v.Name, "", v.Items)...)
			}
		case *ast.ExternFunc:
			out = append(out, hostDecl{KindFunc, Pairing{Kind: KindFunc, Module: module, Receiver: receiver, Interface: iface, Name: v.Name}})
		}
	}
	return out
}

// stdModulePaths lists every stdlib module's logical path by walking the
// checkout's std tree.
//
// nomi/std exports ReadFile, MakeLoader and Load, and no lister for the whole
// stdlib; std.Load would analyze all of it to get one. It used to export
// FirstPartyModulePaths, which read the four adapter DIRECTORIES — those are
// gone (std/ is flat now) and so is that function, along with ffirun's
// firstPartyModulePaths fallback. Walking the source tree is what is left.
func stdModulePaths(t *testing.T) []string {
	t.Helper()
	stdRoot := filepath.Join(moduleRoot(t), "std")
	var out []string
	err := filepath.WalkDir(stdRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != stdRoot && strings.HasPrefix(d.Name(), "_") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".nomi") {
			return nil
		}
		rel, relErr := filepath.Rel(stdRoot, path)
		if relErr != nil {
			return relErr
		}
		rel = strings.TrimSuffix(filepath.ToSlash(rel), ".nomi")
		// The stdlib is one Nomi module and every module in it is a flat
		// `std/<name>.nomi`, adapters included — a directory under std/
		// holds Go support only. So a nested .nomi is not a module; the
		// walk already skips `_`-prefixed trees, and nothing else is
		// nested today.
		if strings.Contains(rel, "/") {
			return nil
		}
		out = append(out, "std/"+rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", stdRoot, err)
	}
	if len(out) == 0 {
		t.Fatalf("no stdlib modules found under %s; the measurement would be vacuous", stdRoot)
	}
	sort.Strings(out)
	return out
}

// TestNoStdlibHostDeclarationNamesAGoSymbol is this file's measurement
// INVERTED, and the inversion is the finding.
//
// It used to read "mostly silent": the co-located adapters — std/calendar,
// std/random, std/regex — named their Go symbols with `go
// alias.Symbol` selectors, so hostpair derived their pairings from source,
// while the rest of std declared bare `host fn` whose pairing exists only in
// a hand-written table. Two mechanisms for one job, split by which module you
// were in.
//
// There is one now. Every std host declaration is silent, its Go lives in a
// sibling package, and its pairing is a row in internal/stdlibbindings. That is a
// property worth holding: a `gopkg` handle reintroduced into a std facade
// would put those modules back on the FFI wrapper path, which is a Go
// toolchain requirement for `nomi run` — see
// internal/ffirun/hostkeyword_toolchain_test.go for what that cost.
//
// WHAT IT WOULD SHOW IF THE WALK WERE BROKEN: zero on both counts. So the
// silent population is asserted to be large, not merely non-empty, and the
// modules walked are asserted to include the three that changed.
func TestNoStdlibHostDeclarationNamesAGoSymbol(t *testing.T) {
	m := measureStdHostDeclarations(t)
	if len(m.Bound) != 0 {
		t.Errorf("%d stdlib host declaration(s) name a Go symbol in source: %s\n"+
			"A `gopkg` handle in a std facade makes the module a discoverable co-located "+
			"adapter again, and every program importing it then needs `go` on PATH.",
			len(m.Bound), strings.Join(m.Bound, " "))
	}
	if len(m.SilentFuncs) < 200 {
		t.Fatalf("only %d silent host FUNCTIONS found across std; the walk is not reaching the "+
			"modules and the zero above would be vacuous", len(m.SilentFuncs))
	}
	converted := map[string]bool{"calendar.": false, "random.": false, "regex.": false}
	for _, key := range m.Silent {
		for prefix := range converted {
			if strings.HasPrefix(key, prefix) {
				converted[prefix] = true
			}
		}
	}
	for prefix, seen := range converted {
		if !seen {
			t.Errorf("no silent host declaration from %s; the three converted modules are the "+
				"whole reason this assertion is a zero, so one missing makes it weaker than it reads",
				strings.TrimSuffix(prefix, "."))
		}
	}
	t.Logf("stdlib host declarations: %d name a Go symbol in source, %d name none", len(m.Bound), len(m.Silent))
	t.Logf("of those, host FUNCTIONS only: %d derivable, %d silent", len(m.BoundFuncs), len(m.SilentFuncs))
}
