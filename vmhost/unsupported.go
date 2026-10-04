package vmhost

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/vm"
)

// Unsupported reports, as front-end diagnostics, every body the program
// reaches that the compiler accepted and cannot lower for the VM: exactly
// what `nomi run` and `nomi test` would refuse with BLOCKED, found without
// running anything. The roots are `main` and its boot, and every test case
// with its group's boot. Each diagnostic sits at the expression the lowering
// stopped at, or at the declaration when the body was refused as a whole,
// and is worded for the program's author: it names the construct, never the
// compiler's internal reason. Nil when everything the program reaches runs.
func (p *Program) Unsupported() error {
	var m *vm.Machine
	if p.entry != nil {
		m = p.machine(nilWriter{})
	}
	var found []string
	seen := map[string]bool{}
	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			found = append(found, name)
		}
	}
	reach := func(roots []*ir.Func, boots []*ir.Symbol) {
		for _, u := range m.Unretained(roots, boots) {
			// A host function the run binds from its host tables, which a
			// check does not have, is not a lowering's to report.
			if u.Kind == vm.NotRetained || u.Kind == vm.OnceNotRetained {
				add(u.Name)
			}
		}
	}
	if p.HasMain() {
		if mainFn := p.entryFunc("main"); mainFn == nil || m == nil {
			add("main")
		} else {
			var boots []*ir.Symbol
			if boot := p.entry.Boot(); boot != nil {
				boots = append(boots, boot)
			}
			reach([]*ir.Func{mainFn}, boots)
		}
	}
	if p.prog.HasTests {
		plan, _ := frontend.SelectTests(p.prog.Entry().Nodes, TestOptions{})
		retained := map[string]ir.TestCase{}
		if p.entry != nil {
			for _, c := range p.entry.Tests() {
				retained[c.Name()] = c
			}
		}
		for _, tc := range plan {
			c, ok := retained[tc.FullName()]
			if !ok || m == nil {
				add("test body: " + tc.FullName())
				continue
			}
			var boots []*ir.Symbol
			if b := c.Group().Boot; b != nil {
				boots = append(boots, b, c.Group().Startup)
			}
			reach([]*ir.Func{c.Fn()}, boots)
		}
	}
	if len(found) == 0 {
		return nil
	}
	var ds frontend.Diagnostics
	for _, name := range found {
		ds = append(ds, p.unsupportedDiagnostic(name))
	}
	sort.SliceStable(ds, func(i, j int) bool {
		if ds[i].Path != ds[j].Path {
			return ds[i].Path < ds[j].Path
		}
		if ds[i].Line != ds[j].Line {
			return ds[i].Line < ds[j].Line
		}
		return ds[i].Col < ds[j].Col
	})
	return ds
}

// nilWriter discards a machine's output; Unsupported runs nothing.
type nilWriter struct{}

func (nilWriter) Write(b []byte) (int, error) { return len(b), nil }

// unsupportedDiagnostic is one blocker, located and worded for the author.
func (p *Program) unsupportedDiagnostic(name string) frontend.Diagnostic {
	what := blockerSubject(name)
	d := p.declineOf(name)
	path, line, col := "", 0, 0
	if d != nil && d.Line > 0 {
		path, line, col = d.Path, d.Line, d.Col
	}
	if line == 0 {
		path, line, col = p.declarationOf(name)
	}
	if analysis.IsSynthesizedLine(line) {
		// A derive-synthesized body has no source line of its own.
		line, col = 0, 0
	}
	construct := "its body"
	if line > 0 {
		if n := nodeAt(p.modulePath(path), line, col); n != nil {
			construct = describeConstruct(n)
			if call, ok := n.(*ast.Call); ok {
				// A call's own position is its parenthesis; the author reads
				// the call at its callee.
				if l, c := leftmost(call.Func); l > 0 {
					line, col = l, c
				}
			}
		}
	}
	diag := frontend.NewDiagnostic(path, "", line, col,
		fmt.Sprintf("%s is not supported yet, so %s cannot run", construct, what))
	if d != nil {
		if hint := unsupportedHint(d.Reason); hint != "" {
			diag.Hints = append(diag.Hints, hint)
		}
	}
	return diag
}

