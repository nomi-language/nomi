package analysis

import "github.com/nomi-language/nomi/internal/ast"

// Conversion is a value whose type converts to the expected one below its
// top (FileAnalysis.Conversions): a `Maybe<List<Int>>` where a
// `Maybe<Display>` is expected, a `Box<Circle>` where a `Box<Shape>` is,
// a `() -> Int` where a `() -> Display` is (spec §5 *Covariant
// conversion*). The IR builder relabels the value where Cost says it
// costs nothing, and otherwise rebuilds it with each part converted,
// boxing a part by the impl Boxes names (internal/irbuild, irrelabel.go,
// irembedsrebuild.go).
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
	// Cost is what the conversion does at run time (conversionCost): the
	// IR builder relabels the value where it is ConversionFree and
	// rebuilds it where it is ConversionRebuilds, and the hover says
	// which (internal/hoverdoc, conversions.go).
	Cost ConversionCost
}

// ConversionCost is what a covariant conversion does at run time (spec §5
// *Covariant conversion*).
type ConversionCost uint8

const (
	// ConversionFree is a value that already is a value of the wider
	// type: each part that enters an interface is an Int, Float, Bool or
	// String, a declared struct, a declared enum with no `embeds` variant
	// or a declared non-generic distinct type (carriesOwnType), inside std
	// collections, tuples, records and functions only. The IR builder
	// relabels it.
	ConversionFree ConversionCost = iota
	// ConversionRebuilds is a value built again with each part converted:
	// a collection, a Maybe, Result or user enum, a tuple, record or
	// struct, or a function wrapped in one that converts its arguments
	// and result.
	ConversionRebuilds
	// ConversionPerInstance is a value whose cost its generic body's
	// instances decide: a part of a type parameter's type enters an
	// interface, free for an instance at Int and a rebuild for one at a
	// Maybe. The IR builder decides each instance from its kinds.
	ConversionPerInstance
)

// recordConversion records n, an expression of type t checked against the
// type exp that is no interface, when t converts to exp below its top.
func (c *checker) recordConversion(n ast.Node, exp, t Type) {
	var boxes []*Coercion
	if !conversionParts(exp, t, func(into, v Type) {
		boxes = append(boxes, c.coercionInto(into, v))
	}) {
		return
	}
	if c.fa.Conversions == nil {
		c.fa.Conversions = map[ast.Node]*Conversion{}
	}
	c.fa.Conversions[n] = &Conversion{From: t, To: exp, Boxes: boxes, Cost: conversionCost(exp, t, false)}
}

