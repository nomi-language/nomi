package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// Undetermined type arguments. A generic function is instantiated per
// type-argument tuple (spec §13), so a call whose type argument nothing in
// the program fixes has no instance to run: `Result.collect([])` leaves T and
// E unknown, and `Maybe.values([None])` leaves T unknown.
//
// The rule is narrower than "every inference variable must be solved".
// An unsolved variable is harmless where no value of its type is made: the
// element of an empty collection (`[]`, `Iter.to_list([])`,
// `List.head([])`), the variant a value is not (`Ok(1)`'s error type,
// `Result.map(Ok(3), f)`'s E), or a collection a call returns empty
// (`Vector.empty()`). A callee makes values of a type argument when its
// signature mentions it
//
//   - inside a function type: the callee calls a function with, or receives
//     from it, a value of that type (`Iter.map([], |x| x)`), or
//   - two levels inside a parameter type and inside a collection in its
//     result: the callee takes the argument apart and gathers what it finds
//     into a new collection (`Result.collect`'s T, from `Iter<Result<T, E>>`
//     into `Result<List<T>, E>`; `Maybe.values`, `Iter.flatten`,
//     `Iter.to_map`). `Result.collect`'s E is not made: an error is passed
//     through, so `Result.collect([Ok(1)])` is determined enough.
//
// Such a type argument still unsolved once every body in the file is
// checked is an error. Variables are checked at the end because a later
// statement may still fix them: `r = Result.collect([])` followed by
// `s: Result<List<Int>, String> = r` is determined.

// undeterminedCall is one generic call whose type arguments are checked at
// the end of the file.
type undeterminedCall struct {
	// callee names the function in the message; at is where the error goes
	// when no argument carries the type.
	callee, at ast.Node
	ft         *FuncType
	subs       map[*TypeParam_]Type
	// slotArgs is the argument written for each parameter slot.
	slotArgs map[int]ast.Node
	// keep is the enclosing declaration's own type parameters, which are
	// answers and never holes.
	keep map[string]*TypeParam_
}

// noteUndeterminedCall queues call n for checkUndeterminedTypeArgs. A call
// that reported an error while it was checked is skipped: what it left
// unsolved follows from that error.
func (c *checker) noteUndeterminedCall(callee, at ast.Node, ft *FuncType, subs map[*TypeParam_]Type, slotArgs map[int]ast.Node, errMark int) {
	if len(c.errors) > errMark || at == nil || IsSynthesizedLine(at.LineNum()) {
		return
	}
	c.undetermined = append(c.undetermined, undeterminedCall{
		callee: callee, at: at, ft: ft, subs: subs, slotArgs: slotArgs, keep: c.fnTypeParams,
	})
}

// markUndeterminedReported records value, the right-hand side of a binding
// whose type a binding error has called not determined, so a call inside it
// is not reported a second time.
func (c *checker) markUndeterminedReported(value ast.Node) {
	if value != nil {
		c.undeterminedReported = append(c.undeterminedReported, value)
	}
}

// insideReportedBinding reports whether n lies in the value of a binding
// already reported as not determined.
func (c *checker) insideReportedBinding(n ast.Node) bool {
	inner, ok := spanOf(n)
	if !ok {
		return false
	}
	for _, v := range c.undeterminedReported {
		outer, ok := spanOf(v)
		if !ok {
			continue
		}
		startsIn := inner.StartLine > outer.StartLine || (inner.StartLine == outer.StartLine && inner.StartCol >= outer.StartCol)
		endsIn := inner.EndLine < outer.EndLine || (inner.EndLine == outer.EndLine && inner.EndCol <= outer.EndCol)
		if startsIn && endsIn {
			return true
		}
	}
	return false
}