// unsupportedHint is advice for the declines whose cause the author can act
// on, keyed by the lowering's reason, which is never shown itself.
func unsupportedHint(reason string) string {
	if name, ok := strings.CutPrefix(reason, "a direct call to a function that uses break or continue: "); ok {
		return "`" + name + "` uses break or continue, so pass it as the callback itself, as in `Iter.map(xs, " + name + ")`, rather than calling it"
	}
	if name, ok := strings.CutPrefix(reason, "an iter-sensitive function passed where its break or continue cannot be caught: "); ok {
		return "`" + name + "` uses break or continue; so far only Iter.map, Iter.filter, Iter.take_while, Iter.each, Iter.iterate and Iter.reduce can run such a function as their callback"
	}
	return ""
}

// blockerSubject names a blocker for the author: `fn name`, a test, or a
// `once` initializer.
func blockerSubject(name string) string {
	switch {
	case strings.HasPrefix(name, "test body: "):
		return "test " + strconv.Quote(strings.TrimPrefix(name, "test body: "))
	case strings.HasPrefix(name, "once "):
		return "`once " + strings.TrimPrefix(name, "once ") + "`"
	}
	return "`fn " + name + "`"
}

// declineOf is the decline the lowering recorded for name: by the attempt's
// own name, then by a qualified name's last segment, preferring one in a
// file of this program over a stdlib file.
func (p *Program) declineOf(name string) *irbuild.Decline {
	keys := []string{name}
	if i := strings.LastIndex(name, "."); i >= 0 && !strings.HasPrefix(name, "test body: ") {
		keys = append(keys, name[i+1:])
	}
	for _, key := range keys {
		var any *irbuild.Decline
		for _, d := range p.declineDetails {
			if d.Fn != key {
				continue
			}
			if p.modulePath(d.Path) != nil {
				return d
			}
			if any == nil {
				any = d
			}
		}
		if any != nil {
			return any
		}
	}
	return nil
}

// modulePath is the program module whose file is path, or nil for a stdlib
// file or an unknown one.
func (p *Program) modulePath(path string) *irbuild.Module {
	if path == "" {
		return nil
	}
	for i := range p.prog.Modules {
		if p.prog.Modules[i].Path == path {
			return &p.prog.Modules[i]
		}
	}
	return nil
}

// declarationOf locates the declaration name blames: a test case, a `once`
// binding, or a function (an impl function by its last segment), searched in
// the entry first. It answers the entry's path at line 0 when none matches.
func (p *Program) declarationOf(name string) (string, int, int) {
	entry := p.prog.Entry()
	if test, ok := strings.CutPrefix(name, "test body: "); ok {
		plan, _ := frontend.SelectTests(entry.Nodes, TestOptions{})
		for _, tc := range plan {
			if tc.FullName() == test {
				return entry.Path, tc.Line, 0
			}
		}
		return entry.Path, 0, 0
	}
	once, isOnce := strings.CutPrefix(name, "once ")
	short := name
	if i := strings.LastIndex(name, "."); i >= 0 {
		short = name[i+1:]
	}
	for i := range p.prog.Modules {
		mod := &p.prog.Modules[i]
		var line, col int
		for _, top := range mod.Nodes {
			analysis.WalkNodes(top, func(n ast.Node) {
				if line != 0 {
					return
				}
				switch d := n.(type) {
				case *ast.FuncDef:
					if !isOnce && (d.Name == name || d.Name == short) {
						line, col = d.Line, d.Col
					}
				case *ast.OnceBinding:
					if isOnce && d.Name == once {
						line, col = d.LineNum(), 0
					}
				}
			})
			if line != 0 {
				return mod.Path, line, col
			}
		}
	}
	return entry.Path, 0, 0
}

