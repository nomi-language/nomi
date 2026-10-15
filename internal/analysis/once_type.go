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
	// reads holds, for each unannotated binding the inference phase
	// checked, the `once` bindings its value reads.
	reads map[*ast.OnceBinding][]*ast.OnceBinding
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
		t.sortForInference(pending)
		for _, n := range pending {
			t.inferType(n)
		}
		t.inferred = true
	})
}

// sortForInference sorts bindings into the order the inference phase infers
// them in: by their file's Origin and path, then by position.
func (t *OnceTable) sortForInference(pending []*ast.OnceBinding) {
	slices.SortFunc(pending, func(a, b *ast.OnceBinding) int {
		sa, sb := t.sites[a].fa, t.sites[b].fa
		return cmp.Or(
			cmp.Compare(sa.Origin, sb.Origin),
			cmp.Compare(sa.FilePath, sb.FilePath),
			cmp.Compare(a.Line, b.Line),
			cmp.Compare(a.Col, b.Col),
		)
	})
}

// reinfer infers again the unannotated `once` bindings of files, whose
// bodies (their values among them) a recheck has just walked again and not
// yet checked, as the inference phase infers them: in its order, each with
// a checker for its file, which is the only analysis it writes. It reports
// whether a file that reads one of them may now see something other than
// before: one came out with another type, or its value reads its own type,
// now or before, or one's value reads an unannotated `once` of another file,
// whose type was inferred against the old values. When it reports none,
// each binding keeps the type object it had (early cutoff), so nothing that
// reads one changes.
func (t *OnceTable) reinfer(files map[*FileAnalysis]bool) bool {
	if t == nil || !t.inferred {
		return true
	}
	var pending []*ast.OnceBinding
	syms := map[*ast.OnceBinding]*Symbol{}
	was := map[*ast.OnceBinding]Type{}
	for n, site := range t.sites {
		if site.fa == nil || !files[site.fa] || n.TypeAnnotation != nil || n.Value == nil {
			continue
		}
		if t.cycles[n] != nil {
			return true
		}
		sym := site.fa.Definitions[Pos{Line: n.Line, Col: n.Col}]
		if sym == nil {
			sym = site.fa.ModuleScope.Lookup(n.Name)
		}
		if sym == nil {
			return true
		}
		pending = append(pending, n)
		syms[n], was[n] = sym, sym.Type
	}
	if len(pending) == 0 {
		return false
	}
	t.sortForInference(pending)
	for _, n := range pending {
		syms[n].Type = nil
		delete(t.checked, n)
		delete(t.reads, n)
	}
	t.inferred = false
	for _, n := range pending {
		t.inferType(n)
	}
	t.inferred = true
	for _, n := range pending {
		if t.cycles[n] != nil || !sameOnceType(was[n], syms[n].Type) {
			return true
		}
	}
	if t.readsBackThroughOthers(pending, files) {
		return true
	}
	for _, n := range pending {
		syms[n].Type = was[n]
	}
	return false
}

// readsBackThroughOthers reports whether the value of one of pending, the
// bindings of files, reads one of them again through an unannotated
// binding of another file. That binding was inferred against the old
// values and is not inferred again, so a cycle through it would go unseen.
func (t *OnceTable) readsBackThroughOthers(pending []*ast.OnceBinding, files map[*FileAnalysis]bool) bool {
	ours := func(n *ast.OnceBinding) bool { return files[t.sites[n].fa] }
	seen := map[*ast.OnceBinding]bool{}
	var queue []*ast.OnceBinding
	for _, n := range pending {
		for _, r := range t.reads[n] {
			if !ours(r) && r.TypeAnnotation == nil && !seen[r] {
				seen[r] = true
				queue = append(queue, r)
			}
		}
	}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, r := range t.reads[n] {
			if r.TypeAnnotation != nil || seen[r] {
				continue
			}
			if ours(r) {
				return true
			}
			seen[r] = true
			queue = append(queue, r)
		}
	}
	return false
}

// sameOnceType reports whether a binding inferred as b, having been
// inferred as a, reads the same to every file.
func sameOnceType(a, b Type) bool {
	if a == nil || b == nil || isNilType(a) || isNilType(b) {
		return (a == nil || isNilType(a)) && (b == nil || isNilType(b))
	}
	return TypesEqual(a, b) && a.String() == b.String()
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
	if sym != nil && sym.Kind == SymbolOnce && c.fa != nil {
		// The inference phase notes what each value it checks reads.
		if t := c.fa.Program().Onces; t != nil && !t.inferred && len(t.checking) > 0 {
			if n, ok := sym.Node.(*ast.OnceBinding); ok {
				reader := t.checking[len(t.checking)-1]
				if t.reads == nil {
					t.reads = map[*ast.OnceBinding][]*ast.OnceBinding{}
				}
				t.reads[reader] = append(t.reads[reader], n)
			}
		}
	}
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
