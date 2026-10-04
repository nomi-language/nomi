package analysis

import "fmt"

// declaredArity reports how many type arguments a nominal type's declaration
// requires, and how many the reference being resolved supplied.
//
// The four nominal kinds each carry the declaration's parameter names in
// TypeParams and the use site's arguments in TypeArgs, so one shape answers
// for all of them. A type PARAMETER reference (`T` inside `struct Box<T>`)
// never reaches here: resolveTypeExpr consults the typeParams map before the
// registry, so `T` returns a *TypeParam_, which is not one of these kinds.
func declaredArity(t Type) (name string, want, got int, isGeneric bool) {
	switch n := t.(type) {
	case *StructType:
		return n.Name, len(n.TypeParams), len(n.TypeArgs), len(n.TypeParams) > 0
	case *EnumType:
		return n.Name, len(n.TypeParams), len(n.TypeArgs), len(n.TypeParams) > 0
	case *InterfaceType:
		return n.Name, len(n.TypeParams), len(n.TypeArgs), len(n.TypeParams) > 0
	case *DistinctType:
		return n.Name, len(n.TypeParams), len(n.TypeArgs), len(n.TypeParams) > 0
	}
	return "", 0, 0, false
}

// arityMessage names the declaration and both counts, because the two ways to
// get this wrong need different fixes: `got 0` means an argument is missing
// from a signature, `got 2` means one too many was written.
func arityMessage(name string, want, got int) string {
	unit := "type arguments"
	if want == 1 {
		unit = "type argument"
	}
	return fmt.Sprintf("`%s` expects %d %s, got %d", name, want, unit, got)
}

// checkGenericArity is the decision for a reference that supplied no type
// arguments: `got` is read off the resolved type, so a registry entry that is
// already instantiated is not a violation.
func checkGenericArity(t Type, line, col int) *TypeError {
	name, want, got, isGeneric := declaredArity(t)
	if !isGeneric || want == got {
		return nil
	}
	return &TypeError{Line: line, Col: col, Message: arityMessage(name, want, got)}
}

// checkGenericArityArgs is the same decision for a reference that DID supply
// arguments — `Box<Int, Int>` against a one-parameter declaration.
func checkGenericArityArgs(t Type, got, line, col int) *TypeError {
	name, want, _, isGeneric := declaredArity(t)
	if !isGeneric || want == got {
		return nil
	}
	return &TypeError{Line: line, Col: col, Message: arityMessage(name, want, got)}
}
