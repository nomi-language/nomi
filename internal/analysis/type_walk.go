package analysis

// The structure of a type is read in one place: typeChildren says which
// types a type is built from, and withTypeChildren builds the same type
// from others. walkType, typeHolds and mapType are the recursions over
// them, so a walk that looks for something inside a type, or rebuilds a
// type with part of it replaced, names only the kinds it treats specially
// and reaches every other kind through these. A new kind of Type gets a
// case in typeChildren and withTypeChildren
// (TestTypeChildrenNamesEveryKind fails until it has both), and every
// walk covers it. pairedTypeChildren is the same for two types walked in
// step: the parts of each, position by position, when the two have the
// same shape (sameTypeShape, which the test holds to every kind too).
//
// The children are the types a type expression writes: an element, a key
// and value, tuple elements, anonymous struct fields, parameters and a
// return, type arguments, a conjunction's members, a Partial's target, and
// a solved inference variable's solution. A declared type's own structure
// (a struct's fields, an enum's variants, a distinct type's Inner and
// MoreParents, an interface's methods) is not among them: it is written
// once in the declaration, a recursive declaration reaches itself through
// it, and a walk that needs it reads it by name where it does.

// maxTypeWalkDepth bounds a walk over a type. A type the checker builds is
// finite; the bound holds a walk over one that a defect made cyclic.
const maxTypeWalkDepth = 64

// typeChildren is the types t is built from, one level down, in the order
// t is written. An entry is nil where t leaves the position out (a
// function type with no return).
func typeChildren(t Type) []Type {
	return typeChildrenIn(t, nil)
}

// typeChildrenIn is typeChildren, built in buf's storage where t does not
// already hold its children as a slice, so a walk allocates nothing for a
// list, a map or a function of a few parameters.
func typeChildrenIn(t Type, buf []Type) []Type {
	buf = buf[:0]
	switch tt := t.(type) {
	case nil:
		return nil
	case *PrimitiveType, *TypeParam_:
		return nil
	case *AliasTemplate:
		// A generic alias is a registry entry, never the type of a value:
		// a use is its Target instantiated (instantiateAlias).
		return nil
	case *TypeVar:
		if tt.Resolved == nil {
			return nil
		}
		return append(buf, tt.Resolved)
	case *ListType:
		return append(buf, tt.Elem)
	case *MapType:
		return append(buf, tt.Key, tt.Val)
	case *TupleType:
		return tt.Elems
	case *EnumType:
		return tt.TypeArgs
	case *StructType:
		return tt.TypeArgs
	case *InterfaceType:
		return tt.TypeArgs
	case *DistinctType:
		return tt.TypeArgs
	case *ConjunctionType:
		for _, m := range tt.Members {
			if m == nil {
				// A nil member stays a nil Type, not a typed nil.
				buf = append(buf, nil)
				continue
			}
			buf = append(buf, m)
		}
		return buf
	case *PartialType:
		return append(buf, tt.Inner)
	case *AnonStructType:
		for _, f := range tt.Fields {
			buf = append(buf, f.Type)
		}
		return buf
	case *FuncType:
		return append(append(buf, tt.Params...), tt.Return)
	}
	return nil
}

// withTypeChildren is t built from kids in place of typeChildren(t), which
// kids matches in length and order. A solved variable becomes the type
// that replaces its solution. A conjunction whose members would no longer
// all be interfaces is returned as it is.
func withTypeChildren(t Type, kids []Type) Type {
	switch tt := t.(type) {
	case nil:
		return nil
	case *PrimitiveType, *TypeParam_, *AliasTemplate:
		return t
	case *TypeVar:
		if tt.Resolved == nil {
			return t
		}
		return kids[0]
	case *ListType:
		return &ListType{Elem: kids[0]}
	case *MapType:
		return &MapType{Key: kids[0], Val: kids[1]}
	case *TupleType:
		return &TupleType{Elems: kids}
	case *EnumType:
		c := *tt
		c.TypeArgs = kids
		return &c
	case *StructType:
		c := *tt
		c.TypeArgs = kids
		return &c
	case *InterfaceType:
		c := *tt
		c.TypeArgs = kids
		return &c
	case *DistinctType:
		c := *tt
		c.TypeArgs = kids
		return &c
	case *ConjunctionType:
		members := make([]*InterfaceType, len(kids))
		for i, k := range kids {
			m, ok := k.(*InterfaceType)
			if !ok || m == nil {
				return t
			}
			members[i] = m
		}
		return NewConjunction(members)
	case *PartialType:
		return &PartialType{Inner: kids[0]}
	case *AnonStructType:
		fields := make([]FieldDef, len(tt.Fields))
		copy(fields, tt.Fields)
		for i := range fields {
			fields[i].Type = kids[i]
		}
		return &AnonStructType{Fields: fields}
	case *FuncType:
		c := *tt
		n := len(tt.Params)
		c.Params, c.Return = kids[:n:n], kids[n]
		return &c
	}
	return t
}