// checkUndeterminedTypeArgs reports each queued call with a type argument
// that is still unsolved and that its callee's signature uses as a value's
// type (see the comment at the top of this file).
func (c *checker) checkUndeterminedTypeArgs() {
	reported := map[*TypeVar]bool{}
	for _, u := range c.undetermined {
		if c.insideReportedBinding(u.at) {
			continue
		}
		var bad, unsolved []*TypeParam_
		var badVars []*TypeVar
		names := map[*TypeVar]string{}
		for _, tp := range collectOrderedTypeParams(u.ft) {
			if u.keep[tp.Name_] == tp {
				continue
			}
			// A parameter missing from subs was solved by nothing: unify
			// bound the argument's own variable to a type holding it
			// (`[]` checked against `Iter<Result<T, E>>`). It stands for
			// itself, so a fresh variable keys it. One only an omitted
			// defaulted parameter mentions is no argument's.
			v := &TypeVar{}
			if arg, ok := u.subs[tp]; ok {
				if v, ok = resolveTypeVar(arg).(*TypeVar); !ok {
					continue
				}
			} else if a, _ := undeterminedArg(u, tp); a == nil {
				continue
			}
			if _, named := names[v]; named {
				continue
			}
			names[v] = tp.Name_
			unsolved = append(unsolved, tp)
			if !typeParamUsedAsValue(u.ft, tp) {
				continue
			}
			bad = append(bad, tp)
			badVars = append(badVars, v)
		}
		if len(bad) == 0 {
			continue
		}
		fresh := false
		for _, v := range badVars {
			if !reported[v] {
				fresh = true
			}
		}
		if !fresh {
			continue
		}
		for _, v := range badVars {
			reported[v] = true
		}
		c.report(c.undeterminedError(u, bad, unsolved, names))
	}
}

// typeParamUsedAsValue reports whether ft makes values of tp: its
// parameters mention tp inside a function type, or two or more levels inside
// a parameter type while its result holds tp in a collection.
func typeParamUsedAsValue(ft *FuncType, tp *TypeParam_) bool {
	nested := false
	for _, p := range ft.Params {
		inFunc, deep := typeParamPlaces(p, tp, 0, false)
		if inFunc {
			return true
		}
		nested = nested || deep
	}
	return nested && typeParamCollected(ft.Return, tp, 0, false)
}

// typeParamPlaces reports whether t mentions tp inside a function type, and
// whether it mentions it two or more levels down outside one. depth is how
// many type arguments down t is.
func typeParamPlaces(t Type, tp *TypeParam_, depth int, inFunc bool) (bool, bool) {
	if depth > 32 {
		return false, false
	}
	switch tt := t.(type) {
	case *TypeParam_:
		if tt != tp {
			return false, false
		}
		return inFunc, !inFunc && depth >= 2
	case *FuncType:
		inFunc = true
	}
	var f, d bool
	for _, a := range typeChildren(t) {
		af, ad := typeParamPlaces(a, tp, depth+1, inFunc)
		f, d = f || af, d || ad
	}
	return f, d
}

// typeParamCollected reports whether t holds tp as, or inside, the element of
// a collection: a List, Map, or generic host type such as Vector or Set.
func typeParamCollected(t Type, tp *TypeParam_, depth int, inColl bool) bool {
	if depth > 32 {
		return false
	}
	if p, ok := t.(*TypeParam_); ok {
		return p == tp && inColl
	}
	switch t.(type) {
	case *ListType, *MapType, *DistinctType:
		inColl = true
	}
	for _, a := range typeChildren(t) {
		if typeParamCollected(a, tp, depth+1, inColl) {
			return true
		}
	}
	return false
}

// typeChildren is the type arguments t is built from, one level down.
func typeChildren(t Type) []Type {
	switch tt := t.(type) {
	case *ListType:
		return []Type{tt.Elem}
	case *MapType:
		return []Type{tt.Key, tt.Val}
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
	case *PartialType:
		return []Type{tt.Inner}
	case *AnonStructType:
		out := make([]Type, len(tt.Fields))
		for i, f := range tt.Fields {
			out[i] = f.Type
		}
		return out
	case *FuncType:
		return append(append([]Type(nil), tt.Params...), tt.Return)
	}
	return nil
}

// unboundVars is the unsolved inference variables in t.
func unboundVars(t Type) []*TypeVar {
	var out []*TypeVar
	seen := map[*TypeVar]bool{}
	var walk func(Type, int)
	walk = func(t Type, depth int) {
		if t == nil || depth > 32 {
			return
		}
		t = resolveTypeVar(t)
		if v, ok := t.(*TypeVar); ok {
			if !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
			return
		}
		for _, a := range typeChildren(t) {
			walk(a, depth+1)
		}
	}
	walk(t, 0)
	return out
}

