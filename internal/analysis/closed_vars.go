package analysis

// Closing the variables inference left open.
//
// The language permits an inference variable to stay unsolved where no
// value of its type is made (undetermined.go): `[]`'s element, `Ok(1)`'s
// error type, `List.head([])`'s Maybe. The program means the same whatever
// the variable is, but the IR builder still has to pick a representation
// for it, and every record that mentions the variable (ExprTypes, a call's
// CallType, a binding's type) has to give it the same one, or an argument
// is built at one instance and the callee at another.
//
// So once every body in the file is checked, the checker closes each
// variable still unsolved: it gives the variable one closed type, in its
// Closed field, which every record mentioning the variable shares. The
// rule is the same everywhere: Int when Int meets every interface bound
// the variable was checked against, else Unit when Unit does. Int is the
// first choice because it has a representation in every position the
// builder lowers (an element, a key, a payload, a field) and implements
// every std interface a bound names (Display, Comparable, Hashable, the
// arithmetic ones); Unit, which implements only Equatable and Hashable, is
// not a list or vector element the builder retains. Unit is the answer for
// a variable whose bound only Unit meets. A bound is the interface a
// generic call's type argument or a where clause's subject was checked
// against while the variable was still open, or a demand an interface call
// or an interpolation placed on it (openBounds, recorded by
// typeImplementsInterface and recordBoundConformanceRecording). A variable
// no candidate satisfies stays open, and the elaboration gate reports it.
//
// Closed is for lowering only. The checker and every display (hover,
// diagnostics, inlay hints) read Resolved, which stays nil, so the variable
// still prints as `?N`. A later unification that solves the variable (a
// `once` another file reads) sets Resolved, which wins over Closed.
// [ResolveClosed] and [Elaborate] are the IR builder's readers.

// openBound is one interface bound checked against t while t held an
// unsolved variable. meets reruns the check against t with the variables
// closed.
type openBound struct {
	t     Type
	meets func(Type) bool
}

// closingCandidates are the types an unsolved variable is closed as, in
// order of preference.
var closingCandidates = []Type{TypeInt, TypeUnit}

// closeOpenVariables gives each variable this checker made that is still
// unsolved its closed type.
func (c *checker) closeOpenVariables() {
	c.closing = true
	defer func() { c.closing = false }()
	// The bounds each open variable takes part in, by the variable that
	// stands for it now.
	byVar := map[*TypeVar][]int{}
	for i, b := range c.openBounds {
		for _, v := range openVarsIn(b.t) {
			byVar[v] = append(byVar[v], i)
		}
	}
	answers := map[*TypeVar]Type{}
	for _, v := range c.vars {
		root, open := resolveTypeVar(v).(*TypeVar)
		if !open || root.Closed != nil {
			continue
		}
		if _, done := answers[root]; done {
			continue
		}
		for _, cand := range closingCandidates {
			answers[root] = cand
			ok := true
			for _, i := range byVar[root] {
				b := c.openBounds[i]
				if !b.meets(substituteVars(b.t, answers)) {
					ok = false
					break
				}
			}
			if ok {
				root.Closed = cand
				break
			}
			delete(answers, root)
		}
	}
}

// openVarsIn is the unsolved variables in t, at any depth.
func openVarsIn(t Type) []*TypeVar {
	var out []*TypeVar
	seen := map[*TypeVar]bool{}
	walkTypeParts(t, func(p Type) {
		if v, ok := p.(*TypeVar); ok && v.Resolved == nil && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	})
	return out
}

// walkTypeParts calls f on t and each type inside it, through solved
// variables.
func walkTypeParts(t Type, f func(Type)) {
	var walk func(t Type, depth int)
	walk = func(t Type, depth int) {
		t = resolveTypeVar(t)
		if t == nil || depth > 64 {
			return
		}
		f(t)
		switch tt := t.(type) {
		case *ListType:
			walk(tt.Elem, depth+1)
		case *MapType:
			walk(tt.Key, depth+1)
			walk(tt.Val, depth+1)
		case *TupleType:
			for _, e := range tt.Elems {
				walk(e, depth+1)
			}
		case *FuncType:
			for _, p := range tt.Params {
				walk(p, depth+1)
			}
			walk(tt.Return, depth+1)
		case *AnonStructType:
			for _, fd := range tt.Fields {
				walk(fd.Type, depth+1)
			}
		case *EnumType:
			for _, a := range tt.TypeArgs {
				walk(a, depth+1)
			}
		case *StructType:
			for _, a := range tt.TypeArgs {
				walk(a, depth+1)
			}
		case *DistinctType:
			for _, a := range tt.TypeArgs {
				walk(a, depth+1)
			}
		case *InterfaceType:
			for _, a := range tt.TypeArgs {
				walk(a, depth+1)
			}
		case *ConjunctionType:
			for _, m := range tt.Members {
				walk(m, depth+1)
			}
		case *PartialType:
			walk(tt.Inner, depth+1)
		}
	}
	walk(t, 0)
}

