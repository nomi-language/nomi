package irbuild

// `Context.with_value(c, v)`, `Context.value(c, T)` and the `Type<T>` witness.
//
// std declares `with_value<T>(c: Context, value: T): Context` and
// `value<T>(c: Context, value_type: Type<T>): Option<T>`. The store is keyed by
// a type's IDENTITY, and rt.ContextWithValue says whose that is: the static
// type of the argument, which only the call site knows. So both calls cross
// to VM intrinsics (`Context.with_value`, `Context.value`) with a
// `Type<T>` witness operand, an `ir.RefTypeWitness` naming T's declaration
// symbol, whose name is the qualified runtime name every value of T carries
// (`domain.Token`). The machine interns one `*rt.TypeID` per name, so the
// witness written in the entry file and the one in the declaring module name
// one slot, and two same-named types in two modules name two.
//
// A type name in value position is a witness only where a `Type<T>` is
// expected: an argument to a `Type<T>` parameter (`lookup_marker(c, Marker)`,
// through lowerTypedOperand) or Context.value's second operand. Everywhere
// else `Marker` is the marker's one value. The parameter's type decides.
//
// The witnessed types are the ones whose identity is a single name: a
// scalar, or a non-generic struct, enum or distinct. A generic instance
// (`Option<Int>`) declines, because its runtime name drops the arguments.

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// typeWitnessArg is the witnessed kind X of a `Type<X>` kind.
func typeWitnessArg(k kind) (kind, bool) {
	spec, args, ok := genHostOf(k)
	if !ok || spec.origin != "std/type" || spec.nomi != "Type" || len(args) != 1 {
		return kindInvalid, false
	}
	return args[0], true
}

// irWitnessableKind reports whether a `Type<X>` witness for x has a
// single-name identity the VM can key the store by.
func irWitnessableKind(x kind) bool {
	switch x.tag {
	case tagInt, tagFloat, tagString, tagBool:
		return true
	case tagNamed:
		d := x.def
		if d == nil || d.preludeOf != nil || d.genericOf != nil || d.genHostOf != nil {
			return false
		}
		if d.rtOpaque {
			return irStdLeafIdentity(d)
		}
		return irRetainedStructKind(d) || irRetainedEnumKind(d) || d.isDistinct
	}
	return false
}

// irStdLeafIdentity reports whether d is one of std's process-wide types
// with an rt Go type, an opaque one (`Duration`, `Codepoint`) or a host
// type with a declaring module (`Regex`): its symbol's name is its
// declaring module's qualified spelling in every gen (irTypeSymName), so
// a witness for it names one slot wherever it is written.
func irStdLeafIdentity(d *typeDef) bool {
	for _, od := range opaqueDefs() {
		if od == d {
			return true
		}
	}
	for i, hd := range stdHostDefs() {
		if hd == d && stdHostSpecs[i].origin != "" {
			return true
		}
	}
	return false
}

// irTypeWitnessKind reports a retained `Type<X>` kind.
func irTypeWitnessKind(k kind) bool {
	x, ok := typeWitnessArg(k)
	return ok && irWitnessableKind(x)
}

// witnessSym is the declaration symbol a witness for x names.
func (g *gen) witnessSym(x kind) *ir.Symbol {
	if x.tag == tagNamed {
		return g.irTypeSym(x.def)
	}
	return g.irCalleeSym(witnessToken{x.nomi()}, x.nomi())
}

// witnessToken interns a primitive's witness symbol once per module.
type witnessToken struct{ name string }

// typeWitnessValue lowers the type name at as the `Type<x>` witness want.
// ok is false when at is not a type name naming x, and the caller lowers
// at another way.
func (bl *irScalarBuilder) typeWitnessValue(at ast.Node, x, want kind) (ir.Temp, bool) {
	if !irWitnessableKind(x) || !bl.namesType(at, x) {
		return ir.NoTemp, false
	}
	return bl.witnessRef(at, x, want), true
}

// witnessRef builds the `Type<x>` witness want at at.
func (bl *irScalarBuilder) witnessRef(at ast.Node, x, want kind) ir.Temp {
	r := ir.NewRefTypeWitness(bl.g.irNodePos(at), bl.f.NewTemp(), bl.g.witnessSym(x))
	bl.b.Append(r)
	bl.side(r.Dst(), irScalarSide{k: want})
	return r.Dst()
}

// namesType reports whether at is a type name, not a binding, that resolves to
// x.
func (bl *irScalarBuilder) namesType(at ast.Node, x kind) bool {
	k, ok := bl.witnessTypeKind(at)
	return ok && k == x
}

