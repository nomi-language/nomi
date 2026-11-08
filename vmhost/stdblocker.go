package vmhost

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/std"
)

// A stdlib module's cases are lowered against the process's cached stdlib
// lowering, which records no decline of a std body: a std function that did
// not lower is only marked so, and a case's call to it declines at the call.
// For a program that is right, since its author did not write std. In a
// stdlib module's own cases it names the wrong place: a std/timer.nomi
// prompt's `Duration.nanoseconds(0)` was reported as "this call to
// `Duration.nanoseconds` is not supported yet" at the prompt, while the
// construct the builder declined was in std/duration.nomi. stdBlocker
// follows such a call into the std body that declined
// (irbuild.StdlibDeclines), and on through each call of that body to
// another std function that did not lower, so the report names the
// innermost construct in its own file and line.

// stdBlocker is the decline d, taken in a stdlib module's cases at a call
// to a std function that did not lower, leads to: that function's own
// decline, followed through its calls in turn, with Path its module's file
// ("std/duration.nomi"), and the function as the call names it
// (`Duration.nanoseconds`). nil when p holds no stdlib module's cases, when
// d is at no such call, or when the callee's decline has no position.
func (p *Program) stdBlocker(d *irbuild.Decline) (*irbuild.Decline, string) {
	if p.stdModule == "" || d == nil || d.Line <= 0 {
		return nil, ""
	}
	module := p.stdModule
	var nodes []ast.Node
	if m := p.modulePath(d.Path); m != nil {
		nodes = m.Nodes
	} else {
		nodes = stdModuleNodes(module)
	}
	var found *irbuild.Decline
	subject := ""
	seen := map[string]bool{}
	for {
		call, ok := nodeAtIn(nodes, d.Line, d.Col).(*ast.Call)
		if !ok {
			break
		}
		callee, fn := stdCallee(module, nodes, call.Func)
		if callee == "" || seen[callee+"."+fn] {
			break
		}
		seen[callee+"."+fn] = true
		inner := p.stdDecline(callee, fn)
		if inner == nil || inner.Line <= 0 {
			break
		}
		at := *inner
		at.Path = stdModulePath(callee)
		found, subject, d, module, nodes = &at, fn, &at, callee, stdModuleNodes(callee)
	}
	return found, subject
}

// stdDecline is the decline of module's function fn (`count`,
// `Duration.nanoseconds`) from a fresh lowering of module, kept per program,
// or nil when fn lowered.
//
// The tested module's own bodies are looked up first in the program's
// lowering: when the file is edited (irbuild.GenerateEditedStdlibTestIR)
// that lowering built them, from the edited source, with the program's
// hooks.
func (p *Program) stdDecline(module, fn string) *irbuild.Decline {
	if module == p.stdModule {
		if d := declineNamed(p.declineDetails, fn); d != nil {
			return d
		}
	}
	if p.stdDeclines == nil {
		p.stdDeclines = map[string][]*irbuild.Decline{}
	}
	declines, ok := p.stdDeclines[module]
	if !ok {
		lowering.Lock()
		declines = irbuild.StdlibDeclines(module)
		lowering.Unlock()
		p.stdDeclines[module] = declines
	}
	return declineNamed(declines, fn)
}

// declineNamed is the decline in declines of the function fn's attempt, or
// nil. An impl function's attempt is named by the function alone
// (`nanoseconds` for `Duration.nanoseconds`).
func declineNamed(declines []*irbuild.Decline, fn string) *irbuild.Decline {
	names := []string{fn}
	if i := strings.LastIndex(fn, "."); i >= 0 {
		names = append(names, fn[i+1:])
	}
	for _, name := range names {
		for _, d := range declines {
			if d.Fn == name && d.Once == "" {
				return d
			}
		}
	}
	return nil
}

// stdCallee is the stdlib module and function a call's callee names from
// module, whose nodes are nodes, as the lowering names a std function's
// attempt: a bare name its own top-level function, `m.f` the function f of
// the module imported as m, and `T.f` the function `T.f` of the module that
// declares T. "" for any other callee.
func stdCallee(module string, nodes []ast.Node, callee ast.Node) (string, string) {
	switch c := callee.(type) {
	case *ast.Ident:
		for _, n := range nodes {
			if fd, ok := n.(*ast.FuncDef); ok && fd.Name == c.Name {
				return module, c.Name
			}
		}
	case *ast.FieldAccess:
		if c.Field == nil {
			return "", ""
		}
		switch obj := c.Object.(type) {
		case *ast.Ident:
			if m := importedStdModule(module, obj.Name); m != "" {
				return m, c.Field.Name
			}
		case *ast.TypeIdent:
			if m := stdTypeModule(module, obj.Name); m != "" {
				return m, obj.Name + "." + c.Field.Name
			}
		}
	}
	return "", ""
}

// stdTypeModule is the stdlib module that declares the type module's scope
// names name (`Duration`, declared in std/duration.nomi), or "".
func stdTypeModule(module, name string) string {
	lib := std.Shared().Ensure(module).Latest()
	fa := lib.Files[module]
	if fa == nil || fa.ModuleScope == nil {
		return ""
	}
	sym := fa.ModuleScope.Lookup(name)
	for sym != nil && sym.Resolved != nil && sym.Resolved != sym {
		sym = sym.Resolved
	}
	if sym == nil {
		return ""
	}
	for m, scope := range lib.Modules {
		if scope != nil && scope.LookupLocal(name) == sym && lib.Files[m] != nil {
			if declared := lib.Files[m].ModuleScope; declared != nil && declared.LookupLocal(name) == sym {
				return m
			}
		}
	}
	return ""
}

// importedStdModule is the stdlib module that module's scope binds name
// to (`import duration`, or `duration` in a braced import list), or "".
func importedStdModule(module, name string) string {
	lib := std.Shared().Ensure(module).Latest()
	fa := lib.Files[module]
	if fa == nil || fa.ModuleScope == nil {
		return ""
	}
	sym := fa.ModuleScope.Lookup(name)
	if sym == nil || sym.Kind != analysis.SymbolModule || sym.ModuleScope == nil {
		return ""
	}
	for m, scope := range lib.Modules {
		if scope == sym.ModuleScope {
			return m
		}
	}
	return ""
}

// stdModuleNodes are the shared stdlib's checked nodes of module, its
// bodies among them.
func stdModuleNodes(module string) []ast.Node {
	lib := std.Shared().Ensure(module)
	if err := lib.Bodies(module); err != nil {
		return nil
	}
	return lib.Latest().Nodes[module]
}

// stdModuleAt is the stdlib module whose file a diagnostic names path
// ("std/log.nomi"), with its nodes, or nil when path names none.
func stdModuleAt(path string) *irbuild.Module {
	if name, ok := std.ImageName(path); ok {
		path = name
	}
	rest, ok := strings.CutPrefix(path, "std/")
	if !ok {
		return nil
	}
	module := strings.TrimSuffix(rest, ".nomi")
	if _, ok := std.ReadFile(module); !ok {
		return nil
	}
	return &irbuild.Module{Path: path, Nodes: stdModuleNodes(module)}
}

// stdModulePath is the path a diagnostic names module's file by.
func stdModulePath(module string) string { return "std/" + module + ".nomi" }

// nodeAtIn is nodeAt over nodes.
func nodeAtIn(nodes []ast.Node, line, col int) ast.Node {
	var found ast.Node
	for _, top := range nodes {
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