// nodeAt is the outermost node of mod that starts at line and col, or nil.
func nodeAt(mod *irbuild.Module, line, col int) ast.Node {
	if mod == nil {
		return nil
	}
	var found ast.Node
	for _, top := range mod.Nodes {
		analysis.WalkNodes(top, func(n ast.Node) {
			if found != nil {
				return
			}
			if l, c := nodeStart(n); l == line && c == col {
				found = n
			}
		})
		if found != nil {
			return found
		}
	}
	return nil
}

// nodeStart is a node's own line and column, for the kinds a lowering stops
// at; 0 for any other.
func nodeStart(n ast.Node) (int, int) {
	switch t := n.(type) {
	case *ast.Call:
		return t.Line, t.Col
	case *ast.Ident:
		return t.Line, t.Col
	case *ast.TypeIdent:
		return t.Line, t.Col
	case *ast.FieldAccess:
		return t.Line, t.Col
	case *ast.Lambda:
		return t.Line, t.Col
	case *ast.StringInterp:
		return t.Line, t.Col
	case *ast.Binary:
		return t.Line, t.Col
	case *ast.Unary:
		return t.Line, t.Col
	case *ast.If:
		return t.Line, t.Col
	case *ast.Case:
		return t.Line, t.Col
	case *ast.StructLit:
		return t.Line, t.Col
	case *ast.ListLit:
		return t.Line, t.Col
	case *ast.MapLit:
		return t.Line, t.Col
	case *ast.TupleLit:
		return t.Line, t.Col
	case *ast.Break:
		return t.Line, t.Col
	case *ast.Continue:
		return t.Line, t.Col
	case *ast.Return:
		return t.Line, t.Col
	case *ast.TryOp:
		return t.Line, t.Col
	}
	return 0, 0
}

// describeConstruct names the construct n is in the author's words.
func describeConstruct(n ast.Node) string {
	switch t := n.(type) {
	case *ast.Call:
		if callee := calleeText(t.Func); callee != "" {
			return "this call to `" + callee + "`"
		}
		return "this call"
	case *ast.Ident:
		return "using `" + t.Name + "` here"
	case *ast.TypeIdent:
		return "using `" + t.Name + "` here"
	case *ast.FieldAccess:
		if text := calleeText(t); text != "" {
			return "`" + text + "` here"
		}
		return "this field access"
	case *ast.Lambda:
		return "this function literal"
	case *ast.StringInterp:
		return "this string interpolation"
	case *ast.Binary:
		return "the `" + t.Op + "` operator here"
	case *ast.Unary:
		return "the `" + t.Op + "` operator here"
	case *ast.If:
		return "this `if`"
	case *ast.Case:
		return "this `case`"
	case *ast.StructLit:
		return "this struct literal"
	case *ast.ListLit:
		return "this list literal"
	case *ast.MapLit:
		return "this map literal"
	case *ast.TupleLit:
		return "this tuple"
	case *ast.Break:
		return "`break` here"
	case *ast.Continue:
		return "`continue` here"
	case *ast.Return:
		return "`return` here"
	case *ast.TryOp:
		return "`try` here"
	}
	return "this expression"
}

// calleeText spells a callee as written: `f`, `Type.f`, `mod.Type.f`.
func calleeText(n ast.Node) string {
	switch t := n.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.TypeIdent:
		return t.Name
	case *ast.FieldAccess:
		if t.Field == nil {
			return ""
		}
		if obj := calleeText(t.Object); obj != "" {
			return obj + "." + t.Field.Name
		}
		if te, ok := t.Object.(ast.TypeExpr); ok {
			return te.TypeString() + "." + t.Field.Name
		}
	}
	return ""
}

// leftmost is the position where the callee expression n starts.
func leftmost(n ast.Node) (int, int) {
	if fa, ok := n.(*ast.FieldAccess); ok {
		if l, c := leftmost(fa.Object); l > 0 {
			return l, c
		}
	}
	return nodeStart(n)
}
