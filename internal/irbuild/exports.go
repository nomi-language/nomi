package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// Export is one `export fn`: a top-level function a host calls by name, with
// each argument decoded from JSON by its type's FromJson and the result
// encoded by its type's ToJson. The front end has checked that the
// signature is concrete and that those impls exist, that the function is
// not generic, and that no other file exports the same name.
type Export struct {
	// Name is the function's Nomi name, the name a host calls it by.
	Name string
	// Module is the IR module of the file that declares it, and Func its
	// lowered body there. Func is nil when the builder declined the body;
	// vmhost reports that as it reports a declined `main`.
	Module *ir.Module
	Func   *ir.Func
	// Params are the declared parameters in order, and Result the declared
	// result type (Unit when the function declares none).
	Params []ExportParam
	Result analysis.Type
	// Encode is std/json's export_result instantiated at Result: it takes
	// the result and answers its JSON text. Nil for a Unit result, which
	// crosses as nothing, and when the builder declined the instance.
	Encode *ir.Func
	// Path is the declaring file, absolute; Line and Col are the name's.
	Path      string
	Line, Col int
}

// ExportParam is one parameter of an exported function.
type ExportParam struct {
	Name string
	Type analysis.Type
	// Decode is std/json's export_argument instantiated at Type: it takes
	// the argument's JSON text and answers Result<Type, String>, the Err
	// being the FromJson failure as text. Nil when the builder declined it.
	Decode *ir.Func
}

// Lowered reports whether everything a host's call of e runs was lowered:
// the function, each parameter's decoder and the result's encoder.
func (e *Export) Lowered() bool {
	if e.Func == nil {
		return false
	}
	for _, p := range e.Params {
		if p.Decode == nil {
			return false
		}
	}
	return e.Encode != nil || analysis.IsUnitType(e.Result)
}

// collectExports lists the program's exported functions in module order,
// then declaration order, after every body has been lowered.
func collectExports(p *Program, gens []*gen) []Export {
	var out []Export
	for i := range p.Modules {
		m := &p.Modules[i]
		g := gens[i]
		for _, n := range m.Nodes {
			fd, ok := n.(*ast.FuncDef)
			if !ok || !fd.Export {
				continue
			}
			e := Export{Name: fd.Name, Path: m.Path, Line: fd.Line, Col: fd.Col, Module: g.irMod, Result: analysis.TypeUnit}
			if g.irMod != nil {
				e.Func = g.irMod.FuncFor(g.irCalleeSym(fd, fd.Name))
			}
			var ft *analysis.FuncType
			if m.FA != nil {
				if sym := m.FA.Definitions[analysis.Pos{Line: fd.Line, Col: fd.Col}]; sym != nil && sym.Node == fd {
					ft, _ = sym.Type.(*analysis.FuncType)
				}
			}
			for j, prm := range fd.Params {
				ep := ExportParam{Name: prm.Name}
				if ft != nil && j < len(ft.Params) {
					ep.Type = ft.Params[j]
					ep.Decode = g.jsonCrossing("export_argument", ep.Type)
				}
				e.Params = append(e.Params, ep)
			}
			if ft != nil && ft.Return != nil {
				e.Result = ft.Return
			}
			if !analysis.IsUnitType(e.Result) {
				e.Encode = g.jsonCrossing("export_result", e.Result)
			}
			out = append(out, e)
		}
	}
	return out
}

// jsonCrossing is std/json's generic function name (export_argument or
// export_result) instantiated at t, the conversion a host's call runs on
// one side of an exported function, or nil when it cannot be built.
func (g *gen) jsonCrossing(name string, t analysis.Type) *ir.Func {
	if g.stdInsts == nil {
		return nil
	}
	f := stdModuleGenericTemplate(g.stdInsts.std.views["json"], "", name)
	if f == nil {
		return nil
	}
	k := g.project(t)
	// kindInvalid: sentinel — the projection's failure.
	if k == kindInvalid {
		return nil
	}
	tps, _ := g.stdInsts.stdTemplateParams(f)
	if len(tps) != 1 {
		return nil
	}
	inst, _ := g.stdInsts.instantiate(f, tps, []kind{k}, nil, g)
	if inst == nil || inst.gen.irMod == nil {
		return nil
	}
	return inst.gen.irMod.FuncFor(inst.sym)
}
