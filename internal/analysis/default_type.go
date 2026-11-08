package analysis

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"
)

// Types from defaults (spec §4 *Structs*, §7 *Default Arguments*). A
// struct or record-variant field, or a function parameter, with a default
// may leave out its type: `misses = 0`, `fn search(query: String, limit =
// 10)`. Its type is the default's, checked with no expected type, so a
// default whose type only an expected type tells (`[]`, `%[]`, `None`, a
// `.Variant`) is an error at the field or parameter, with help that writes
// the annotation.
//
// The type is part of the file's interface: another file constructs the
// struct and calls the function. So, as for an unannotated `once`
// (OnceTable), every one is inferred in one phase before any file's
// CheckTypes runs, in a fixed order, each with a checker for its own file.
// Until then the field's FieldDef or the parameter's FuncType slot holds
// pendingDefault, which the phase overwrites in place; an instance of a
// generic struct shares its declaration's Fields, so it sees the type too.
// A default that reads a type still pending is tried again after the
// others; one still pending when no round makes progress depends on its
// own type, and stays pendingDefault. The declaring file's CheckTypes
// reports what the phase found (checkInferredDefault).
//
// A block-local struct is not in the phase: it is inferred when its block
// is checked, before anything after it in the block reads it.

// pendingDefault is the type of a field or parameter whose type comes from
// its default until the inference phase gives it one, and after, when its
// default does not tell it.
var pendingDefault Type = &StructType{Origin: "\x00type from its default", Name: "_"}

// isPendingDefault reports whether t is pendingDefault.
func isPendingDefault(t Type) bool {
	return t == pendingDefault
}

// containsPendingDefault reports whether t is or holds pendingDefault.
func containsPendingDefault(t Type) bool {
	return containsPendingDefaultDepth(t, 0)
}

func containsPendingDefaultDepth(t Type, depth int) bool {
	t = resolveTypeVar(t)
	if t == nil || isNilType(t) || depth > maxTypeRenderDepth {
		return false
	}
	if isPendingDefault(t) {
		return true
	}
	for _, c := range typeChildren(t) {
		if containsPendingDefaultDepth(c, depth+1) {
			return true
		}
	}
	return false
}

// defaultDetermines reports whether t, the type of a default checked with
// no expected type, is one a field or parameter can take: every part known.
func defaultDetermines(t Type) bool {
	return t != nil && !isNilType(t) && !containsTypeVar(t) && !containsPendingDefault(t)
}

// defaultSite is one field or parameter whose type comes from its default.
type defaultSite struct {
	fa    *FileAnalysis
	nodes []ast.Node
	// owner is the receiver base name of the impl block holding a method,
	// or "" elsewhere.
	owner string
	// what is "field" or "parameter"; name is its name; exitless says
	// where a `?` in the default would have nowhere to return to.
	what, name string
	exitless   *exitlessSite
	value      ast.Node
	line, col  int
	// set writes the inferred type where every reader finds it.
	set func(Type)
}

// IndexDefaults records every top-level field and parameter of one file
// whose type comes from its default. The file's types and signatures are
// built.
func (t *OnceTable) IndexDefaults(fa *FileAnalysis, nodes []ast.Node) {
	for _, node := range nodes {
		owner := ""
		switch n := node.(type) {
		case *ast.ImplBlock:
			owner = TypeExprBaseName(n.Receiver)
		case *ast.StructDef:
			owner = n.Name
		case *ast.EnumDef:
			owner = n.Name
		case *ast.TypeDef:
			owner = n.Name
		case *ast.ExternType:
			owner = n.Name
		}
		for _, s := range defaultSitesOf(fa, node) {
			s.nodes, s.owner = nodes, owner
			t.defaults = append(t.defaults, s)
		}
	}
}

// defaultSitesOf answers the sites one top-level node declares.
func defaultSitesOf(fa *FileAnalysis, node ast.Node) []*defaultSite {
	var out []*defaultSite
	switch n := node.(type) {
	case *ast.StructDef:
		out = append(out, structDefaultSites(fa, n)...)
		out = append(out, itemParamSites(fa, n.Items)...)
	case *ast.EnumDef:
		out = append(out, enumDefaultSites(fa, n)...)
		out = append(out, itemParamSites(fa, n.Items)...)
	case *ast.TypeDef:
		out = append(out, itemParamSites(fa, n.Items)...)
	case *ast.ExternType:
		out = append(out, itemParamSites(fa, n.Items)...)
	case *ast.ImplBlock:
		out = append(out, itemParamSites(fa, n.Items)...)
	case *ast.FuncDef, *ast.ExternFunc:
		out = append(out, itemParamSites(fa, []ast.Node{n})...)
	case *ast.InterfaceDef:
		out = append(out, interfaceParamSites(fa, n)...)
	}
	return out
}

