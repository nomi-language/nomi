package analysis

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// CountStdlibWorkForTest runs f with the stdlib-work counter on and answers
// how many visits to a stdlib file, or to an impl a stdlib file declares, the
// program-wide passes made while it ran (noteStdlibWork).
func CountStdlibWorkForTest(f func()) int64 {
	countingStdlibWork.Store(true)
	defer countingStdlibWork.Store(false)
	before := stdlibWork.Load()
	f()
	return stdlibWork.Load() - before
}

// UsesStdBaseForTest reports whether fa's program took the stdlib's share of
// its facts from a StdBase.
func UsesStdBaseForTest(fa *FileAnalysis) bool {
	return fa.Program().stdBase != nil
}

// DumpStdBaseForTest renders what b holds as canonical text, so a test can
// show that no build writes it.
func DumpStdBaseForTest(b *StdBase) string {
	var lines []string
	add := func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	for recv, ifaces := range b.covered {
		for iface := range ifaces {
			add("covered %s %s", recv, iface)
		}
	}
	for recv, ifaces := range b.blocked {
		for iface := range ifaces {
			add("blocked %s %s", recv, iface)
		}
	}
	for name, origin := range b.primitives {
		add("primitive %s %s", name, origin)
	}
	for name := range b.names {
		add("name %s", name)
	}
	for iface, byMethod := range b.collisionGroups {
		for method, groups := range byMethod {
			for group, impls := range groups {
				for _, impl := range impls {
					add("collision %s.%s %q %s@%d:%d #%d", iface, method, group, impl.fn.Name, impl.fn.Line, impl.fn.Col, impl.at)
				}
			}
		}
	}
	for name, decls := range b.enumDecls {
		for _, d := range decls {
			add("enum %s %s %p", name, d.key, d.sym)
		}
	}
	for name, mods := range b.declModules {
		add("declared %s %v", name, mods)
	}
	for iface, byType := range b.demands {
		for typeName, recs := range byType {
			add("demand %s %s %d", iface, typeName, len(recs))
		}
	}
	for pair := range b.pairs {
		add("pair %s %s", pair.typeName, pair.iface)
	}
	for _, pair := range b.wanted {
		add("wanted %s %s", pair.typeName, pair.iface)
	}
	sort.Strings(lines)
	return DumpProgramIndexForTest(&Program{ImplIndex: b.index}, "") + strings.Join(lines, "\n") + "\n"
}