// conversionCost is what converting a value of type have to want costs
// (Conversion.Cost), walked as convParts walks them: a part that converts
// nothing is free, a part entering an interface is free when its value
// carries its own type (carriesOwnType), a std collection, tuple, record
// or function costs what its parts cost, and an instance of a generic
// type the program declares is rebuilt whenever a part converts, since
// its value names its type arguments (internal/irbuild, irrelabel.go).
func conversionCost(want, have Type, flipped bool) ConversionCost {
	want, have = resolveTypeVar(want), resolveTypeVar(have)
	if want == nil || have == nil || want == have || isEmptyTuple(want) || isEmptyTuple(have) {
		return ConversionFree
	}
	worst := ConversionFree
	add := func(c ConversionCost) { worst = worst.and(c) }
	if conj, ok := want.(*ConjunctionType); ok {
		switch have.(type) {
		case *InterfaceType, *ConjunctionType, *TypeVar:
			return ConversionFree
		case *TypeParam_:
			if flipped {
				return ConversionFree
			}
			return ConversionPerInstance
		}
		for _, m := range conj.Members {
			if !entersAsItself(m, have) {
				return ConversionRebuilds
			}
		}
		return ConversionFree
	}
	if _, ok := want.(*InterfaceType); ok {
		if _, haveConj := have.(*ConjunctionType); haveConj {
			// Widening drops members and moves nothing.
			return ConversionFree
		}
	}
	if it, ok := want.(*InterfaceType); ok {
		if hi, haveIface := have.(*InterfaceType); haveIface {
			if isStdIterType(it) && len(it.TypeArgs) == len(hi.TypeArgs) {
				for i := range it.TypeArgs {
					add(conversionCost(it.TypeArgs[i], hi.TypeArgs[i], flipped))
				}
			}
			return worst
		}
		switch have.(type) {
		case *TypeVar:
			return ConversionFree
		case *TypeParam_:
			if flipped {
				return ConversionFree
			}
			return ConversionPerInstance
		}
		if !isStdIterType(it) && entersAsItself(it, have) {
			return ConversionFree
		}
		return ConversionRebuilds
	}
	if et, ok := want.(*EnumType); ok && isEmbeddedTypeOf(have, et) {
		return ConversionRebuilds
	}
	parts := func(ws, hs []Type) {
		if len(ws) == len(hs) {
			for i := range ws {
				add(conversionCost(ws[i], hs[i], flipped))
			}
		}
	}
	switch w := want.(type) {
	case *ListType:
		if h, ok := have.(*ListType); ok {
			add(conversionCost(w.Elem, h.Elem, flipped))
		}
	case *MapType:
		if h, ok := have.(*MapType); ok {
			parts([]Type{w.Key, w.Val}, []Type{h.Key, h.Val})
		}
	case *TupleType:
		if h, ok := have.(*TupleType); ok {
			parts(w.Elems, h.Elems)
		}
	case *AnonStructType:
		if h, ok := have.(*AnonStructType); ok && len(w.Fields) == len(h.Fields) {
			for _, f := range w.Fields {
				if hf, found := scopedField(h, f.Name); found {
					add(conversionCost(f.Type, hf.Type, flipped))
				}
			}
		}
	case *FuncType:
		if h, ok := have.(*FuncType); ok && len(w.Params) == len(h.Params) {
			for i := range w.Params {
				add(conversionCost(h.Params[i], w.Params[i], !flipped))
			}
			add(conversionCost(normalizeReturn(w.Return), normalizeReturn(h.Return), flipped))
		}
	case *StructType:
		if h, ok := have.(*StructType); ok && sameGenericDecl(w, h) {
			add(argsCost(w, w.TypeArgs, h.TypeArgs, flipped))
		}
	case *EnumType:
		if h, ok := have.(*EnumType); ok && sameGenericDecl(w, h) {
			add(argsCost(w, w.TypeArgs, h.TypeArgs, flipped))
		}
	case *DistinctType:
		if h, ok := have.(*DistinctType); ok && sameGenericDecl(w, h) {
			add(argsCost(w, w.TypeArgs, h.TypeArgs, flipped))
		}
	}
	return worst
}

// argsCost is conversionCost over the type arguments of two instances of
// the generic type t: what its parts cost for a std collection (Maybe,
// Result, Set, Vector), and a rebuild for any other whose arguments
// convert at all.
func argsCost(t Type, ws, hs []Type, flipped bool) ConversionCost {
	if len(ws) != len(hs) {
		return ConversionFree
	}
	if !stdCollectionArgs(t) {
		if argParts(t, ws, hs, flipped, func(Type, Type) {}) {
			return ConversionRebuilds
		}
		return ConversionFree
	}
	worst := ConversionFree
	for i := range ws {
		c := conversionCost(ws[i], hs[i], flipped)
		if argVariance(t, i) == VarianceContravariant {
			c = conversionCost(hs[i], ws[i], !flipped)
		}
		worst = worst.and(c)
	}
	return worst
}

// and is the cost of a conversion with parts costing c and d: a rebuild
// when either part rebuilds, else per instance when either part's
// instances decide.
func (c ConversionCost) and(d ConversionCost) ConversionCost {
	switch {
	case c == ConversionRebuilds || d == ConversionRebuilds:
		return ConversionRebuilds
	case c == ConversionPerInstance || d == ConversionPerInstance:
		return ConversionPerInstance
	}
	return ConversionFree
}

// conversionParts walks want and have, a value's expected and own types,
// by the conversion rule (spec §5 *Covariant conversion*), calls box with
// each part of have that enters an interface in want, and reports whether
// any part converts: enters an interface, an enum that embeds it, or an
// Iter that views it.
func conversionParts(want, have Type, box func(Type, Type)) bool {
	return convParts(want, have, false, box)
}