// walkType calls visit on t and then on each type inside it
// (typeChildren), a type before its children. A visit that answers false
// keeps the walk out of that type's children. A solved inference variable
// is visited and then its solution, so a visit that looks for an unsolved
// variable checks Resolved, and one that must not look through a solved
// variable answers false for it.
func walkType(t Type, visit func(Type) bool) {
	walkTypeDepth(t, visit, 0)
}

func walkTypeDepth(t Type, visit func(Type) bool, depth int) {
	if t == nil || depth > maxTypeWalkDepth || !visit(t) {
		return
	}
	var buf [4]Type
	for _, c := range typeChildrenIn(t, buf[:]) {
		walkTypeDepth(c, visit, depth+1)
	}
}

// typeHolds reports whether is answers true for t or a type inside it,
// through solved inference variables.
func typeHolds(t Type, is func(Type) bool) bool {
	found := false
	walkType(t, func(p Type) bool {
		found = found || is(p)
		return !found
	})
	return found
}

// typeHoldsOutsideVars is typeHolds for a question about a type as it is
// written: an inference variable is a leaf, solved or not, and what a
// solved one holds is not looked at.
func typeHoldsOutsideVars(t Type, is func(Type) bool) bool {
	found := false
	walkType(t, func(p Type) bool {
		if _, isVar := p.(*TypeVar); isVar {
			return false
		}
		found = found || is(p)
		return !found
	})
	return found
}

// mapType is t with f applied to each type it is built from, one level
// down (typeChildren). It is t itself when f changed none of them, so
// parts with nothing to replace are shared. A mapping at every depth is an
// f that calls mapType on what it is given.
func mapType(t Type, f func(Type) Type) Type {
	kids := typeChildren(t)
	var out []Type
	for i, k := range kids {
		if k == nil {
			continue
		}
		m := f(k)
		if m != k && out == nil {
			out = append([]Type(nil), kids...)
		}
		if out != nil {
			out[i] = m
		}
	}
	if out == nil {
		return t
	}
	return withTypeChildren(t, out)
}

// ReplaceTypeParams is t with each type parameter in it replaced by what by
// answers for it, at every depth (mapType), and whether by answered for
// every one. A type that holds no type parameter is t itself. The IR
// builder reads an instance's type arguments through it: a call inside a
// generic body records its callee's arguments in the body's own type
// parameters, which the enclosing instance's arguments replace. The
// `self` of an interface default is one of these parameters in either
// form the checker records it: the interface's SelfParam (a call's For)
// and the interface type marked DefaultSelf (the type of a `self` value,
// so of an expression, a call's type argument or a rendering). Each is
// asked of by as SelfParam.
func ReplaceTypeParams(t Type, by func(*TypeParam_) (Type, bool)) (Type, bool) {
	all := true
	var replace func(Type, int) Type
	replace = func(p Type, depth int) Type {
		if it, ok := p.(*InterfaceType); ok && it.DefaultSelf && it.SelfParam != nil {
			p = it.SelfParam
		}
		if tp, ok := p.(*TypeParam_); ok {
			r, found := by(tp)
			if !found {
				all = false
				return p
			}
			return r
		}
		if depth > maxTypeWalkDepth {
			return p
		}
		return mapType(p, func(c Type) Type { return replace(c, depth+1) })
	}
	if t == nil {
		return nil, true
	}
	return replace(t, 0), all
}