// DumpProgramIndexForTest renders every table of p's impl index as
// canonical text, one sorted line per entry. Two indexes that dump alike
// answer every lookup alike. Each list of impls keeps its order; each set of
// type-argument templates is rendered as a set, since a duplicate in one
// changes no answer. root is stripped from the paths it names.
func DumpProgramIndexForTest(p *Program, root string) string {
	idx := p.ImplIndex
	if idx == nil {
		return "no index\n"
	}
	path := func(s string) string { return strings.TrimPrefix(s, root) }
	fn := func(f *ast.FuncDef) string {
		if f == nil {
			return "<nil>"
		}
		return fmt.Sprintf("%s@%d:%d", f.Name, f.Line, f.Col)
	}
	ext := func(e *ast.ExternFunc) string {
		if e == nil {
			return "<nil>"
		}
		return fmt.Sprintf("%s@%d:%d", e.Name, e.Line, e.Col)
	}
	typ := func(t Type) string {
		if t == nil || isNilType(t) {
			return "<nil>"
		}
		return typeVarID.ReplaceAllString(t.String(), "?")
	}
	sym := func(s *Symbol) string {
		if s == nil {
			return "<nil>"
		}
		return fmt.Sprintf("%s/%d@%d:%d in %s : %s", s.Name, s.Kind, s.Pos.Line, s.Pos.Col, path(s.SourceFile), typ(s.Type))
	}
	ita := func(a *ImplTypeArgs) string {
		if a == nil {
			return "<nil>"
		}
		args := make([]string, len(a.Args))
		for i, t := range a.Args {
			args[i] = typ(t)
		}
		return "[" + strings.Join(args, ", ") + "]"
	}
	var lines []string
	add := func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	for t, ifaces := range idx.Impls {
		for i, ok := range ifaces {
			add("impls %s %s %v", t, i, ok)
		}
	}
	for i, byType := range idx.ImplManifest {
		for t, recs := range byType {
			for n, r := range recs {
				add("manifest %s %s #%d %d %s:%d:%d %s", i, t, n, r.Kind, path(r.Pos.File), r.Pos.Line, r.Pos.Col, r.TypeOrigin)
			}
		}
	}
	for i, byMethod := range idx.IfaceMethodImpls {
		for m, fns := range byMethod {
			names := make([]string, len(fns))
			for n, f := range fns {
				names[n] = fn(f) + " in " + idx.ImplFiles[f]
			}
			add("ifaceMethodImpls %s.%s %s", i, m, strings.Join(names, ", "))
		}
	}
	for f, home := range idx.ImplFiles {
		add("implFiles %s %s", fn(f), home)
	}
	for t, byIface := range idx.ImplTypeArgs {
		for i, a := range byIface {
			add("implTypeArgs %s %s %s", t, i, ita(a))
		}
	}
	for t, byIface := range idx.ImplTypeArgSets {
		for i, set := range byIface {
			distinct := map[string]bool{}
			for _, a := range set {
				distinct[ita(a)] = true
			}
			var rendered []string
			for s := range distinct {
				rendered = append(rendered, s)
			}
			sort.Strings(rendered)
			add("implTypeArgSets %s %s %s", t, i, strings.Join(rendered, " "))
		}
	}
	for f, t := range idx.ImplFuncTypes {
		add("implFuncTypes %s in %s %s", fn(f), idx.ImplFiles[f], typ(t))
	}
	for t, byMethod := range idx.TypeMethods {
		for m, s := range byMethod {
			add("typeMethods %s.%s %s from %s", t, m, sym(s), idx.TypeMethodModule[t][m])
		}
	}
	for t, byMethod := range idx.TypeMethodModule {
		for m, mod := range byMethod {
			add("typeMethodModule %s.%s %s", t, m, mod)
		}
	}
	for f, r := range idx.ImplBlockReceiver {
		add("implBlockReceiver %s %s", fn(f), r)
	}
	for f, k := range idx.ImplBlockInterfaceKey {
		add("implBlockInterfaceKey %s %s", fn(f), k)
	}
	for f, r := range idx.QualifiedReceiver {
		add("qualifiedReceiver %s %s", fn(f), r)
	}
	for f, o := range idx.ImplIfaceOrigin {
		add("implIfaceOrigin %s %s", fn(f), o)
	}
	for r, byIface := range idx.IfaceOriginByPair {
		for i, o := range byIface {
			add("ifaceOriginByPair %s %s %s", r, i, o)
		}
	}
	for k, ok := range idx.ImplsByIdentity {
		add("implsByIdentity %s %s %s %v", k.Origin, k.Type, k.Iface, ok)
	}
	for t, ifaces := range idx.implNamesWithIdentity {
		for i, ok := range ifaces {
			add("implNamesWithIdentity %s %s %v", t, i, ok)
		}
	}
	for k, s := range idx.TypeMethodByIdentity {
		add("typeMethodByIdentity %s %s.%s %s from %s", k.Origin, k.Type, k.Method, sym(s), idx.TypeMethodModuleByIdentity[k])
	}
	for k, mod := range idx.TypeMethodModuleByIdentity {
		add("typeMethodModuleByIdentity %s %s.%s %s", k.Origin, k.Type, k.Method, mod)
	}
	for _, k := range idx.TypeMethodIdentityConflicts {
		add("typeMethodIdentityConflict %s %s.%s", k.Origin, k.Type, k.Method)
	}
	for i, byMethod := range idx.IfaceMethodImplExterns {
		for m, exts := range byMethod {
			names := make([]string, len(exts))
			for n, e := range exts {
				names[n] = ext(e) + " in " + idx.ImplExternFiles[e]
			}
			add("ifaceMethodImplExterns %s.%s %s", i, m, strings.Join(names, ", "))
		}
	}
	for e, home := range idx.ImplExternFiles {
		add("implExternFiles %s %s", ext(e), home)
	}
	for e, r := range idx.ImplBlockReceiverExtern {
		add("implBlockReceiverExtern %s %s", ext(e), r)
	}
	for e, k := range idx.ImplBlockInterfaceKeyExtern {
		add("implBlockInterfaceKeyExtern %s %s", ext(e), k)
	}
	for name, syms := range idx.EnumDecls {
		rendered := make([]string, len(syms))
		for n, s := range syms {
			rendered[n] = sym(s)
		}
		add("enumDecls %s %s", name, strings.Join(rendered, ", "))
	}
	for _, e := range p.Errors {
		add("programError %d:%d %s", e.Line, e.Col, e.Message)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}
