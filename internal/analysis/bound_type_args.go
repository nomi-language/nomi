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
		if info == nil || !implReceiverMatches(info, concrete, c.implsContext(), c.implTypeArgsContext()) {
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
func (c *checker) boundArgsFit(concrete Type, bound *InterfaceType, subs map[*TypeParam_]Type) (bool, []string) {
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
	var have []string
	for _, args := range lists {
		if len(args) != len(want) {
			continue
		}
		if typesAllEqual(args, want) {
			return true, nil
		}
		// The impl tables of several scopes can each record the same impl.
		if s := (&InterfaceType{Name: bound.Name, TypeArgs: args}).String(); !slices.Contains(have, s) {
			have = append(have, s)
		}
	}
	if len(have) == 0 {
		return true, nil
	}
	return false, have
}

// boundArgsError is a call whose bound's subject implements the bound's
// interface, but not with the type arguments the call gives it:
// `Range.step_by(1.0d..=1.3d, 0.1)` steps a Decimal range by a Float.
func (c *checker) boundArgsError(line, col int, concrete Type, bound *InterfaceType, param string, subs map[*TypeParam_]Type, have []string) {
	args := make([]Type, len(bound.TypeArgs))
	for i, a := range bound.TypeArgs {
		args[i] = resolveTypeVar(Substitute(a, subs))
	}
	wanted := &InterfaceType{Name: bound.Name, TypeArgs: args}
	quoted := make([]string, len(have))
	for i, h := range have {
		quoted[i] = "`" + h + "`"
	}
	hint := fmt.Sprintf("`%s` implements %s", concrete, strings.Join(quoted, " and "))
	for _, a := range args {
		if a == TypeInt || a == TypeFloat || a == TypeDecimal {
			hint += "; Int, Float and Decimal never convert implicitly"
			break
		}
	}
	c.report(TypeError{Line: line, Col: col, Message: fmt.Sprintf(
		"%s does not implement %s (required by `where %s: %s`)",
		concrete, wanted, param, bound)}.WithHint(hint))
}
