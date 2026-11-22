package hostpair

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// hostDecl is one enumerated host declaration. The whole Pairing is carried
// rather than only a pre-joined key, because registered_test.go reports a
// declaration by its BuilderKey and matches it by the Keys() candidate list.
type hostDecl struct {
	kind Kind
	p    Pairing
}

func (d hostDecl) key() string { return d.p.BuilderKey() }

// hostDecls enumerates EVERY host declaration in a file.
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
// stdlib; std.Load would analyze all of it to get one, so the test walks the
// source tree.
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
		// A module is a `std/<name>.nomi` or one nested under its parent,
		// `std/<parent>/<name>.nomi` (std/fs/host.nomi); the walk already
		// skips `_`-prefixed trees.
		if strings.Count(rel, "/") > 1 {
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
