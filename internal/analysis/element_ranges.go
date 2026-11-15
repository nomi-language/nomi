package analysis

import (
	"math/big"

	"github.com/nomi-language/nomi/internal/ast"
)

// Element ranges (spec §4 *Value ranges*). The checker knows a range for
// each element of a few iterables, and a callback a std `Iter` function
// calls with those elements takes it for the parameter that receives them,
// so in `1..=n |> Iter.filter(fn d -> x % d == 0)` the parameter `d` is 1
// and up and `%` takes it as a divisor. The parameter keeps its type (an
// Int there): the range converts it where a subtype is expected, as a
// binding's does, and changes no type the program infers.
//
// The sources whose elements have a range (elemFactOf):
//
//   - a range literal of Ints, `a..b` or `a..=b`: from the lowest value
//     `a`'s range holds to the highest `b`'s does (below it for `a..b`);
//   - `Range.from(n)` and `Iter.from(n)`: n's lowest value and up;
//     `Range.naturals()`: 0 and up;
//   - `Range.step_by(r, by)` of a range literal: between its ends, which
//     way the step goes;
//   - a List, Vector or Set literal with no spread: the union of its
//     elements' ranges, part by part for tuples;
//   - an `Iter` function that passes the elements it keeps through
//     unchanged (filter, reject, take, drop, take_while, drop_while,
//     sort, sort_by, sort_with, reverse, to_list, to_vector, to_set,
//     cycle, and concat's two sources); `Iter.with_index`'s and
//     `Iter.zip`'s tuples, part by part; `Iter.map` and `Iter.scan`: the
//     range of the callback's result, when its body has no `return` or
//     `break`.
//
// The fact is worked out when the callback is checked, after its source:
// a callback argument is noted with its source as the call is checked
// (noteIterCallbacks), and checkLambdaExpecting and checkCapture seed the
// parameter's range (seedLambdaElems, seedCaptureElems). A source of any
// other form, a binding among them, gives no fact.

// elemFact is what the checker knows of each element of an iterable: a
// range of fam, or, for a tuple, a fact per part (nil for a part it knows
// nothing of).
type elemFact struct {
	fam   *numericFamily
	r     numRange
	parts []*elemFact
}

// callbackElem is a callback argument of a std Iter function: the source
// whose elements it receives, at its parameters params.
type callbackElem struct {
	source ast.Node
	params []int
}

// iterElemCallbacks are the std Iter functions that call a callback with
// their source's elements, and the callback's parameters that receive one.
var iterElemCallbacks = map[string][]int{
	"map": {0}, "filter": {0}, "reject": {0}, "each": {0}, "find": {0},
	"find_map": {0}, "find_index": {0}, "any_match": {0}, "all_match": {0},
	"flat_map": {0}, "take_while": {0}, "drop_while": {0}, "partition": {0},
	"group_by": {0}, "count_by": {0}, "min_by": {0}, "max_by": {0},
	"sum_by": {0}, "mean_by": {0}, "product_by": {0}, "chunk_by": {0},
	"sort_by": {0}, "sort_with": {0, 1},
	"reduce": {1}, "scan": {1}, "try_reduce": {1}, "map_reduce": {1},
}

// iterPassThrough are the std Iter functions whose result's elements are
// some of their first argument's, unchanged.
var iterPassThrough = map[string]bool{
	"filter": true, "reject": true, "take": true, "drop": true,
	"take_while": true, "drop_while": true, "sort": true, "sort_by": true,
	"sort_with": true, "reverse": true, "to_list": true, "to_vector": true,
	"to_set": true, "cycle": true,
}

// stdCallee is the owner and name of a call written `Owner.name(...)`.
func stdCallee(call *ast.Call) (owner, name string) {
	fa, ok := ungroupExpr(call.Func).(*ast.FieldAccess)
	if !ok || fa.Field == nil {
		return "", ""
	}
	t, ok := fa.Object.(*ast.TypeIdent)
	if !ok || t == nil {
		return "", ""
	}
	return t.Name, fa.Field.Name
}

// callArgs is call's arguments, with piped first when the call is a
// pipe's stage.
func callArgs(call *ast.Call, piped ast.Node) []ast.Node {
	args := make([]ast.Node, 0, len(call.Args)+1)
	if piped != nil {
		args = append(args, piped)
	}
	for _, a := range call.Args {
		args = append(args, valueOfArg(a))
	}
	return args
}