// convParts is conversionParts with flipped set inside a contravariant
// position, where have is a part of the expected type. A type parameter
// there is the expected type's own, left uninstantiated (a generic
// struct's field type, as `Sink{take: f}` expects `(T) -> String`), and
// converts nothing.
func convParts(want, have Type, flipped bool, box func(Type, Type)) bool {
	want, have = resolveTypeVar(want), resolveTypeVar(have)
	if want == nil || have == nil || want == have {
		return false
	}
	if isEmptyTuple(want) || isEmptyTuple(have) {
		return false
	}
	if conj, ok := want.(*ConjunctionType); ok {
		// A value enters a conjunction as it enters each member, and
		// an interface value widening to fewer members is one already.
		switch have.(type) {
		case *InterfaceType, *ConjunctionType, *TypeVar:
			return false
		case *TypeParam_:
			if flipped {
				return false
			}
		}
		box(conj, have)
		return true
	}
	if _, ok := want.(*InterfaceType); ok {
		if _, haveConj := have.(*ConjunctionType); haveConj {
			return false
		}
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
func argParts(t Type, ws, hs []Type, flipped bool, box func(Type, Type)) bool {
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

// entersAsItself reports whether a value of type have, entering the
// interface it, is the interface value itself, with no box (irbuild's
// erasesUnboxed): a value that carries its own type (carriesOwnType); an
// anonymous record entering std's Struct; and, entering std's Display, a
// Decimal or Byte, whose text the machine renders itself, or a tuple or
// record whose every part the rendering reads off it (displayReadsOff). A
// std struct enters std's Display and Debug boxed.
func entersAsItself(it *InterfaceType, have Type) bool {
	have = resolveTV(have)
	if st, ok := have.(*StructType); ok && isStdOrigin(st.Origin) && (isStdInterfaceNamed(it, "Display") || isStdInterfaceNamed(it, "Debug")) {
		// A std struct's own Display may be a host function or a body the
		// program never lowers, so it enters std's Display and Debug boxed
		// by a thunk that calls it (irbuild's irStdNamedBoxPlan).
		return false
	}
	if carriesOwnType(have) {
		return true
	}
	if it.Name == "Struct" && isStdOrigin(it.Origin) {
		_, record := have.(*AnonStructType)
		return record
	}
	if !isStdInterfaceNamed(it, "Display") {
		return false
	}
	switch h := have.(type) {
	case *PrimitiveType:
		return samePrimitive(h, TypeDecimal) || samePrimitive(h, TypeByte)
	case *TupleType, *AnonStructType:
		return displayReadsOff(it, h)
	}
	return false
}

// displayReadsOff reports whether a Display value holding a value of type
// t renders it from the value alone: a value that enters Display as
// itself, a Display value, a List, Vector, Map, tuple or record of such
// parts, rendered as its shape around them, a Maybe, Result or Set,
// rendered by std's impl at its own type, or a declared non-generic enum,
// by its own impl (irbuild's irErases).
func displayReadsOff(display *InterfaceType, t Type) bool {
	t = resolveTV(t)
	if it, ok := t.(*InterfaceType); ok {
		return isStdInterfaceNamed(it, "Display")
	}
	all := func(ts ...Type) bool {
		for _, p := range ts {
			if !displayReadsOff(display, p) {
				return false
			}
		}
		return true
	}
	switch h := t.(type) {
	case *ListType:
		return all(h.Elem)
	case *MapType:
		return all(h.Key, h.Val)
	case *TupleType:
		return all(h.Elems...)
	case *AnonStructType:
		for _, f := range h.Fields {
			if !all(f.Type) {
				return false
			}
		}
		return true
	case *DistinctType:
		if h.Name == "Vector" && isStdOrigin(h.Origin) && len(h.TypeArgs) == 1 {
			return all(h.TypeArgs[0])
		}
	case *EnumType:
		// A declared enum's own `impl Display`, `embeds` variants and all.
		if !isStdOrigin(h.Origin) && len(h.TypeArgs) == 0 {
			return true
		}
	}
	if stdCollectionArgs(t) {
		return true
	}
	return entersAsItself(display, t)
}
