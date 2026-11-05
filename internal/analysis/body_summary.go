package analysis

import (
	"crypto/sha256"
	"fmt"
	"io"
	"sort"

	"github.com/nomi-language/nomi/internal/ast"
)

// BodySummary fingerprints what the program's reductions over bodies read
// of one file's bodies: the boot-only check's calls, lambda calls and
// function values of each function (bootSummaryOf), each declaration's
// references to declarations and members, in order, with the impl a
// dispatched call resolved to (whole-file imports, application-field
// roots), and the impls the file's checks demand (coherence's missing
// impls). It leaves out positions in the file's own top-level frame: a
// target in the file is named by the top-level declaration it is in and
// its place there, and a target in another file by its own place, so an
// edit that only moves the file's declarations, or changes what the
// reductions do not read (a literal, an arithmetic operator), keeps the
// fingerprint.
//
// A kept program's reductions found nothing. When an edited file's body
// summary is the kept one, no reduction can find anything now either, so a
// build that reuses the other files' kept results (vmhost's file records)
// needs no reduction over their bodies. fa and nodes are the file's
// analysis and nodes as its check left them: the whole program's check
// and the file's check against interfaces answer the same fingerprint for
// an unchanged file.
func BodySummary(fa *FileAnalysis, nodes []ast.Node) [32]byte {
	h := sha256.New()
	if fa == nil {
		return [32]byte(h.Sum(nil))
	}
	names := DeclarationNames(nodes)
	frame := newTopFrame(nodes, names)
	target := func(sym *Symbol) string {
		for sym != nil && sym.Resolved != nil {
			sym = sym.Resolved
		}
		if sym == nil {
			return "-"
		}
		switch sym.Kind {
		case SymbolParam, SymbolBinding, SymbolArgHint, SymbolAssertion, SymbolTestSetup,
			SymbolTryOp, SymbolControlFlow, SymbolImplKeyword, SymbolLiteral:
			return ""
		}
		return fmt.Sprintf("%d %s %s", sym.Kind, sym.Name, frame.place(sym.Node, sym.SourceFile == fa.FilePath && fa.FilePath != "", names))
	}
	node := func(n ast.Node) string {
		return frame.place(n, true, names)
	}

	// The references, by the top-level declaration each is in, in order.
	positions := make([]Pos, 0, len(fa.References))
	for pos := range fa.References {
		positions = append(positions, pos)
	}
	sort.Slice(positions, func(i, j int) bool {
		if positions[i].Line != positions[j].Line {
			return positions[i].Line < positions[j].Line
		}
		return positions[i].Col < positions[j].Col
	})
	owner := ""
	for _, pos := range positions {
		ref := fa.References[pos]
		t := target(ref)
		if t == "" && (ref == nil || ref.DispatchImpl == nil) {
			continue
		}
		if o := frame.owner(pos); o != owner {
			owner = o
			fmt.Fprintf(h, "in %s\n", owner)
		}
		fmt.Fprintf(h, "ref %s", t)
		if ref != nil && ref.DispatchImpl != nil {
			fmt.Fprintf(h, " dispatch %s", node(ref.DispatchImpl))
		}
		io.WriteString(h, "\n")
	}

	// The boot-only check's reading of each function.
	bootMu.Lock()
	s := fa.bootSummaryOf()
	bootMu.Unlock()
	fns := make([]string, 0, len(s.fns))
	byName := map[string]*bootFnSummary{}
	for fd, fn := range s.fns {
		name := names[fd]
		if name == "" {
			name = node(fd)
		}
		fns = append(fns, name)
		byName[name] = fn
	}
	sort.Strings(fns)
	refs := func(kind string, list []bootRef) {
		for _, r := range list {
			fmt.Fprintf(h, "%s %s %t\n", kind, target(r.sym), r.supervisor)
		}
	}
	for _, name := range fns {
		fn := byName[name]
		fmt.Fprintf(h, "boot %s\n", name)
		refs("call", fn.body.calls)
		refs("lambda call", fn.body.lambdaCalls)
		for _, v := range fn.body.values {
			fmt.Fprintf(h, "value %s\n", target(v.sym))
		}
		for _, c := range fn.called {
			fmt.Fprintf(h, "called %s\n", target(c))
		}
	}

	// The impls the file's checks demand.
	ifaces := make([]string, 0, len(fa.ImplManifest))
	for iface := range fa.ImplManifest {
		ifaces = append(ifaces, iface)
	}
	sort.Strings(ifaces)
	for _, iface := range ifaces {
		types := make([]string, 0, len(fa.ImplManifest[iface]))
		for t := range fa.ImplManifest[iface] {
			types = append(types, t)
		}
		sort.Strings(types)
		for _, t := range types {
			kinds := map[string]bool{}
			for _, r := range fa.ImplManifest[iface][t] {
				kinds[fmt.Sprintf("%d %s %s %t", r.Kind, r.Op, r.TypeOrigin, r.Derive != nil)] = true
			}
			list := make([]string, 0, len(kinds))
			for k := range kinds {
				list = append(list, k)
			}
			sort.Strings(list)
			fmt.Fprintf(h, "demand %s %s %v\n", iface, t, list)
		}
	}
	return [32]byte(h.Sum(nil))
}

// topFrame places a node of one file by the top-level declaration it is in.
type topFrame struct {
	starts []Pos
	names  []string
}

func newTopFrame(nodes []ast.Node, names map[ast.Node]string) *topFrame {
	f := &topFrame{}
	type top struct {
		pos  Pos
		name string
	}
	var tops []top
	for i, n := range nodes {
		pos := topLevelPos(n)
		if pos.Line == 0 || IsSynthesizedLine(pos.Line) {
			continue
		}
		name := names[n]
		if name == "" {
			name = fmt.Sprintf("#%d", i)
		}
		tops = append(tops, top{pos, name})
	}
	sort.SliceStable(tops, func(i, j int) bool {
		a, b := tops[i].pos, tops[j].pos
		return a.Line < b.Line || a.Line == b.Line && a.Col < b.Col
	})
	for _, t := range tops {
		f.starts = append(f.starts, t.pos)
		f.names = append(f.names, t.name)
	}
	return f
}

// owner is the top-level declaration pos is in, by name.
func (f *topFrame) owner(pos Pos) string {
	i := sort.Search(len(f.starts), func(i int) bool {
		s := f.starts[i]
		return s.Line > pos.Line || s.Line == pos.Line && s.Col > pos.Col
	}) - 1
	if i < 0 {
		return "-"
	}
	return f.names[i]
}

// place names n: a declaration by its name, a node of the file (own) by
// the top-level declaration it is in and its line and column from that
// declaration's start, and any other node by its line and column.
func (f *topFrame) place(n ast.Node, own bool, names map[ast.Node]string) string {
	if n == nil {
		return "-"
	}
	if name, ok := names[n]; ok && own {
		return "decl " + name
	}
	pos := topLevelPos(n)
	if !own || pos.Line == 0 || IsSynthesizedLine(pos.Line) {
		return fmt.Sprintf("at %d:%d", pos.Line, pos.Col)
	}
	i := sort.Search(len(f.starts), func(i int) bool {
		s := f.starts[i]
		return s.Line > pos.Line || s.Line == pos.Line && s.Col > pos.Col
	}) - 1
	if i < 0 {
		return fmt.Sprintf("at %d:%d", pos.Line, pos.Col)
	}
	return fmt.Sprintf("in %s +%d:%d", f.names[i], pos.Line-f.starts[i].Line, pos.Col)
}