// noteIterCallbacks notes each lambda or capture argument of call, a std
// Iter function that calls it with its source's elements, with the source.
func (c *checker) noteIterCallbacks(call *ast.Call, piped ast.Node) {
	owner, name := stdCallee(call)
	if owner != "Iter" {
		return
	}
	params, ok := iterElemCallbacks[name]
	if !ok {
		return
	}
	args := callArgs(call, piped)
	if len(args) < 2 || args[0] == nil {
		return
	}
	for _, a := range args[1:] {
		switch cb := ungroupExpr(a).(type) {
		case *ast.Lambda, *ast.Capture:
			if c.callbackElems == nil {
				c.callbackElems = map[ast.Node]callbackElem{}
			}
			c.callbackElems[cb] = callbackElem{source: args[0], params: params}
		}
	}
}

// seedLambdaElems gives each parameter of n that receives its source's
// elements their range, with params the parameters' types and expected
// the types the position passes. A parameter annotated with a subtype the
// range proves, `fn d: PositiveInt ->` over `1..=n`, takes the type the
// position passes, as a partial capture's slot does (holeProvesSlot):
// params is changed in place.
func (c *checker) seedLambdaElems(n *ast.Lambda, params, expected []Type) {
	ce, ok := c.callbackElems[n]
	if !ok {
		return
	}
	f := c.elemFactOf(ce.source, 0)
	if f == nil {
		return
	}
	for _, i := range ce.params {
		if i >= len(n.Params) || i >= len(params) {
			continue
		}
		p := n.Params[i]
		if p.Destructure != nil {
			c.seedPattern(p.Destructure, f)
			continue
		}
		c.seedSym(c.fa.Definitions[Pos{Line: p.Line, Col: p.Col}], params[i], f)
		if p.TypeAnnotation != nil && i < len(expected) && c.factProves(f, resolveTypeVar(expected[i]), params[i]) {
			params[i] = resolveTypeVar(expected[i])
		}
	}
}

// factProves reports whether an element of fact f, passed as a have,
// converts to want: have is want's base and f's range proves want's
// rules.
func (c *checker) factProves(f *elemFact, have, want Type) bool {
	if f == nil || f.fam == nil || have == nil || want == nil || refinementOf(want) == nil || ContainsTypeParam(want) {
		return false
	}
	fam := familyOfBase(RefinementBase(want))
	if fam != f.fam || !TypesEqual(RefinementBase(want), have) || !provableTarget(want, fam) {
		return false
	}
	need, _ := typeRange(want, fam)
	return within(f.r, need)
}

// seedCaptureElems gives each `&N` of n that receives its source's
// elements their range.
func (c *checker) seedCaptureElems(n *ast.Capture, holes []ast.Node) {
	ce, ok := c.callbackElems[n]
	if !ok {
		return
	}
	f := c.elemFactOf(ce.source, 0)
	if f == nil || f.fam == nil {
		return
	}
	for _, i := range ce.params {
		for _, h := range holes {
			if ast.HoleNumber(h) == i+1 {
				c.setRange(h, f.fam, f.r)
			}
		}
	}
}

// seedSym gives sym, a binding of type ty, f's range met with what ty
// states.
func (c *checker) seedSym(sym *Symbol, ty Type, f *elemFact) {
	if sym == nil || f == nil || f.fam == nil || familyOfBase(RefinementBase(ty)) != f.fam {
		return
	}
	declared, _ := typeRange(ty, f.fam)
	r := meet(f.fam, declared, f.r)
	if !r.known {
		return
	}
	if c.symRanges == nil {
		c.symRanges = map[*Symbol]famRange{}
	}
	c.symRanges[sym] = famRange{f.fam, r}
}

// seedPattern gives the names a parameter's pattern binds their parts of
// f: `fn (i, x) ->` over `Iter.with_index(1..=n)` gives x 1 and up.
func (c *checker) seedPattern(pat ast.Node, f *elemFact) {
	if f == nil {
		return
	}
	switch p := pat.(type) {
	case *ast.IdentPattern:
		if sym := c.fa.Definitions[Pos{Line: p.Line, Col: p.Col}]; sym != nil {
			c.seedSym(sym, c.bindingType(sym), f)
		}
	case *ast.TuplePattern:
		if p.Flat || len(p.Patterns) != len(f.parts) {
			return
		}
		for i, sub := range p.Patterns {
			c.seedPattern(sub, f.parts[i])
		}
	}
}

// maxElemDepth bounds how far elemFactOf follows a chain of adapters.
const maxElemDepth = 32

