package analysis

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"
)

// RuleText renders a refinement's rule on one line, as diagnostics and
// hovers quote it: `fn port -> port >= 1`. Empty for no rule.
func RuleText(rule ast.Node) string {
	if rule == nil {
		return ""
	}
	return strings.Join(strings.Fields(format.RenderNode(rule)), " ")
}

// refinementOf answers t as a distinct refinement, or nil when it is none.
func refinementOf(t Type) *DistinctType {
	dt, ok := resolveTypeVar(t).(*DistinctType)
	if !ok || dt.Rule == "" {
		return nil
	}
	return dt
}

// IsRefinement reports whether t is a refinement: a distinct type or a
// struct that declares a `when` rule.
func IsRefinement(t Type) bool {
	switch tt := resolveTypeVar(t).(type) {
	case *DistinctType:
		return tt.Rule != ""
	case *StructType:
		return tt.Rule != ""
	}
	return false
}

// RefinementRule answers t's rule as written, and "" for a type with none.
func RefinementRule(t Type) string {
	switch tt := resolveTypeVar(t).(type) {
	case *DistinctType:
		return tt.Rule
	case *StructType:
		return tt.Rule
	}
	return ""
}

// RefinementBase answers the type at the end of t's chain of parents: Float
// for `PositiveFloat` over `NonNegativeFloat` over `Float`. A type that is
// no distinct refinement is its own base.
func RefinementBase(t Type) Type {
	for i := 0; i < maxRefinementChain; i++ {
		dt := refinementOf(t)
		if dt == nil || dt.Inner == nil {
			return t
		}
		t = dt.Inner
	}
	return t
}

// RefinementChain answers t's chain from t up to the base, t first and the
// base last: [PositiveFloat, NonNegativeFloat, Float]. A type that is no
// distinct refinement is a chain of one.
func RefinementChain(t Type) []Type {
	out := []Type{t}
	for i := 0; i < maxRefinementChain; i++ {
		dt := refinementOf(t)
		if dt == nil || dt.Inner == nil {
			return out
		}
		t = dt.Inner
		out = append(out, t)
	}
	return out
}

// maxRefinementChain bounds a walk up a chain of parents. A chain that
// reaches itself is a declaration cycle the type builder reports
// (distinctCycle), and its inner type is left unset.
const maxRefinementChain = 64

// refinesInto reports whether a value of have converts up to want: want
// is one of have's ancestors or its base, by type identity.
func refinesInto(have, want Type) bool {
	if refinementOf(have) == nil {
		return false
	}
	chain := RefinementChain(have)
	for _, anc := range chain[1:] {
		if TypesIdentical(anc, want) {
			return true
		}
	}
	return false
}

// refinementBaseOf answers what a refinement's values are built from: the
// base at the end of a distinct refinement's chain, or the record of a
// struct refinement's fields. Nil for a type that is no refinement.
func refinementBaseOf(t Type) Type {
	switch tt := t.(type) {
	case *DistinctType:
		if tt.Rule != "" && tt.Inner != nil {
			return RefinementBase(tt)
		}
	case *StructType:
		if tt.Rule != "" {
			return &AnonStructType{Fields: tt.Fields}
		}
	}
	return nil
}

// IsDistinctRefinement reports whether t is a distinct refinement, whose
// values are its base's at run time.
func IsDistinctRefinement(t Type) bool {
	return refinementOf(t) != nil
}

// eraseRefinements is t with each distinct refinement in it replaced by its
// base: the type a value of t is at run time, which a comparison compares
// it as.
func eraseRefinements(t Type, depth int) Type {
	if t == nil || depth > 16 {
		return t
	}
	t = resolveTypeVar(t)
	each := func(ts []Type) ([]Type, bool) {
		var out []Type
		for i, a := range ts {
			e := eraseRefinements(a, depth+1)
			if e != a && out == nil {
				out = append([]Type(nil), ts...)
			}
			if out != nil {
				out[i] = e
			}
		}
		return out, out != nil
	}
	switch tt := t.(type) {
	case *DistinctType:
		if tt.Rule != "" && tt.Inner != nil {
			return eraseRefinements(RefinementBase(tt), depth+1)
		}
		if args, changed := each(tt.TypeArgs); changed {
			c := *tt
			c.TypeArgs = args
			return &c
		}
	case *ListType:
		if e := eraseRefinements(tt.Elem, depth+1); e != tt.Elem {
			return &ListType{Elem: e}
		}
	case *MapType:
		k, v := eraseRefinements(tt.Key, depth+1), eraseRefinements(tt.Val, depth+1)
		if k != tt.Key || v != tt.Val {
			return &MapType{Key: k, Val: v}
		}
	case *TupleType:
		if elems, changed := each(tt.Elems); changed {
			return &TupleType{Elems: elems}
		}
	case *EnumType:
		if args, changed := each(tt.TypeArgs); changed {
			c := *tt
			c.TypeArgs = args
			return &c
		}
	case *StructType:
		if args, changed := each(tt.TypeArgs); changed {
			c := *tt
			c.TypeArgs = args
			return &c
		}
	}
	return t
}