// itemParamSites answers the parameters of the functions among items
// whose type comes from a default.
func itemParamSites(fa *FileAnalysis, items []ast.Node) []*defaultSite {
	var out []*defaultSite
	for _, item := range items {
		var params []ast.Param
		var line, col int
		switch fn := item.(type) {
		case *ast.FuncDef:
			params, line, col = fn.Params, fn.Line, fn.Col
		case *ast.ExternFunc:
			params, line, col = fn.Params, fn.Line, fn.Col
		default:
			continue
		}
		for i, p := range params {
			if !paramTypeFromDefault(p) {
				continue
			}
			out = append(out, &defaultSite{
				fa: fa, what: "parameter", name: p.Name,
				exitless: paramDefaultExitless(p),
				value:    p.Default, line: p.Line, col: p.Col,
				set: func(t Type) { setFuncParamType(fa, line, col, i, p, t) },
			})
		}
	}
	return out
}

// interfaceParamSites answers the parameters of n's default functions
// whose type comes from a default.
func interfaceParamSites(fa *FileAnalysis, n *ast.InterfaceDef) []*defaultSite {
	var out []*defaultSite
	for _, m := range n.Methods {
		for i, p := range m.Params {
			if !paramTypeFromDefault(p) {
				continue
			}
			out = append(out, &defaultSite{
				fa: fa, what: "parameter", name: p.Name,
				exitless: paramDefaultExitless(p),
				value:    p.Default, line: p.Line, col: p.Col,
				set: func(t Type) {
					if it, ok := declaredTypeOf(fa, Pos{Line: n.Line, Col: n.Col}, n.Name).(*InterfaceType); ok && it != nil {
						for k := range it.Methods {
							if it.Methods[k].Name == m.Name && i < len(it.Methods[k].Params) {
								it.Methods[k].Params[i] = t
							}
						}
					}
					attachInferredParam(fa, p, t)
				},
			})
		}
	}
	return out
}

// paramTypeFromDefault reports whether p is a parameter written with a
// default and no type, `limit = 10`.
func paramTypeFromDefault(p ast.Param) bool {
	return p.TypeAnnotation == nil && p.Default != nil && p.Destructure == nil && !p.TypeOnly
}

// setFuncParamType gives parameter i of the function declared at line:col
// the type t: on the function's FuncType and on the parameter's symbol.
func setFuncParamType(fa *FileAnalysis, line, col, i int, p ast.Param, t Type) {
	if sym := fa.Definitions[Pos{Line: line, Col: col}]; sym != nil {
		if ft, ok := sym.Type.(*FuncType); ok && i < len(ft.Params) {
			ft.Params[i] = t
		}
	}
	attachInferredParam(fa, p, t)
}

// attachInferredParam puts t on parameter p's symbol, which hover and the
// IR builder read. A parameter's symbol is defined where its function's
// body is walked, so walking the body again (rebuildBodies, and std's
// bodies grafted into its declarations) defines a new one: the type is
// kept with the declarations' others, which a rebuild writes again.
func attachInferredParam(fa *FileAnalysis, p ast.Param, t Type) {
	pos := Pos{Line: p.Line, Col: p.Col}
	sym, ok := fa.Definitions[pos]
	if !ok || sym.Kind != SymbolParam || sym.Name != p.Name {
		return
	}
	sym.Type = t
	if k := fa.kept; k != nil && k.definitions[pos] != sym {
		k.typed = append(k.typed, keptType{pos, sym.Name, sym.Kind, t})
	}
}

// structDefaultSites answers n's fields whose type comes from a default.
func structDefaultSites(fa *FileAnalysis, n *ast.StructDef) []*defaultSite {
	var out []*defaultSite
	for _, f := range n.Fields {
		if f.TypeAnnotation != nil || f.Default == nil {
			continue
		}
		out = append(out, &defaultSite{
			fa: fa, what: "field", name: f.Name,
			exitless: fieldDefaultExitless(f.Name, n.Name),
			value:    f.Default, line: f.Line, col: f.Col,
			set: func(t Type) { setStructFieldType(fa, n, f, t) },
		})
	}
	return out
}

// enumDefaultSites answers n's record-variant fields whose type comes from
// a default.
func enumDefaultSites(fa *FileAnalysis, n *ast.EnumDef) []*defaultSite {
	var out []*defaultSite
	for _, v := range n.Variants {
		for _, f := range v.Fields {
			if f.TypeAnnotation != nil || f.Default == nil {
				continue
			}
			out = append(out, &defaultSite{
				fa: fa, what: "field", name: f.Name,
				exitless: fieldDefaultExitless(f.Name, n.Name+"."+v.Name),
				value:    f.Default, line: f.Line, col: f.Col,
				set: func(t Type) { setVariantFieldType(fa, n, v.Name, f, t) },
			})
		}
	}
	return out
}

// setStructFieldType gives struct n's field f the type t: on the struct's
// FieldDef, which its instances share, and on the field's symbol.
func setStructFieldType(fa *FileAnalysis, n *ast.StructDef, f ast.StructField, t Type) {
	st, ok := declaredTypeOf(fa, Pos{Line: n.Line, Col: n.Col}, n.Name).(*StructType)
	if !ok || st == nil {
		return
	}
	setFieldDef(st.Fields, f.Name, t)
	attachInferredField(fa, f, t)
}

