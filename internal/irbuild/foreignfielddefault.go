package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A struct literal, or a record-variant literal, that omits a defaulted field
// of a type ANOTHER file declares: `M{name: "a"}` after `import lib.M`, where
// lib.nomi declares `drops: Maybe<Int> = None`.
//
// A field default is Nomi source belonging to the declaring file. It may name
// that file's private functions, onces and types the constructing file never
// imported (`hello: String = greeting`), and the checker's references for it
// are the declaring file's, so only the declaring file's gen can lower it.
// This is siblingdefault.go's rule for a defaulted `fn` parameter, applied to
// a field: the declaring gen builds one zero-parameter accessor per field
// default some literal omitted, answering the default at the field's kind, and
// the literal calls it.
//
// The constructing gen asks for an accessor while it lowers, and the declaring
// gen may already have finished its own walk, so requests are queued on the
// declaring gen and built by flushFieldDefaults: at the end of that gen's walk,
// or by irFlushLateInstances once every walk is done. A default that does not
// lower leaves no accessor, and a program that reaches one is reported
// BLOCKED under the accessor's name.
//
// A generic struct's instance has its own accessors, keyed by the declaring
// gen's instance def, so `extra: List<T> = []` is built at `List<Int>` for
// `Box<Int>` and at `List<String>` for `Box<String>`.

// irFieldDefaultFill identifies one accessor: the declaring gen's def, the
// record variant ("" for a struct) and the field.
type irFieldDefaultFill struct {
	def     *typeDef
	variant string
	field   string
}

// irFieldDefaultReq is one queued accessor.
type irFieldDefaultReq struct {
	sym   *ir.Symbol
	name  string
	deflt ast.Node
	// k is the field's kind in the declaring gen.
	k     kind
	built bool
}

// foreignFieldDefault fills one omitted field of the mirror d with a call to
// the declaring gen's accessor for it. variant is the record variant's name,
// "" for a struct; want is the field's kind here.
func (bl *irScalarBuilder) foreignFieldDefault(at ast.Node, d *typeDef, variant, field string, want kind) (ir.Temp, bool) {
	g := bl.g
	src := d.ownerDef
	if src == nil || g.reg == nil || !irCallOperandKind(want) {
		return ir.NoTemp, false
	}
	decl := src.decl
	if src.genericOf != nil && src.genericOf.decl != nil {
		decl = src.genericOf.decl
	}
	o := g.reg.byDecl[decl]
	if o == nil {
		return ir.NoTemp, false
	}
	owner := g.unitGen(o.unit)
	if owner == nil || owner == g {
		return ir.NoTemp, false
	}
	var deflt ast.Node
	var k kind
	if variant == "" {
		f := src.field(field)
		if f == nil {
			return ir.NoTemp, false
		}
		deflt, k = f.deflt, f.k
	} else {
		v := src.variant(variant)
		if v == nil {
			return ir.NoTemp, false
		}
		for i := range v.payloads {
			if v.payloads[i].nomi == field {
				deflt, k = v.payloads[i].deflt, v.payloads[i].k
			}
		}
	}
	if deflt == nil {
		return ir.NoTemp, false
	}
	sym := owner.irFieldDefaultSym(irFieldDefaultFill{def: src, variant: variant, field: field}, deflt, k)
	pos := g.irNodePos(at)
	c := ir.NewCall(pos, bl.f.NewTemp(), ir.OrdinaryCall, sym)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: want})
	cp := ir.NewCopy(pos, bl.f.NewTemp(), c.Dst())
	bl.b.Append(cp)
	bl.side(cp.Dst(), irScalarSide{k: want, copy: irCopyForce})
	return cp.Dst(), true
}

// irFieldDefaultSym is the accessor symbol for key, interned in g's table and
// queued for flushFieldDefaults the first time any file asks. g is the
// DECLARING gen.
func (g *gen) irFieldDefaultSym(key irFieldDefaultFill, deflt ast.Node, k kind) *ir.Symbol {
	if r := g.fieldDefaults[key]; r != nil {
		return r.sym
	}
	name := key.def.nomi + "."
	if key.variant != "" {
		name += key.variant + "."
	}
	name += key.field + " default"
	r := &irFieldDefaultReq{sym: g.irCalleeSym(key, name), name: name, deflt: deflt, k: k}
	if g.fieldDefaults == nil {
		g.fieldDefaults = map[irFieldDefaultFill]*irFieldDefaultReq{}
	}
	g.fieldDefaults[key] = r
	g.fieldDefaultQueue = append(g.fieldDefaultQueue, r)
	return r.sym
}

// flushFieldDefaults builds every queued accessor not yet built. Building one
// can queue another, which the index loop picks up.
func (g *gen) flushFieldDefaults() {
	for i := 0; i < len(g.fieldDefaultQueue); i++ {
		if r := g.fieldDefaultQueue[i]; !r.built {
			r.built = true
			g.irFieldDefaultRetain(r)
		}
	}
}

// irFieldDefaultRetain builds one accessor: no parameters, the default
// lowered as a same-file literal's omitted field is (declDefault), returned.
func (g *gen) irFieldDefaultRetain(r *irFieldDefaultReq) {
	g.irDeclineOpen(r.name)
	if !irCallOperandKind(r.k) {
		irDeclineNote("a field kind outside the call domain: " + r.k.nomi())
		return
	}
	sh := g.irFuncShellAt(r.deflt, nil, irFuncSig{result: r.k, name: r.name, origin: irFromModule}, nil, r.sym)
	if sh == nil {
		irDeclineNote("no function shell")
		return
	}
	bl := &irScalarBuilder{g: g, sh: sh, f: sh.fn, b: sh.entry, returnKind: r.k, sides: sh.sides,
		bound: map[string]ir.Temp{}, boundK: map[string]kind{}}
	v, ok := bl.declDefault(r.deflt, r.k)
	if !ok {
		irDeclineNote("a default that does not lower")
		return
	}
	at := g.irNodePos(r.deflt)
	cp := ir.NewCopy(at, sh.result, v)
	bl.b.Append(cp)
	bl.side(sh.result, irScalarSide{k: r.k})
	bl.b.SetTerm(ir.NewReturn(at, sh.result))
	if err := ir.Lint(sh.fn); err != nil {
		irDeclineNote("a field default accessor that does not lint: " + err.Error())
		return
	}
	g.irModule().AddFunc(sh.fn)
	if err := ir.LintModuleAdded(g.irModule()); err != nil {
		panic("irbuild: field default accessor: " + err.Error())
	}
}