// elemFactOf is what the checker knows of each element of n, nil when
// nothing.
func (c *checker) elemFactOf(n ast.Node, depth int) *elemFact {
	if n == nil || depth > maxElemDepth {
		return nil
	}
	switch v := ungroupExpr(n).(type) {
	case *ast.RangeLit:
		return c.rangeLitElems(v)
	case *ast.ListLit:
		if len(v.Spreads) > 0 || len(v.Items) == 0 {
			return nil
		}
		var out *elemFact
		for i, item := range v.Items {
			f := c.valueFact(item)
			if f == nil {
				return nil
			}
			if i == 0 {
				out = f
				continue
			}
			if out = unionFacts(out, f); out == nil {
				return nil
			}
		}
		return out
	case *ast.Call:
		return c.callElems(v, nil, depth)
	case *ast.Binary:
		if v.Op != "|>" {
			return nil
		}
		if call, ok := ungroupExpr(v.Right).(*ast.Call); ok {
			return c.callElems(call, v.Left, depth)
		}
	}
	return nil
}

// callElems is what the checker knows of each element of call's result,
// with piped the value piped into it when it is a pipe's stage.
func (c *checker) callElems(call *ast.Call, piped ast.Node, depth int) *elemFact {
	owner, name := stdCallee(call)
	args := callArgs(call, piped)
	switch owner {
	case "Range":
		switch name {
		case "from":
			if len(args) == 1 {
				return c.fromElems(args[0])
			}
		case "naturals":
			if len(args) == 0 {
				return intElems(numRange{known: true, lo: closedBound(new(big.Rat)), hi: infBound()})
			}
		case "step_by":
			if len(args) == 2 {
				if rl, ok := ungroupExpr(args[0]).(*ast.RangeLit); ok && rl.Start != nil && rl.End != nil && c.intValued(rl.Start) {
					// A step either way walks from the start toward the
					// end and stops at it.
					return intElems(union(c.valueRange(rl.Start, intFamily), c.valueRange(rl.End, intFamily)))
				}
			}
		}
	case "Iter":
		switch {
		case name == "from" && len(args) == 1:
			return c.fromElems(args[0])
		case iterPassThrough[name] && len(args) >= 1:
			return c.elemFactOf(args[0], depth+1)
		case name == "concat" && len(args) == 2:
			a, b := c.elemFactOf(args[0], depth+1), c.elemFactOf(args[1], depth+1)
			if a == nil || b == nil {
				return nil
			}
			return unionFacts(a, b)
		case name == "with_index" && len(args) == 1:
			if f := c.elemFactOf(args[0], depth+1); f != nil {
				return &elemFact{parts: []*elemFact{nil, f}}
			}
		case name == "zip" && len(args) == 2:
			a, b := c.elemFactOf(args[0], depth+1), c.elemFactOf(args[1], depth+1)
			if a != nil || b != nil {
				return &elemFact{parts: []*elemFact{a, b}}
			}
		case name == "map" && len(args) == 2, name == "scan" && len(args) == 3:
			// scan's elements are its callback's results; its first
			// accumulator, `from`, is not one of them.
			return c.callbackResultFact(args[len(args)-1])
		}
	}
	return nil
}

// fromElems is the elements of `Range.from(start)` or `Iter.from(start)`:
// start's lowest value and up.
func (c *checker) fromElems(start ast.Node) *elemFact {
	if !c.intValued(start) {
		return nil
	}
	s := c.valueRange(start, intFamily)
	if !s.known || s.lo.inf {
		return nil
	}
	return intElems(numRange{known: true, lo: s.lo, hi: infBound()})
}

// rangeLitElems is the elements of a range literal of Ints: from the
// lowest value its start can be to the highest its end can be, below it
// when the end is left out.
func (c *checker) rangeLitElems(rl *ast.RangeLit) *elemFact {
	if rl.Start == nil || !c.intValued(rl.Start) {
		return nil
	}
	r := numRange{known: true, lo: infBound(), hi: infBound()}
	if s := c.valueRange(rl.Start, intFamily); s.known {
		r.lo = s.lo
	}
	if rl.End != nil {
		if e := c.valueRange(rl.End, intFamily); e.known && !e.hi.inf {
			r.hi = rbound{v: e.hi.v, open: e.hi.open || !rl.Inclusive}
		}
	}
	return intElems(r)
}

// intElems is a fact of Ints in r, nil when r holds nothing: no bound, or
// no value.
func intElems(r numRange) *elemFact {
	if !r.known || (r.lo.inf && r.hi.inf) {
		return nil
	}
	r = numRange{known: true, lo: r.lo, hi: r.hi}.normalize(intFamily)
	if r.empty() {
		return nil
	}
	return &elemFact{fam: intFamily, r: r}
}

