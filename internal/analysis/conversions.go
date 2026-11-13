package analysis

import "github.com/nomi-language/nomi/internal/ast"

// Conversion is a value whose type converts to the expected one below its
// top (FileAnalysis.Conversions): a `Maybe<List<Int>>` where a
// `Maybe<Display>` is expected, a `Box<Circle>` where a `Box<Shape>` is,
// a `() -> Int` where a `() -> Display` is (spec §5 *Covariant
// conversion*). The IR builder relabels the value where its
// representation already is the wider one, and otherwise rebuilds it with
// each part converted, boxing a part by the impl Boxes names
// (internal/irbuild, irembedsrebuild.go).
type Conversion struct {
	// From is the value's own type and To the expected one.
	From, To Type
	// Boxes is each part of From that enters an interface in To, with the
	// impl the checker selects for it, as a Coercion records a value
	// entering an interface at the top: a `List<Int>` element entering
	// Display in `Maybe<List<Int>>` to `Maybe<Display>`. A part of a
	// function's parameter enters the other way: the wrapper boxes the
	// argument it passes on.
	Boxes []*Coercion
}

// recordConversion records n, an expression of type t checked against the
// type exp that is no interface, when t converts to exp below its top.
func (c *checker) recordConversion(n ast.Node, exp, t Type) {
	var boxes []*Coercion
	if !conversionParts(exp, t, func(it *InterfaceType, v Type) {
		boxes = append(boxes, &Coercion{Iface: it, Value: v, Impl: c.coercionImpl(it, v)})
	}) {
		return
	}
	if c.fa.Conversions == nil {
		c.fa.Conversions = map[ast.Node]*Conversion{}
	}
	c.fa.Conversions[n] = &Conversion{From: t, To: exp, Boxes: boxes}
}

// conversionParts walks want and have, a value's expected and own types,
// by the conversion rule (spec §5 *Covariant conversion*), calls box with
// each part of have that enters an interface in want, and reports whether
// any part converts: enters an interface, an enum that embeds it, or an
// Iter that views it.
func conversionParts(want, have Type, box func(*InterfaceType, Type)) bool {
	return convParts(want, have, false, box)
}

// convParts is conversionParts with flipped set inside a contravariant
// position, where have is a part of the expected type. A type parameter
// there is the expected type's own, left uninstantiated (a generic
// struct's field type, as `Sink{take: f}` expects `(T) -> String`), and
// converts nothing.
func convParts(want, have Type, flipped bool, box func(*InterfaceType, Type)) bool {
	want, have = resolveTypeVar(want), resolveTypeVar(have)
	if want == nil || have == nil || want == have {
		return false
	}
	if isEmptyTuple(want) || isEmptyTuple(have) {
		return false
	}
	if it, ok := want.(*InterfaceType); ok {
		hi, haveIface := have.(*InterfaceType)
		if haveIface {
			changed := false
			if isStdIterType(it) && len(it.TypeArgs) == len(hi.TypeArgs) {
				for i := range it.TypeArgs {
					changed = convParts(it.TypeArgs[i], hi.TypeArgs[i], flipped, box) || changed
				}
			}
			return changed
		}
		switch have.(type) {
		case *TypeVar:
			return false
		case *TypeParam_:
			if flipped {
				return false
			}
		}
		if !isStdIterType(it) {
			box(it, have)
		}
		return true
	}
	if et, ok := want.(*EnumType); ok && isEmbeddedTypeOf(have, et) {
		return true
	}
	parts := func(ws, hs []Type) bool {
		if len(ws) != len(hs) {
			return false
		}
		changed := false
		for i := range ws {
			changed = convParts(ws[i], hs[i], flipped, box) || changed
		}
		return changed
	}
	switch w := want.(type) {
	case *ListType:
		if h, ok := have.(*ListType); ok {
			return convParts(w.Elem, h.Elem, flipped, box)
		}
	case *MapType:
		if h, ok := have.(*MapType); ok {
			return parts([]Type{w.Key, w.Val}, []Type{h.Key, h.Val})
		}
	case *TupleType:
		if h, ok := have.(*TupleType); ok {
			return parts(w.Elems, h.Elems)
		}
	case *AnonStructType:
		h, ok := have.(*AnonStructType)
		if !ok || len(w.Fields) != len(h.Fields) {
			return false
		}
		changed := false
		for _, f := range w.Fields {
			if hf, found := scopedField(h, f.Name); found {
				changed = convParts(f.Type, hf.Type, flipped, box) || changed
			}
		}
		return changed
	case *FuncType:
		h, ok := have.(*FuncType)
		if !ok || len(w.Params) != len(h.Params) {
			return false
		}
		changed := false
		for i := range w.Params {
			// The wrapper passes its own argument, of want's parameter
			// type, to have's function.
			changed = convParts(h.Params[i], w.Params[i], !flipped, box) || changed
		}
		return convParts(normalizeReturn(w.Return), normalizeReturn(h.Return), flipped, box) || changed
	case *StructType:
		if h, ok := have.(*StructType); ok && sameGenericDecl(w, h) {
			return argParts(w, w.TypeArgs, h.TypeArgs, flipped, box)
		}
	case *EnumType:
		if h, ok := have.(*EnumType); ok && sameGenericDecl(w, h) {
			return argParts(w, w.TypeArgs, h.TypeArgs, flipped, box)
		}
	case *DistinctType:
		if h, ok := have.(*DistinctType); ok && sameGenericDecl(w, h) {
			return argParts(w, w.TypeArgs, h.TypeArgs, flipped, box)
		}
	}
	return false
}

// argParts walks the type arguments of two instances of the generic type
// t by each parameter's variance: a contravariant one the other way.
func argParts(t Type, ws, hs []Type, flipped bool, box func(*InterfaceType, Type)) bool {
	if len(ws) != len(hs) {
		return false
	}
	changed := false
	for i := range ws {
		if argVariance(t, i) == VarianceContravariant {
			changed = convParts(hs[i], ws[i], !flipped, box) || changed
		} else {
			changed = convParts(ws[i], hs[i], flipped, box) || changed
		}
	}
	return changed
}