// undeterminedError is the error for u, whose type parameters bad are
// unsolved and make values (unsolved is every unsolved one). It goes on the
// argument that carries the first of bad; an empty collection literal there
// is named as the thing to annotate.
func (c *checker) undeterminedError(u undeterminedCall, bad, unsolved []*TypeParam_, names map[*TypeVar]string) TypeError {
	callee := calleeText(u.callee)
	which := joinAnd(tpNameList(bad))
	hint := fmt.Sprintf("`%s` needs to know %s, and nothing in the call or in how its result is used fixes %s",
		callee, which, itOrThem(len(bad)))

	arg, slot := undeterminedArg(u, bad[0])
	if lit, ok := emptyLiteralText(arg); ok {
		ty := c.fa.ExprTypes[arg]
		if ty == nil {
			ty = Substitute(u.ft.Params[slot], u.subs)
		}
		var fill []*TypeParam_
		for _, tp := range unsolved {
			if mentionsTypeParam(u.ft.Params[slot], tp) {
				fill = append(fill, tp)
			}
		}
		return errAt(arg, fmt.Sprintf(
			"the element type of `%s` is not determined: annotate it, as in `xs: %s = %s` with %s filled in, or pass a typed value",
			lit, renderNamingVars(ty, names), lit, joinAnd(tpNameList(fill)))).WithHint(hint)
	}
	at := u.at
	if arg != nil {
		at = arg
	}
	var all []string
	for _, tp := range collectOrderedTypeParams(u.ft) {
		if u.keep[tp.Name_] != tp {
			all = append(all, tp.Name_)
		}
	}
	return errAt(at, fmt.Sprintf(
		"the type argument%s %s of `%s` %s not determined: annotate the value passed in, or give the call its type arguments, as in `%s<%s>(…)`",
		plural(len(bad)), which, callee, isOrAre(len(bad)), callee, strings.Join(all, ", "))).WithHint(hint)
}

func tpNameList(tps []*TypeParam_) []string {
	out := make([]string, len(tps))
	for i, tp := range tps {
		out[i] = tp.Name_
	}
	return out
}

// undeterminedArg is the argument whose parameter mentions tp, preferring
// an empty collection literal, and its slot. nil when no argument does.
func undeterminedArg(u undeterminedCall, tp *TypeParam_) (ast.Node, int) {
	var first ast.Node
	firstSlot := -1
	for slot := 0; slot < len(u.ft.Params); slot++ {
		arg := u.slotArgs[slot]
		if na, ok := arg.(*ast.NamedArg); ok {
			arg = na.Value
		}
		if arg == nil || !mentionsTypeParam(u.ft.Params[slot], tp) {
			continue
		}
		if _, ok := emptyLiteralText(arg); ok {
			return arg, slot
		}
		if first == nil {
			first, firstSlot = arg, slot
		}
	}
	return first, firstSlot
}

func mentionsTypeParam(t Type, tp *TypeParam_) bool {
	if p, ok := t.(*TypeParam_); ok {
		return p == tp
	}
	for _, a := range typeChildren(t) {
		if mentionsTypeParam(a, tp) {
			return true
		}
	}
	return false
}

// emptyLiteralText is the spelling of an empty, unannotated collection
// literal.
func emptyLiteralText(n ast.Node) (string, bool) {
	switch v := n.(type) {
	case *ast.ListLit:
		if len(v.Items) == 0 && v.TypeName == nil {
			return "[]", true
		}
	case *ast.VectorLit:
		if len(v.Items) == 0 {
			return "#[]", true
		}
	case *ast.SetLit:
		if len(v.Items) == 0 {
			return "#{}", true
		}
	case *ast.MapLit:
		if len(v.Entries) == 0 && v.TypeName == nil {
			return "{=>}", true
		}
	}
	return "", false
}

// renderNamingVars is t's spelling with each unsolved variable in names
// written as that name, and any other unsolved variable as `_`.
func renderNamingVars(t Type, names map[*TypeVar]string) string {
	vars := unboundVars(t)
	for _, v := range vars {
		name := names[v]
		if name == "" {
			name = "_"
		}
		v.Resolved = &TypeParam_{Name_: name}
	}
	s := t.String()
	for _, v := range vars {
		v.Resolved = nil
	}
	return s
}

func joinAnd(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func isOrAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

func itOrThem(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}
