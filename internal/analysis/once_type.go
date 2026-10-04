package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// OnceTable holds a project's module-level `once` bindings: those at the top
// of a file, in a type's namespace items, and in an impl block. One table is
// shared by every file of a project build, so a checker reading a `once`
// declared in another file can find that file.
//
// An unannotated `once` takes the type of its value, and only checking the
// value tells it. A read can be checked before the declaration is: later in
// the same file, in an attached test above it, or in another file the front
// end checks first. Such a read checks the declaration on demand
// (checker.ensureOnceType), so a read never sees a `once` without its type.
type OnceTable struct {
	sites map[*ast.OnceBinding]onceSite
	// checking is the bindings whose value is being checked, innermost last.
	// A read of one of them is a cycle.
	checking []*ast.OnceBinding
	// cycles holds, for each unannotated binding whose value reads its own
	// type, the names along the cycle, starting and ending with its own.
	cycles map[*ast.OnceBinding][]string
	// checked holds the bindings whose value some checker has checked, so
	// a read of one whose value has no type does not check it again.
	checked map[*ast.OnceBinding]bool
}

// onceSite is where a `once` is declared: its file, that file's nodes (a
// checker for the file needs both), and the receiver base name of the impl
// block holding it, or "" outside one.
type onceSite struct {
	fa    *FileAnalysis
	nodes []ast.Node
	owner string
}

// NewOnceTable answers an empty table.
func NewOnceTable() *OnceTable {
	return &OnceTable{
		sites:   map[*ast.OnceBinding]onceSite{},
		cycles:  map[*ast.OnceBinding][]string{},
		checked: map[*ast.OnceBinding]bool{},
	}
}

// Index records every module-level `once` of one file.
func (t *OnceTable) Index(fa *FileAnalysis, nodes []ast.Node) {
	add := func(items []ast.Node, owner string) {
		for _, item := range items {
			if n, ok := item.(*ast.OnceBinding); ok {
				t.sites[n] = onceSite{fa: fa, nodes: nodes, owner: owner}
			}
		}
	}
	add(nodes, "")
	for _, node := range nodes {
		switch n := node.(type) {
		case *ast.StructDef:
			add(n.Items, "")
		case *ast.EnumDef:
			add(n.Items, "")
		case *ast.TypeDef:
			add(n.Items, "")
		case *ast.ExternType:
			add(n.Items, "")
		case *ast.ImplBlock:
			add(n.Items, TypeExprBaseName(n.Receiver))
		}
	}
}

// ensureOnceType gives a module-level unannotated `once` its type before a
// read uses it, checking its value with a checker for its own file if no
// checker has yet. Errors that check finds are dropped: the declaring file's
// own CheckTypes reports them at the declaration.
func (c *checker) ensureOnceType(sym *Symbol) {
	if sym == nil || sym.Kind != SymbolOnce || sym.Type != nil || c.fa == nil || c.fa.Onces == nil {
		return
	}
	n, ok := sym.Node.(*ast.OnceBinding)
	if !ok || n.TypeAnnotation != nil {
		return
	}
	t := c.fa.Onces
	site, ok := t.sites[n]
	if !ok {
		return
	}
	for i, open := range t.checking {
		if open != n {
			continue
		}
		names := make([]string, 0, len(t.checking)-i+1)
		for _, b := range t.checking[i:] {
			names = append(names, b.Name)
		}
		names = append(names, n.Name)
		for _, b := range t.checking[i:] {
			if b.TypeAnnotation == nil && t.cycles[b] == nil {
				t.cycles[b] = rotateCycle(names, b.Name)
			}
		}
		return
	}
	if t.checked[n] {
		return
	}
	sub := newChecker(site.fa, site.nodes)
	sub.selfTypeName = site.owner
	sub.checkOnce(n)
}

// rotateCycle answers the cycle names (first == last) restarted at name.
func rotateCycle(names []string, name string) []string {
	ring := names[:len(names)-1]
	for i, s := range ring {
		if s == name {
			out := append(append([]string{}, ring[i:]...), ring[:i]...)
			return append(out, name)
		}
	}
	return names
}

// onceCycleMessage is the error at an unannotated `once` whose value reads its
// own type.
func onceCycleMessage(name string, cycle []string) string {
	return fmt.Sprintf(
		"once '%s' has no type annotation and its value depends on its own type (%s) — annotate it (`%s: T = …`)",
		name, strings.Join(cycle, " → "), name)
}