// witnessTypeKind is the X of the `Type<X>` the checker records for at, a
// type name in value position, bare (`Token`) or through a whole-file
// import's qualifier (`domain.Token`). ok is false when at is not a type
// name: a binding, or a field chain over one. A type name whose record is
// no witness the builder represents answers kindInvalid.
func (bl *irScalarBuilder) witnessTypeKind(at ast.Node) (kind, bool) {
	switch t := at.(type) {
	case *ast.TypeIdent:
	case *ast.Ident:
		if bl.namesLocal(t) {
			return kindInvalid, false
		}
	case *ast.FieldAccess:
		root := t.Object
		for fa, chained := root.(*ast.FieldAccess); chained; fa, chained = root.(*ast.FieldAccess) {
			root = fa.Object
		}
		if id, isIdent := root.(*ast.Ident); !isIdent || bl.namesLocal(id) {
			return kindInvalid, false
		}
		if !bl.dottedTypeName(t) {
			return kindInvalid, false
		}
	default:
		return kindInvalid, false
	}
	k, _ := bl.g.recordedKind(at)
	x, _ := typeWitnessArg(k)
	return x, true
}

// contextValueCall lowers `Context.with_value(c, v)` and
// `Context.value(c, T)`.
func (bl *irScalarBuilder) contextValueCall(t *ast.Call, method string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	// A turbofish (`Context.value<TraceId>(c, TraceId)`) names the type
	// the witness argument already does.
	if len(t.Args) != 2 {
		return no()
	}
	if namedArgNode(t.Args) != nil {
		// call sends a call with a named argument to namedIntrinsicCall,
		// which comes back with the positional form.
		panic("irbuild: contextValueCall reached with a named argument: Context." + method)
	}
	a := irQualArgs{temps: make([]ir.Temp, 2), kinds: make([]kind, 2), mobile: make([]bool, 2), ok: true}
	c, ck, _, ok := bl.lower(t.Args[0])
	if !ok || !irContextKind(ck) {
		return no()
	}
	a.temps[0], a.kinds[0], a.mobile[0] = c, ck, true
	var result kind
	operands := []ir.Temp{c}
	switch method {
	case "with_value":
		v, vk, vmobile, ok := bl.lower(t.Args[1])
		if !ok || !irWitnessableKind(vk) {
			bl.g.dc.note("a Context.with_value whose value's type has no single-name identity")
			return no()
		}
		want, ok := bl.g.genHostOfType("std/type", "Type", []kind{vk})
		if !ok {
			return no()
		}
		w := ir.NewRefTypeWitness(bl.g.irNodePos(t.Args[1]), bl.f.NewTemp(), bl.g.witnessSym(vk))
		bl.b.Append(w)
		bl.side(w.Dst(), irScalarSide{k: want})
		a.temps[1], a.kinds[1], a.mobile[1] = v, vk, vmobile
		operands = append(operands, v, w.Dst())
		result = ck
	case "value":
		arg := t.Args[1]
		var w ir.Temp
		var wk, x kind
		typeName := false
		if id, isIdent := arg.(*ast.Ident); !isIdent || !bl.namesLocal(id) {
			x, typeName = bl.witnessTypeKind(arg)
		}
		if !typeName {
			// A `Type<T>` value is already a witness: a parameter or
			// binding, a field (`keys.trace`), a call's result.
			v, k, _, ok := bl.lower(arg)
			if !ok || !irTypeWitnessKind(k) {
				return no()
			}
			w, wk = v, k
			x, _ = typeWitnessArg(k)
		} else {
			// kindInvalid: lookup — a name that is not a type declines.
			if x == kindInvalid || !irWitnessableKind(x) {
				bl.g.dc.note("a Context.value witness whose type has no single-name identity")
				return no()
			}
			if wk, ok = bl.g.genHostOfType("std/type", "Type", []kind{x}); !ok {
				return no()
			}
			// witnessTypeKind named arg a type name the checker typed Type<x>.
			w = bl.witnessRef(arg, x, wk)
		}
		a.temps[1], a.kinds[1], a.mobile[1] = w, wk, true
		operands = append(operands, w)
		result = bl.g.preludeInstanceOf(preludeSpecFor("std/option", "Option"), []kind{x})
		// kindInvalid: propagates — preludeInstanceOf answers it for an argument it rejected.
		if result == kindInvalid || !irRetainedValueKind(result) {
			return no()
		}
	default:
		panic("irbuild: contextValueCall: reached for Context." + method + ", which is neither with_value nor value")
	}
	if bl.qualRecord == t {
		bl.qualRecordArgs, bl.qualRecordSeen = a, true
	}
	name := "Context." + method
	call := ir.NewHostCall(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), irCallSite(t),
		bl.g.irCalleeSym(name, name), operands...)
	bl.b.Append(call)
	bl.side(call.Dst(), irScalarSide{k: result, deferrable: true})
	return call.Dst(), result, false, true
}