// substituteVars is t with each unsolved variable answers names replaced by
// its answer, at any depth. Parts with nothing to replace are shared.
func substituteVars(t Type, answers map[*TypeVar]Type) Type {
	return mapOpenVars(t, func(v *TypeVar) Type { return answers[v] }, false, 0)
}

// resolveSolvedVars is t with every solved variable replaced by its
// solution, at any depth, so a substitution reaches the type parameters a
// solution holds.
func resolveSolvedVars(t Type) Type {
	return mapOpenVars(t, func(*TypeVar) Type { return nil }, true, 0)
}

// ResolveClosed is t through solved variables, and an unsolved variable's
// closed type (TypeVar.Closed) when the checker gave it one: the type the
// IR builder lowers. It looks at t's outermost layer only; [Elaborate]
// replaces at every depth.
func ResolveClosed(t Type) Type {
	t = resolveTypeVar(t)
	if v, ok := t.(*TypeVar); ok && v.Closed != nil {
		return v.Closed
	}
	return t
}

// Elaborate is t with every unsolved variable the checker closed replaced
// by its closed type, at any depth: the type the IR builder lowers. A t
// with no such variable is returned as it is.
func Elaborate(t Type) Type {
	return mapOpenVars(t, func(v *TypeVar) Type { return v.Closed }, false, 0)
}

// mapOpenVars is t with each unsolved variable v replaced by answer(v)
// when that is not nil. With solved, a solved variable is replaced by its
// solution too; without, one whose solution holds nothing to replace is
// kept.
func mapOpenVars(t Type, answer func(*TypeVar) Type, solved bool, depth int) Type {
	if t == nil || depth > 64 {
		return t
	}
	if r := resolveTypeVar(t); r != t {
		if m := mapOpenVars(r, answer, solved, depth+1); m != r || solved {
			return m
		}
		return t
	}
	sub := func(x Type) Type { return mapOpenVars(x, answer, solved, depth+1) }
	subs := func(xs []Type) ([]Type, bool) {
		var out []Type
		for i, x := range xs {
			y := sub(x)
			if y != x && out == nil {
				out = make([]Type, len(xs))
				copy(out, xs[:i])
			}
			if out != nil {
				out[i] = y
			}
		}
		if out == nil {
			return xs, false
		}
		return out, true
	}
	switch tt := t.(type) {
	case *TypeVar:
		if a := answer(tt); a != nil {
			return a
		}
	case *ListType:
		if e := sub(tt.Elem); e != tt.Elem {
			return &ListType{Elem: e}
		}
	case *MapType:
		k, v := sub(tt.Key), sub(tt.Val)
		if k != tt.Key || v != tt.Val {
			return &MapType{Key: k, Val: v}
		}
	case *TupleType:
		if es, changed := subs(tt.Elems); changed {
			return &TupleType{Elems: es}
		}
	case *FuncType:
		ps, changed := subs(tt.Params)
		r := sub(tt.Return)
		if changed || r != tt.Return {
			c := *tt
			c.Params, c.Return = ps, r
			return &c
		}
	case *AnonStructType:
		var fields []FieldDef
		for i, f := range tt.Fields {
			ft := sub(f.Type)
			if ft != f.Type && fields == nil {
				fields = make([]FieldDef, len(tt.Fields))
				copy(fields, tt.Fields)
			}
			if fields != nil {
				fields[i].Type = ft
			}
		}
		if fields != nil {
			return &AnonStructType{Fields: fields}
		}
	case *EnumType:
		if as, changed := subs(tt.TypeArgs); changed {
			c := *tt
			c.TypeArgs = as
			return &c
		}
	case *StructType:
		if as, changed := subs(tt.TypeArgs); changed {
			c := *tt
			c.TypeArgs = as
			return &c
		}
	case *DistinctType:
		as, changed := subs(tt.TypeArgs)
		in := sub(tt.Inner)
		if changed || in != tt.Inner {
			c := *tt
			c.TypeArgs, c.Inner = as, in
			return &c
		}
	case *InterfaceType:
		if as, changed := subs(tt.TypeArgs); changed {
			c := *tt
			c.TypeArgs = as
			return &c
		}
	case *ConjunctionType:
		members := make([]*InterfaceType, len(tt.Members))
		changed := false
		for i, m := range tt.Members {
			members[i], _ = sub(m).(*InterfaceType)
			if members[i] == nil {
				return t
			}
			changed = changed || members[i] != m
		}
		if changed {
			return NewConjunction(members)
		}
	case *PartialType:
		if in := sub(tt.Inner); in != tt.Inner {
			return &PartialType{Inner: in}
		}
	}
	return t
}
