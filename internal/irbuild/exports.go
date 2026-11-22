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
				if prm.Destructure != nil {
					// `Req{path:}` names no parameter, as `Req` written
					// alone does (its name is `_`): the parser's
					// `__destr_` slot is not a name a host should see.
					ep.Name = "_"
				}
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
	f := g.stdInsts.stdTemplateOf(analysis.StdFunc{Module: "std/json", Name: name})
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
	inst, _ := g.stdInsts.instantiateTyped(f, tps, []kind{k}, []analysis.Type{t}, g)
	if inst == nil || inst.gen.irMod == nil {
		return nil
	}
	return inst.gen.irMod.FuncFor(inst.sym)
}

// HostFn is one `host fn` at the top of a project's file, as a host that
// answers it in JSON sees it (a WASI reactor's, vmrunner.WithHostCalls):
// the VM encodes each argument by its type's ToJson, the host answers JSON
// text, and the VM decodes it by the result's FromJson. These are the
// exported functions' conversions in the other direction, std/json's
// export_result at each parameter and export_argument at the result. A
// host that takes the VM's own values (vmhost.WithHosts) needs none of
// them.
type HostFn struct {
	// Key is the key the host answers it under (frontend.HostKey), the one
	// its crossing is named by.
	Key string
	// Name is the function's Nomi name.
	Name   string
	Params []HostFnParam
	// Result is the declared result type (Unit when it declares none).
	Result analysis.Type
	// Decode is export_argument instantiated at Result: it takes the
	// host's JSON text and answers Result<Result, String>. Nil for a Unit
	// result, and when NoJSON is set or the builder declined it.
	Decode *ir.Func
	// NoJSON is why the signature cannot cross as JSON, the checker's
	// record (FileAnalysis.HostJSONForms), or "" when it can.
	NoJSON string
}

// HostFnParam is one parameter of a HostFn.
type HostFnParam struct {
	Name string
	Type analysis.Type
	// Encode is export_result instantiated at Type: it takes the argument
	// and answers its JSON text. Nil when NoJSON is set or the builder
	// declined it.
	Encode *ir.Func
}

// Lowered reports whether every conversion a JSON host's call of h runs
// was lowered. It is false when the checker recorded that h cannot cross.
func (h *HostFn) Lowered() bool {
	if h.NoJSON != "" {
		return false
	}
	for _, p := range h.Params {
		if p.Encode == nil {
			return false
		}
	}
	return h.Decode != nil || analysis.IsUnitType(h.Result)
}

// Signature is h as Nomi declares it, without `host fn`:
// `greet(name: String) -> String`.
func (h *HostFn) Signature() string {
	x := ir.Export{Name: h.Name}
	for _, p := range h.Params {
		x.Params = append(x.Params, ir.ExportParam{Name: p.Name, Type: p.Type.String()})
	}
	if !analysis.IsUnitType(h.Result) {
		x.Result = h.Result.String()
	}
	return x.Signature()
}

// collectHostFns lists the `host fn`s at the top of the program's own files
// in module order, then declaration order, after every body has been
// lowered, with the conversions a JSON host's call runs when the checker
// recorded that the signature crosses (FileAnalysis.HostJSONForms).
func collectHostFns(p *Program, gens []*gen) []HostFn {
	var out []HostFn
	for i := range p.Modules {
		m := &p.Modules[i]
		g := gens[i]
		if g.stdModule != "" || m.FA == nil {
			continue
		}
		for _, n := range m.Nodes {
			ef, ok := n.(*ast.ExternFunc)
			if !ok {
				continue
			}
			h := HostFn{Key: g.irHostBindingKey(ef), Name: ef.Name, Result: analysis.TypeUnit}
			why, recorded := m.FA.HostJSONForms[ef]
			if !recorded {
				why = "the checker recorded no JSON form for it"
			}
			h.NoJSON = why
			var ft *analysis.FuncType
			if sym := m.FA.Definitions[analysis.Pos{Line: ef.Line, Col: ef.Col}]; sym != nil {
				ft, _ = sym.Type.(*analysis.FuncType)
			}
			for j, prm := range ef.Params {
				hp := HostFnParam{Name: prm.Name}
				if ft != nil && j < len(ft.Params) {
					hp.Type = ft.Params[j]
					if why == "" {
						hp.Encode = g.jsonCrossing("export_result", hp.Type)
					}
				}
				h.Params = append(h.Params, hp)
			}
			if ft != nil && ft.Return != nil {
				h.Result = ft.Return
			}
			if why == "" && !analysis.IsUnitType(h.Result) {
				h.Decode = g.jsonCrossing("export_argument", h.Result)
			}
			out = append(out, h)
		}
	}
	return out
}
