package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// Construction of a distinct type through its call form: `Id(5)` for
// `type Id Int`, piped `5 |> Id()`, and the embedded spelling `Shape.Id(5)`
// (variant_construction.go). The one argument is the value the distinct
// wraps, so it is checked against the inner type. A zero-sized type
// (`type Expired`) has no call form: it is its own value, written bare.
//
// Tuple-distincts keep checkTupleDistinctArgs, which also takes the flat
// `Pair(1, "x")` form. A call with a `&1` placeholder is partial application
// and keeps the ordinary path.

// distinctCtorLabel is how a diagnostic spells the constructor: the callee as
// written (`Id`, `ids.Id`).
func distinctCtorLabel(callee ast.Node, dt *DistinctType) string {
	switch fn := callee.(type) {
	case *ast.TypeIdent:
		return fn.Name
	case *ast.FieldAccess:
		if fn.Field != nil {
			switch obj := fn.Object.(type) {
			case *ast.Ident:
				return obj.Name + "." + fn.Field.Name
			case *ast.TypeIdent:
				return obj.Name + "." + fn.Field.Name
			}
			return fn.Field.Name
		}
	}
	return dt.Name
}

// checkDistinctCtorCall checks `D(args)` for a distinct D called by its type
// name. Tuple-distincts are routed by the caller.
func (c *checker) checkDistinctCtorCall(n *ast.Call, dt *DistinctType) Type {
	dt = c.freshDistinct(dt)
	label := distinctCtorLabel(n.Func, dt)
	line, col := nodeLineCol(n.Func)
	if line == 0 {
		line, col = n.Line, 1
	}
	if dt.Inner == nil {
		c.checkArgs(n.Args)
		c.addError(line, col, zeroSizedTypeCallMessage(label))
		return dt
	}
	if len(n.Args) != 1 || isNamedArg(n.Args[0]) {
		c.checkArgs(n.Args)
		c.addError(line, col, fmt.Sprintf(
			"%s wraps %s, so %s(...) takes one argument, %s %s; got %d",
			label, formatTypeForError(dt.Inner), label,
			articleFor(formatTypeForError(dt.Inner)), formatTypeForError(dt.Inner), len(n.Args)))
		return dt
	}
	argTy := c.checkNodeExpecting(n.Args[0], distinctInnerOf(dt))
	c.checkDistinctInner(n.Args[0], argTy, label, dt)
	return dt
}

// freshDistinct is a generic distinct's declaration instantiated with a fresh
// type variable per type parameter, which the constructor's argument (and
// the type the construction is expected to have) then solve: `Tagged(#("n",
// 1))` is a `Tagged<Int>`. A non-generic or already instantiated one is
// itself.
func (c *checker) freshDistinct(dt *DistinctType) *DistinctType {
	if len(dt.TypeParamDefs) == 0 || len(dt.TypeArgs) == len(dt.TypeParamDefs) {
		return dt
	}
	args := make([]Type, len(dt.TypeParamDefs))
	for i := range args {
		args[i] = c.freshTypeVar()
	}
	return &DistinctType{
		Origin:           dt.Origin,
		Name:             dt.Name,
		Inner:            dt.Inner,
		Opaque:           dt.Opaque,
		OwningSourceFile: dt.OwningSourceFile,
		TypeParams:       dt.TypeParams,
		TypeParamDefs:    dt.TypeParamDefs,
		TypeArgs:         args,
	}
}

// checkPipedDistinctCtor checks `value |> D()`: the piped value is the
// construction's one argument.
func (c *checker) checkPipedDistinctCtor(call *ast.Call, piped ast.Node, pipedTy Type, dt *DistinctType) Type {
	dt = c.freshDistinct(dt)
	label := distinctCtorLabel(call.Func, dt)
	line, col := nodeLineCol(call.Func)
	if line == 0 {
		line, col = call.Line, 1
	}
	if dt.Inner == nil {
		c.checkArgs(call.Args)
		c.addError(line, col, zeroSizedTypeCallMessage(label))
		return dt
	}
	c.checkOpaqueConstructor(call.Func, dt)
	// A tuple-distinct takes the piped tuple whole, as a variant over a
	// tuple does: `#(1, 2) |> Pair()`. The flat form is a prefix call only.
	if len(call.Args) != 0 {
		c.checkArgs(call.Args)
		c.addError(line, col, fmt.Sprintf(
			"%s takes the piped value as its only argument, got %d more", label, len(call.Args)))
		return dt
	}
	c.checkDistinctInner(piped, pipedTy, label, dt)
	return dt
}

// checkDistinctInner checks one already-typed argument against the type the
// distinct wraps. ctor is how the constructor was spelled (`Id`,
// `Shape.Id`), which the message names beside the distinct itself.
func (c *checker) checkDistinctInner(at ast.Node, argTy Type, ctor string, dt *DistinctType) {
	if argTy == nil {
		return
	}
	line, col := startOf(at)
	inner := distinctInnerOf(dt)
	if ContainsTypeParam(inner) || containsTypeVar(inner) {
		if err := c.unify(inner, argTy, map[*TypeParam_]Type{}); err != nil {
			c.addError(line, col, distinctArgMessage(ctor, dt, argTy))
		}
		return
	}
	if !c.argMatchesParam(argTy, inner, c.recPos(line, col), RecordingKindCallSite) {
		c.addError(line, col, distinctArgMessage(ctor, dt, argTy))
	}
}

func distinctArgMessage(ctor string, dt *DistinctType, argTy Type) string {
	inner := formatTypeForError(distinctInnerOf(dt))
	if ctor == dt.Name {
		return fmt.Sprintf("%s wraps %s, so %s(...) takes %s %s; got %s",
			dt.Name, inner, ctor, articleFor(inner), inner, formatTypeForError(argTy))
	}
	return fmt.Sprintf("%s builds %s %s, which wraps %s, so %s(...) takes %s %s; got %s",
		ctor, articleFor(dt.Name), dt.Name, inner, ctor, articleFor(inner), inner, formatTypeForError(argTy))
}

func zeroSizedTypeCallMessage(label string) string {
	return fmt.Sprintf("%s is a zero-sized type and takes no arguments; write %s", label, label)
}