// sameTypeShape reports whether a and b, with solved variables followed,
// are built the same way one level down: the same kind, and for a declared
// type the same declaration, for a tuple, a function, a conjunction or a
// generic instance as many parts, for an anonymous struct the same field
// names. Two types with no parts have the same shape when they are the
// same type: one primitive, one type parameter, one unsolved variable.
// What the parts are is not compared.
func sameTypeShape(a, b Type) bool {
	a, b = resolveTypeVar(a), resolveTypeVar(b)
	switch at := a.(type) {
	case nil:
		return false
	case *PrimitiveType:
		bt, ok := b.(*PrimitiveType)
		return ok && samePrimitive(at, bt)
	case *TypeParam_:
		bt, ok := b.(*TypeParam_)
		return ok && at.Name_ == bt.Name_
	case *TypeVar:
		return a == b
	case *AliasTemplate:
		return a == b
	case *ListType:
		_, ok := b.(*ListType)
		return ok
	case *MapType:
		_, ok := b.(*MapType)
		return ok
	case *PartialType:
		_, ok := b.(*PartialType)
		return ok
	case *TupleType:
		bt, ok := b.(*TupleType)
		return ok && len(at.Elems) == len(bt.Elems)
	case *FuncType:
		bt, ok := b.(*FuncType)
		return ok && len(at.Params) == len(bt.Params)
	case *EnumType:
		bt, ok := b.(*EnumType)
		return ok && sameNominalIdentity(at.Origin, at.Name, bt.Origin, bt.Name) && len(at.TypeArgs) == len(bt.TypeArgs)
	case *StructType:
		bt, ok := b.(*StructType)
		return ok && sameNominalIdentity(at.Origin, at.Name, bt.Origin, bt.Name) && len(at.TypeArgs) == len(bt.TypeArgs)
	case *InterfaceType:
		bt, ok := b.(*InterfaceType)
		return ok && sameNominalIdentity(at.Origin, at.Name, bt.Origin, bt.Name) && len(at.TypeArgs) == len(bt.TypeArgs)
	case *DistinctType:
		bt, ok := b.(*DistinctType)
		return ok && sameNominalIdentity(at.Origin, at.Name, bt.Origin, bt.Name) && len(at.TypeArgs) == len(bt.TypeArgs)
	case *ConjunctionType:
		bt, ok := b.(*ConjunctionType)
		return ok && len(at.Members) == len(bt.Members)
	case *AnonStructType:
		bt, ok := b.(*AnonStructType)
		if !ok || len(at.Fields) != len(bt.Fields) {
			return false
		}
		for _, f := range at.Fields {
			if _, found := scopedField(bt, f.Name); !found {
				return false
			}
		}
		return true
	}
	return false
}

// pairedTypeChildren is typeChildren of a and of b, with solved variables
// followed, when the two have the same shape (sameTypeShape): as[i] and
// bs[i] stand at the same position, an anonymous struct's fields paired by
// name in a's order and a function type's missing result read as Unit, so
// withTypeChildren(a, ...) rebuilds a from parts in as's order. same is
// false, and there are no pairs, where the shapes differ. A function that
// walks two types in step is a recursion over these pairs, and names only
// the kinds it treats specially: one whose parts flow another way (a
// function type's parameters), or one it must stay out of.
func pairedTypeChildren(a, b Type) (as, bs []Type, same bool) {
	a, b = resolveTypeVar(a), resolveTypeVar(b)
	if !sameTypeShape(a, b) {
		return nil, nil, false
	}
	as, bs = typeChildren(a), typeChildren(b)
	switch at := a.(type) {
	case *AnonStructType:
		byName := make([]Type, len(at.Fields))
		for i, f := range at.Fields {
			bf, _ := scopedField(b, f.Name)
			byName[i] = bf.Type
		}
		bs = byName
	case *FuncType:
		n := len(at.Params)
		as[n], bs[n] = normalizeReturn(as[n]), normalizeReturn(bs[n])
	}
	return as, bs, true
}