// setVariantFieldType is setStructFieldType for a record variant's field.
func setVariantFieldType(fa *FileAnalysis, n *ast.EnumDef, variant string, f ast.StructField, t Type) {
	et, ok := declaredTypeOf(fa, Pos{Line: n.Line, Col: n.Col}, n.Name).(*EnumType)
	if !ok || et == nil {
		return
	}
	for i := range et.Variants {
		if et.Variants[i].Name == variant {
			setFieldDef(et.Variants[i].Fields, f.Name, t)
		}
	}
	attachInferredField(fa, f, t)
}

func setFieldDef(fields []FieldDef, name string, t Type) {
	for i := range fields {
		if fields[i].Name == name {
			fields[i].Type = t
		}
	}
}

// attachInferredField puts t on the field's own symbol, which hover reads.
func attachInferredField(fa *FileAnalysis, f ast.StructField, t Type) {
	if sym := fa.Definitions[Pos{Line: f.Line, Col: f.Col}]; sym != nil && sym.Kind == SymbolField && sym.Name == f.Name {
		sym.Type = t
	}
}

// declaredTypeOf is the type a declaration's symbol carries, found by its
// position and else by name.
func declaredTypeOf(fa *FileAnalysis, pos Pos, name string) Type {
	sym := fa.Definitions[pos]
	if sym == nil && fa.ModuleScope != nil {
		sym = fa.ModuleScope.Lookup(name)
	}
	if sym == nil {
		return nil
	}
	if sym.Resolved != nil {
		sym = sym.Resolved
	}
	return sym.Type
}

// inferDefaults gives every indexed site its type, in the order of its
// file's Origin and path and then its position, in rounds: a default whose
// type still holds a pending one is tried again in the next round, while a
// round gives some site a type.
func (t *OnceTable) inferDefaults() {
	pending := slices.Clone(t.defaults)
	slices.SortStableFunc(pending, func(a, b *defaultSite) int {
		return cmp.Or(
			cmp.Compare(a.fa.Origin, b.fa.Origin),
			cmp.Compare(a.fa.FilePath, b.fa.FilePath),
			cmp.Compare(a.line, b.line),
			cmp.Compare(a.col, b.col),
		)
	})
	for len(pending) > 0 {
		var again []*defaultSite
		for _, s := range pending {
			sub := newChecker(s.fa, s.nodes)
			sub.selfTypeName = s.owner
			got := sub.checkDefaultAlone(s.exitless, s.value)
			switch {
			case containsPendingDefault(got):
				again = append(again, s)
			case defaultDetermines(got):
				s.set(got)
			}
		}
		if len(again) == len(pending) {
			return
		}
		pending = again
	}
}

// checkDefaultAlone checks a default with no expected type, as the value
// that gives a field or parameter its type.
func (c *checker) checkDefaultAlone(site *exitlessSite, value ast.Node) Type {
	return c.checkExitless(site, func() Type { return c.checkNode(value) })
}

// checkInferredDefault checks the default of a field or parameter that has
// no type, in the declaring file's CheckTypes: declared is what the
// inference phase gave it, pendingDefault when it gave none. A block-local
// declaration, which the phase does not see, is inferred here, and set
// writes its type.
func (c *checker) checkInferredDefault(site *exitlessSite, what, name string, value ast.Node, declared Type, line, col int, set func(Type)) {
	mark := len(c.errors)
	got := c.checkDefaultAlone(site, value)
	if !isPendingDefault(declared) {
		return
	}
	if defaultDetermines(got) {
		if set != nil {
			set(got)
		}
		return
	}
	// What the default's own check said about the missing expected type
	// (`.Active requires a determinable enum type`) is this error's to say.
	c.errors = c.errors[:mark]
	c.reportUndeterminedDefault(what, name, value, got, line, col)
}

// reportUndeterminedDefault is the error at a field or parameter whose
// default does not tell its type, with help that writes the annotation.
func (c *checker) reportUndeterminedDefault(what, name string, value ast.Node, got Type, line, col int) {
	text := format.RenderNode(value)
	if containsPendingDefault(got) {
		c.report(TypeError{Line: line, Col: col, Message: fmt.Sprintf(
			"can't tell this %s's type from `%s`: it depends on a type that comes from this default", what, text)}.
			WithHint(fmt.Sprintf("write its type: `%s: T = %s`", name, text)))
		return
	}
	spelled := "T"
	if got != nil && !isNilType(got) {
		if s, ok := c.spellType(got, 0); ok {
			spelled = s
		} else if containsTypeVar(got) {
			spelled = renderNamingVars(got, nil)
		}
	}
	hint := fmt.Sprintf("write `%s: %s = %s`", name, spelled, text)
	switch {
	case spelled == "T":
		hint += ", with its type for `T`"
	case containsTypeVar(got):
		hint += ", with the type for each `_`"
	}
	c.report(TypeError{Line: line, Col: col, Message: fmt.Sprintf("can't tell this %s's type from `%s`", what, text)}.WithHint(hint))
}