// intValued reports whether n is an Int, or an Int literal.
func (c *checker) intValued(n ast.Node) bool {
	return c.valueFamily(n) == intFamily
}

// valueFamily is the numeric family of n's value: its type's, or a
// literal's.
func (c *checker) valueFamily(n ast.Node) *numericFamily {
	if t := c.fa.ExprTypes[n]; t != nil {
		return familyOfBase(RefinementBase(t))
	}
	if t := c.fa.ExprTypes[ungroupExpr(n)]; t != nil {
		return familyOfBase(RefinementBase(t))
	}
	for _, fam := range []*numericFamily{intFamily, floatFamily, decimalFamily} {
		if _, ok := literalRange(n, fam); ok {
			return fam
		}
	}
	return nil
}

// valueRange is n's range: exprRange, or, for a binding read the checker
// has not reached yet, the binding's.
func (c *checker) valueRange(n ast.Node, fam *numericFamily) numRange {
	r := c.exprRange(n, fam)
	if r.known {
		return r
	}
	if id, ok := ungroupExpr(n).(*ast.Ident); ok {
		if sym := c.fa.References[Pos{Line: id.Line, Col: id.Col}]; sym != nil {
			return c.bindingRange(sym, fam)
		}
	}
	return r
}

// valueFact is what the checker knows of the value n: its range, or a
// tuple's parts'. The range's literals give the elements no type of their
// own: typed is cleared.
func (c *checker) valueFact(n ast.Node) *elemFact {
	if t, ok := ungroupExpr(n).(*ast.TupleLit); ok {
		parts := make([]*elemFact, len(t.Items))
		anyPart := false
		for i, item := range t.Items {
			parts[i] = c.valueFact(item)
			anyPart = anyPart || parts[i] != nil
		}
		if !anyPart {
			return nil
		}
		return &elemFact{parts: parts}
	}
	fam := c.valueFamily(n)
	if fam == nil {
		return nil
	}
	r := c.valueRange(n, fam)
	if !r.known {
		return nil
	}
	r.typed = false
	return &elemFact{fam: fam, r: r}
}

// callbackResultFact is what the checker knows of each result of cb, a
// callback checked already: its body's value's, when the body has no
// `return` or `break` that ends it with another value.
func (c *checker) callbackResultFact(cb ast.Node) *elemFact {
	switch v := ungroupExpr(cb).(type) {
	case *ast.Lambda:
		if v.Body == nil || leavesEarly(v.Body) {
			return nil
		}
		if t := tailExpr(v.Body); t != nil {
			return c.valueFact(t)
		}
	case *ast.Capture:
		if leavesEarly(v.Body) {
			return nil
		}
		return c.valueFact(v.Body)
	}
	return nil
}

// leavesEarly reports whether n holds a `return` or a `break`.
func leavesEarly(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(m ast.Node) bool {
		switch m.(type) {
		case *ast.Return, *ast.Break:
			found = true
		}
		return !found
	})
	return found
}

// unionFacts is what holds of a value of either fact, nil when nothing.
func unionFacts(a, b *elemFact) *elemFact {
	if a == nil || b == nil {
		return nil
	}
	if a.fam != nil || b.fam != nil {
		if a.fam != b.fam {
			return nil
		}
		r := union(a.r, b.r)
		if !r.known {
			return nil
		}
		return &elemFact{fam: a.fam, r: r}
	}
	if len(a.parts) != len(b.parts) || len(a.parts) == 0 {
		return nil
	}
	parts := make([]*elemFact, len(a.parts))
	anyPart := false
	for i := range a.parts {
		parts[i] = unionFacts(a.parts[i], b.parts[i])
		anyPart = anyPart || parts[i] != nil
	}
	if !anyPart {
		return nil
	}
	return &elemFact{parts: parts}
}

// holeProvesSlot reports whether arg, a partial capture's `&N` that the
// position passes a have, converts to want, the slot it fills, by the
// range of the elements it receives (seedCaptureElems): have is want's
// base and the range proves want's rules.
func (c *checker) holeProvesSlot(arg ast.Node, have, want Type) bool {
	if na, ok := arg.(*ast.NamedArg); ok {
		arg = na.Value
	}
	fr, ok := c.ranges[arg]
	if !ok {
		return false
	}
	return c.factProves(&elemFact{fam: fr.fam, r: fr.r}, have, want)
}
