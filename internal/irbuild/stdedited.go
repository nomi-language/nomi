package irbuild

// A STDLIB MODULE TESTED FROM EDITED SOURCE.
//
// `nomi test <std>/<module>.nomi` tests the file as it is on disk. When that
// differs from the module embedded in the binary, the cached lowering's bodies
// for the module are the embedded ones, so a case must not call them: an
// edited body has to be what the module's own cases run. GenerateEditedStdlibTestIR
// lowers the module a second time, from the edited nodes, against the same
// resolution scope buildStdlibIndex gave it (the modules lowered before it),
// and builds the cases against that lowering.
//
// Only this module is re-lowered. Every other stdlib module stays the cached,
// embedded lowering, and a call another module makes into this one still
// reaches the embedded body: its graph names the cached symbol. The re-lowered
// module is linked after the cached ones, so an interface dispatch on one of
// this module's types (`Display.to_string(time)`) selects the edited impl.

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// GenerateEditedStdlibTestIR is GenerateStdlibTestIR for a stdlib module whose
// source differs from the embedded one: nodes and fa are the edited source,
// checked, and the module's own bodies are lowered from them rather than taken
// from the cached lowering.
func GenerateEditedStdlibTestIR(module string, nodes []ast.Node, fa *analysis.FileAnalysis) (*Program, *Result, error) {
	std := stdlibLowering()
	v := std.views[module]
	if v == nil {
		return nil, nil, fmt.Errorf("irbuild: std/%s is not a lowered stdlib module", module)
	}
	earlierMods := std.earlierMods[module]
	earlier := std.filtered(func(m string) bool { return earlierMods[m] })
	nv := stdModuleContext(module, v.path, v.pkg, nodes, fa, earlier)
	funcs, onces, irMod := lowerStdlibModule(nv, earlier)
	if irMod != nil {
		irLintFinished(irMod)
	}

	// The index the cases resolve against: every other module's cached
	// entries, and this module's re-lowered ones.
	edited := std.filtered(func(m string) bool { return m != module })
	for _, f := range funcs {
		edited.add(f)
	}
	for _, o := range onces {
		edited.addOnce(o)
	}
	edited.views[module] = nv
	if len(nv.unlowered) > 0 {
		edited.unlowered[v.pkg] = nv.unlowered
	}
	var extra []*ir.Module
	if irMod != nil && (len(irMod.Funcs()) > 0 || len(irMod.Cells()) > 0) {
		extra = append(extra, irMod)
	}
	return generateStdlibTestIR(edited, nv, nv.nodes, nv.fa, std.irModules, extra)
}

// filtered is a copy of the index holding only the entries of the modules keep
// accepts. The copy's maps are its own, so adding to it leaves x unchanged; the
// entries themselves are shared.
func (x *stdlibIndex) filtered(keep func(module string) bool) *stdlibIndex {
	out := &stdlibIndex{
		byType:      map[string][]*stdFunc{},
		byFile:      map[string]*stdFunc{},
		byIface:     map[string]map[kind][]*stdFunc{},
		unlowered:   map[string]map[string]string{},
		irModules:   map[string]*ir.Module{},
		modulePkg:   map[string]string{},
		byKey:       map[string]*stdFunc{},
		byOnce:      map[string]*stdOnce{},
		views:       map[string]*stdModuleView{},
		earlierMods: x.earlierMods,
	}
	keepFuncs := func(fs []*stdFunc) []*stdFunc {
		var kept []*stdFunc
		for _, f := range fs {
			if keep(f.module) {
				kept = append(kept, f)
			}
		}
		return kept
	}
	for key, fs := range x.byType {
		if kept := keepFuncs(fs); len(kept) > 0 {
			out.byType[key] = kept
		}
	}
	for key, f := range x.byFile {
		if keep(f.module) {
			out.byFile[key] = f
		}
	}
	for key, byRecv := range x.byIface {
		for recv, fs := range byRecv {
			if kept := keepFuncs(fs); len(kept) > 0 {
				if out.byIface[key] == nil {
					out.byIface[key] = map[kind][]*stdFunc{}
				}
				out.byIface[key][recv] = kept
			}
		}
	}
	for key, f := range x.byKey {
		if keep(f.module) {
			out.byKey[key] = f
		}
	}
	for key, o := range x.byOnce {
		if keep(o.module) {
			out.byOnce[key] = o
		}
	}
	for module, pkg := range x.modulePkg {
		// Every module keeps its package name: a name is not an entry, and
		// the edited module's re-lowering reuses its own.
		out.modulePkg[module] = pkg
		if !keep(module) {
			continue
		}
		if m := x.irModules[pkg]; m != nil {
			out.irModules[pkg] = m
		}
		if u := x.unlowered[pkg]; u != nil {
			out.unlowered[pkg] = u
		}
		if v := x.views[module]; v != nil {
			out.views[module] = v
		}
	}
	return out
}
