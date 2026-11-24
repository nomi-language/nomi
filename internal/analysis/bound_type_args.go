package analysis

import (
	"fmt"
	"slices"
	"strings"
)

// implArgLists is the interface type arguments of each impl of iface that
// concrete has, read for concrete itself: for Decimal and Steppable,
// [[Decimal]]; for a generic receiver, each impl's arguments with the
// impl's own type parameters replaced by concrete's type arguments. An
// impl whose receiver does not match concrete is left out.
func (c *checker) implArgLists(concrete Type, iface string) [][]Type {
	name := concreteTypeName(concrete)
	if name == "" {
		return nil
	}
	candidates := lookupImplTypeArgSets(name, iface, c.implTypeArgSetsContext())
	if len(candidates) == 0 {
		if info := lookupImplTypeArgs(name, iface, c.implTypeArgsContext()); info != nil {
			candidates = []*ImplTypeArgs{info}
		}
	}
	cargs := concreteTypeArgs(concrete)
	var out [][]Type
	for _, info := range candidates {
		if info == nil || c.hidesConstruction(info, concrete) || !implReceiverMatches(info, concrete, c.implsContext(), c.implTypeArgsContext()) {
			continue
		}
		args := make([]Type, len(info.Args))
		for i, a := range info.Args {
			args[i] = substituteTypeParamDefs(info.TypeParamDefs, cargs, a)
		}
		out = append(out, args)
	}
	return out
}

// boundArgsFit reports whether concrete has an impl of bound whose type
// arguments are the ones the call gives the bound: with `where T:
// Steppable<S>`, T solved to Decimal and S to Float, Decimal must have an
// impl of Steppable<Float>, and its only impl is Steppable<Decimal>. A bound
// with no type arguments, an argument the call left unsolved, or a
// receiver whose impls record no arguments fits; typeImplementsInterface
// has already answered whether any impl exists. When it does not fit, the
// second result is each impl's interface as concrete has it, for the hint.
func (c *checker) boundArgsFit(concrete Type, bound *InterfaceType, subs map[*TypeParam_]Type) (bool, []Type) {
	if bound == nil || len(bound.TypeArgs) == 0 || c.fa == nil {
		return true, nil
	}
	concrete = resolveTypeVar(concrete)
	if ContainsTypeParam(concrete) || containsTypeVar(concrete) {
		return true, nil
	}
	want := make([]Type, len(bound.TypeArgs))
	for i, a := range bound.TypeArgs {
		want[i] = resolveTypeVar(Substitute(a, subs))
		if ContainsTypeParam(want[i]) || containsTypeVar(want[i]) {
			return true, nil
		}
	}
	lists := c.implArgLists(concrete, bound.Name)
	if len(lists) == 0 {
		return true, nil
	}
	var have []Type
	for i, args := range lists {
		if len(args) != len(want) {
			continue
		}
		if sameTypeArgs(args, want) {
			return true, nil
		}
		// The impl tables of several scopes can each record the same impl.
		if !slices.ContainsFunc(lists[:i], func(earlier []Type) bool { return sameTypeArgs(earlier, args) }) {
			have = append(have, &InterfaceType{Origin: bound.Origin, Name: bound.Name, TypeArgs: args})
		}
	}
	if len(have) == 0 {
		return true, nil
	}
	return false, have
}

// boundArgsAmbiguous reports, and says it did, a call whose bound's subject
// implements the bound's interface at several type arguments when the call
// leaves the bound's arguments open: `via(b, .Red)` for `fn via<C, T>(c: C,
// x: T) where C: Holds<T>` and a `Box` implementing `Holds<Color>` and
// `Holds<Shade>`. Arguments the call solved chose (boundArgsFit).
func (c *checker) boundArgsAmbiguous(line, col int, concrete Type, bound *InterfaceType, subs map[*TypeParam_]Type) bool {
	if bound == nil || len(bound.TypeArgs) == 0 || c.fa == nil {
		return false
	}
	concrete = resolveTypeVar(concrete)
	if ContainsTypeParam(concrete) || containsTypeVar(concrete) {
		return false
	}
	open := false
	for _, a := range bound.TypeArgs {
		w := resolveTypeVar(Substitute(a, subs))
		if containsTypeVar(w) || c.containsForeignTypeParam(w) {
			open = true
		}
	}
	if !open {
		return false
	}
	cands := c.implCandidates(concrete, bound.Name, bound.Origin)
	if len(cands) < 2 {
		return false
	}
	list, _ := candidateList(cands)
	c.addError(line, col, fmt.Sprintf(
		"`%s` implements `%s` at %s, and the call's arguments do not choose one for this bound: annotate them",
		concrete, c.shownInterfaceFor(concrete, bound, false), list))
	return true
}

// boundArgsError is a call whose bound's subject implements the bound's
// interface, but not with the type arguments the call gives it:
// `Range.step_by(1.0d..=1.3d, 0.1)` steps a Decimal range by a Float.
//
// The bound, the instance it is wanted at and the impls concrete has are
// rendered together (distinctTypeTexts), so `where T: From<b.Error>` beside
// an `impl From<a.Error>` names both Errors by their files.
func (c *checker) boundArgsError(line, col int, concrete Type, bound *InterfaceType, param string, subs map[*TypeParam_]Type, have []Type) {
	args := make([]Type, len(bound.TypeArgs))
	for i, a := range bound.TypeArgs {
		args[i] = resolveTypeVar(Substitute(a, subs))
	}
	wanted := &InterfaceType{Origin: bound.Origin, Name: bound.Name, TypeArgs: args}
	texts := distinctTypeTexts(c.entryName(), append([]Type{wanted, bound}, have...), nil)
	quoted := make([]string, len(have))
	for i, h := range texts[2:] {
		quoted[i] = "`" + h + "`"
	}
	hint := fmt.Sprintf("`%s` implements %s", concrete, strings.Join(quoted, " and "))
	for _, a := range args {
		if a == TypeInt || a == TypeFloat || a == TypeDecimal {
			hint += "; Int, Float and Decimal never convert implicitly"
			break
		}
	}
	e, _ := c.withConstructionReason(TypeError{Line: line, Col: col, Message: fmt.Sprintf(
		"%s does not implement %s (required by `where %s: %s`)",
		concrete, c.shownInterfaceAs(concrete, wanted, texts[0]), param, c.shownInterfaceAs(concrete, bound, texts[1]))}.WithHint(hint), concrete, wanted)
	c.report(e)
}
