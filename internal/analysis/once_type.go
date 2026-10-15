package analysis

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/ast"
)

// OnceTable holds a project's module-level `once` bindings: those at the top
// of a file, in a type's namespace items, and in an impl block. One table is
// shared by every file of a project build, so a checker reading a `once`
// declared in another file can find that file.
//
// An unannotated `once` takes the type of its value, and only checking the
// value tells it. That type is part of its file's interface, so every one is
// inferred in one phase before any file's CheckTypes runs (infer), in a fixed
// order and with a checker for the declaring file, which is the only analysis
// the phase writes. A value that reads another unannotated `once` infers that
// one first, and a value that reads its own type is a cycle, which the
// declaring file's CheckTypes reports at the declaration. After the phase the
// table is read-only, so what a file's check sees does not depend on which
// file was checked first.
type OnceTable struct {
	sites map[*ast.OnceBinding]onceSite
	// checking is the bindings whose value the inference phase is checking,
	// innermost last. A read of one of them is a cycle.
	checking []*ast.OnceBinding
	// cycles holds, for each unannotated binding whose value reads its own
	// type, the names along the cycle, starting and ending with its own.
	cycles map[*ast.OnceBinding][]string
	// checked holds the bindings whose value the inference phase has
	// checked, so a read of one whose value has no type does not check it
	// again.
	checked map[*ast.OnceBinding]bool
	// inferOnce runs the inference phase; inferred is set when it is done.
	inferOnce sync.Once
	inferred  bool
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
	for _, node := range nodes {
		owner := ""
		if impl, ok := node.(*ast.ImplBlock); ok {
			owner = TypeExprBaseName(impl.Receiver)
		}
		for _, n := range moduleOnces(node) {
			t.sites[n] = onceSite{fa: fa, nodes: nodes, owner: owner}
		}
	}
}

// moduleOnces answers the module-level `once` bindings one top-level node
// holds: the node itself, or those among a type's namespace items or an impl
// block's items.
func moduleOnces(node ast.Node) []*ast.OnceBinding {
	var items []ast.Node
	switch n := node.(type) {
	case *ast.OnceBinding:
		return []*ast.OnceBinding{n}
	case *ast.StructDef:
		items = n.Items
	case *ast.EnumDef:
		items = n.Items
	case *ast.TypeDef:
		items = n.Items
	case *ast.ExternType:
		items = n.Items
	case *ast.ImplBlock:
		items = n.Items
	}
	var out []*ast.OnceBinding
	for _, item := range items {
		if n, ok := item.(*ast.OnceBinding); ok {
			out = append(out, n)
		}
	}
	return out
}

// infer runs the inference phase once: it gives every unannotated `once` its
// type, in the order of its file's Origin and path and then its position.
// CheckTypes runs it before it checks anything, so it is done before the
// first file of the program is checked, whichever file that is.
func (t *OnceTable) infer() {
	if t == nil {
		return
	}
	t.inferOnce.Do(func() {
		pending := make([]*ast.OnceBinding, 0, len(t.sites))
		for n := range t.sites {
			if n.TypeAnnotation == nil && n.Value != nil {
				pending = append(pending, n)
			}
		}
		slices.SortFunc(pending, func(a, b *ast.OnceBinding) int {
			sa, sb := t.sites[a].fa, t.sites[b].fa
			return cmp.Or(
				cmp.Compare(sa.Origin, sb.Origin),
				cmp.Compare(sa.FilePath, sb.FilePath),
				cmp.Compare(a.Line, b.Line),
				cmp.Compare(a.Col, b.Col),
			)
		})
		for _, n := range pending {
			t.inferType(n)
		}
		t.inferred = true
	})
}

// inferType checks an unannotated `once`'s value with a checker for its own
// file, unless the phase already has. Errors that check finds are dropped:
// the declaring file's own CheckTypes reports them at the declaration.
func (t *OnceTable) inferType(n *ast.OnceBinding) {
	if t.checked[n] {
		return
	}
	site := t.sites[n]
	sub := newChecker(site.fa, site.nodes)
	sub.selfTypeName = site.owner
	sub.checkOnce(n)
}

// ensureOnceType gives a module-level unannotated `once` its type before a
// read uses it. Only the inference phase infers one: once the phase is done,
// every type it could give is already on its symbol.
func (c *checker) ensureOnceType(sym *Symbol) {
	if sym == nil || sym.Kind != SymbolOnce || sym.Type != nil || c.fa == nil || c.fa.Program().Onces == nil {
		return
	}
	n, ok := sym.Node.(*ast.OnceBinding)
	if !ok || n.TypeAnnotation != nil {
		return
	}
	t := c.fa.Program().Onces
	if t.inferred {
		return
	}
	if _, ok := t.sites[n]; !ok {
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
	t.inferType(n)
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
