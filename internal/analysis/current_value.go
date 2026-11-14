package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"
)

// A bare `..` (*ast.CurrentValue) is the spread of an update literal with no
// head: `{.., city: "NYC"}`, `{.., "k" => v}`, `[.., x]`, `[t, ..]`. It
// stands for the value already at the field or key the literal is written
// at, so
//
//	.{..user, address: .{.., city: "NYC"}}
//
// changes `address.city` and keeps the rest of `address`. A nested literal
// without `..` is a whole new value of the field's type. A `..` written as
// an operand anywhere in a field's value, a lambda's body included, is the
// value itself: `visits: .. + 1`, `street: rename(..)`. Each `..` belongs to
// the innermost field or key whose value holds it (ast.CurrentValuesOf), and
// its type is that position's.
//
// A bare `..` has a value only at a field or key of a literal that is itself
// an update: one with a named `..base`, one with a bare `..` of its own, or
// the literal patch of a `Struct.update` call. The update that owns the
// position records what each of its `..`s stands for (atUpdatePosition)
// before it checks the value, and checking a `..` reads that record; an
// update nested in the value records its own positions later, so the
// innermost wins. Before checking anything, an update also records each
// `..` in its field and key values as standing for a value of unknown type
// (atUpdateSyntax), so a `..` keeps its update when the update cannot be
// checked: a base of an unknown type, or a field the type lacks. Such a
// `..` has no type and reports nothing, since the error that stopped the
// update is reported already. Anywhere else the record is missing and the
// `..` is an error. At a map key the base may lack, an operand `..` is the empty
// collection when the value is one, and an error otherwise: there is no
// value to read and a missing key never traps.

// currentAt is what one bare `..` stands for.
type currentAt struct {
	// ty is the type of the value at the position, nil when the update
	// could not be checked.
	ty Type
	// missing is set when the position may hold no value: a map key the
	// base may lack, or any position under one. The update then builds the
	// value from nothing: an empty collection, or a struct from the fields
	// written and the struct's field defaults.
	missing bool
}

// atUpdatePosition records that value is written at a field or key of an
// update, where the old value has type ty (missing when it may be absent).
// A bare-`..` literal there gets its current value; a plain brace literal
// there is a whole new value, remembered so its errors can suggest `..`.
func (c *checker) atUpdatePosition(value ast.Node, ty Type, missing bool) {
	for _, cv := range ast.CurrentValuesOf(value) {
		if c.currentValues == nil {
			c.currentValues = map[*ast.CurrentValue]currentAt{}
		}
		c.currentValues[cv] = currentAt{ty: ty, missing: missing}
	}
	if cv, ok := value.(*ast.CurrentValue); ok && cv.Operand {
		c.report(errAt(cv, "a field or key whose value is `..` keeps the value it holds; leave it out"))
		return
	}
	if ast.UpdateHole(value) != nil {
		return
	}
	if lit, ok := value.(*ast.StructLit); ok && lit.TypeName == nil && lit.Spread == nil && len(lit.Fields) > 0 {
		if c.wholeAtUpdate == nil {
			c.wholeAtUpdate = map[*ast.StructLit]bool{}
		}
		c.wholeAtUpdate[lit] = true
	}
}

// atUpdateSyntax records that value is written at a field or key of an
// update, before the update's types are known: each `..` belonging to the
// position stands for a value of unknown type until atUpdatePosition gives
// it the position's type. A record already there is kept.
func (c *checker) atUpdateSyntax(value ast.Node) {
	for _, cv := range ast.CurrentValuesOf(value) {
		if c.currentValues == nil {
			c.currentValues = map[*ast.CurrentValue]currentAt{}
		}
		if _, ok := c.currentValues[cv]; !ok {
			c.currentValues[cv] = currentAt{}
		}
	}
}

// checkCurrentValue types a bare `..`: the type of the value at its
// position, or an error when the position has none.
func (c *checker) checkCurrentValue(n *ast.CurrentValue) Type {
	at, ok := c.currentValues[n]
	if !ok && n.Operand {
		c.report(errAt(n, "`..` means the value already at a field or key of an update, and this `..` is not in one").
			WithHint("write `..` in a field's new value, as in `{..user, visits: .. + 1}`, or name the value here"))
		return nil
	}
	if !ok {
		c.report(errAt(n, "a bare `..` has no value to update here").
			WithHint("a bare `..` stands for the value already at a field or key of an update, as in `{..user, address: {.., city: \"NYC\"}}`; anywhere else, name the base: `{..base, field: value}`"))
		return nil
	}
	if at.ty == nil {
		// The update could not be checked, and said why.
		return nil
	}
	if n.Operand && at.missing && !emptyCollectionType(at.ty) {
		c.report(errAt(n, c.typef("a missing key leaves `..` no %s to read", at.ty)).
			WithHint("the map may lack the key, and only a collection has a value to start from there, the empty one; to give a missing key a value, use `Map.update(m, key, default, f)`"))
		return at.ty
	}
	return at.ty
}

// emptyCollectionType reports whether ty is a list, set, map or vector:
// a type whose empty value a `..` at a missing key stands for.
func emptyCollectionType(ty Type) bool {
	if isListType(ty) || isMapType(ty) {
		return true
	}
	if _, ok := setElemType(ty); ok {
		return true
	}
	_, ok := vectorElemType(ty)
	return ok
}

// updateMissing reports whether lit is a bare-`..` update whose position may
// hold no value, so the positions inside it may hold none either.
func (c *checker) updateMissing(lit *ast.StructLit) bool {
	cv, ok := lit.Spread.(*ast.CurrentValue)
	return ok && c.currentValues[cv].missing
}

// holesAllowed reports whether the fields of the patch lit are update
// positions: lit is an update literal, or the literal patch of a
// `Struct.update` call. A patch passed as a value to another function's
// `Partial<T>` parameter has no old value at hand.
func (c *checker) holesAllowed(lit *ast.StructLit) bool {
	return lit.Spread != nil || c.structUpdatePatches[lit]
}

// emptyUpdateMessage is the error for an update literal whose bare `..` is
// followed by nothing: it changes nothing.
const emptyUpdateMessage = "an update with nothing after `..` changes nothing; leave this field or key out"

// checkUpdateBuildable reports the fields a bare-`..` struct update at a
// position that may hold no value cannot build: the fields it does not
// write that have no default. head is the struct's type.
func (c *checker) checkUpdateBuildable(n *ast.StructLit, head Type) {
	written := make(map[string]bool, len(n.Fields))
	for _, f := range n.Fields {
		written[f.Name] = true
	}
	var name string
	var missing []string
	switch h := head.(type) {
	case *StructType:
		canon := canonicalStructForFieldAccess(c, h)
		if canon == nil {
			return
		}
		name = canon.Name
		for _, f := range canon.Fields {
			if !written[f.Name] && !f.HasDefault {
				missing = append(missing, f.Name)
			}
		}
	case *AnonStructType:
		name = h.String()
		for _, f := range h.Fields {
			if !written[f.Name] {
				missing = append(missing, f.Name)
			}
		}
	default:
		return
	}
	if len(missing) == 0 {
		return
	}
	quoted := make([]string, len(missing))
	for i, m := range missing {
		quoted[i] = "'" + m + "'"
	}
	what := "field " + quoted[0] + " has no default"
	if len(quoted) > 1 {
		what = "fields " + strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1] + " have no default"
	}
	c.report(TypeError{Line: n.SpreadLine, Col: n.SpreadCol, Message: fmt.Sprintf(
		"a missing key leaves `..` no %s to update, and %s", name, what)}.
		WithHint("when the key is missing the update builds a whole value from the fields written and the field defaults; write the other fields, or give them defaults"))
}

// wholeValueHint is the help for a plain brace literal at an update position
// that does not fit the field: written with `..`, it would change only the
// fields it names.
func (c *checker) wholeValueHint(lit *ast.StructLit, ty Type) string {
	if !c.wholeAtUpdate[lit] {
		return ""
	}
	cp := *lit
	// The `..` stands where the literal opens, so the literal keeps the
	// layout it was written in.
	cp.Spread = &ast.CurrentValue{Line: lit.Line, Col: lit.Col + 1}
	cp.SpreadLine, cp.SpreadCol = lit.Line, lit.Col+1
	suggestion := format.RenderNode(&cp)
	if strings.Contains(suggestion, "\n") || len(suggestion) > 60 {
		names := make([]string, len(lit.Fields))
		for i, f := range lit.Fields {
			names[i] = f.Name + ": …"
		}
		suggestion = "{.., " + strings.Join(names, ", ") + "}"
	}
	return fmt.Sprintf("a brace without `..` is a whole new %s; to change only the fields written, write `%s`", formatTypeForError(ty), suggestion)
}

// markStructUpdatePatch records the literal patch of a `Struct.update` call,
// whose fields are update positions (holesAllowed).
func (c *checker) markStructUpdatePatch(call *ast.Call) {
	fa, ok := call.Func.(*ast.FieldAccess)
	if !ok || fa.Field == nil || fa.Field.Name != "update" {
		return
	}
	if ti, ok := fa.Object.(*ast.TypeIdent); !ok || ti.Name != "Struct" {
		return
	}
	var patch *ast.StructLit
	var original ast.Node
	for _, arg := range call.Args {
		if na, ok := arg.(*ast.NamedArg); ok {
			arg = na.Value
		}
		if lit, ok := arg.(*ast.StructLit); ok && lit.TypeName == nil && lit.Spread == nil {
			if c.structUpdatePatches == nil {
				c.structUpdatePatches = map[*ast.StructLit]bool{}
			}
			c.structUpdatePatches[lit] = true
			for _, f := range lit.Fields {
				c.atUpdateSyntax(f.Value)
			}
			patch = lit
		} else {
			original = arg
		}
	}
	// The patch updates the original, so a nested spread naming the
	// original's field is redundant there as in `{..original, ...}`.
	if patch != nil && original != nil && len(call.Args) == 2 {
		c.recordUpdatePath(patch, original)
	}
}

// checkPatchLit checks the brace literal lit passed for a `Partial<target>`
// parameter as a patch of target's fields: the field names and values by the
// struct literal validator, a field it leaves out is not missing, and a
// bare-`..` literal at a field updates that field (in a `Struct.update`
// call). ok is false when target is not a struct with fields, and the caller
// checks the literal as a plain value. The type is the patch's own, its
// fields at the types of their values, or nil when the check reported an
// error.
func (c *checker) checkPatchLit(lit *ast.StructLit, target Type) (Type, bool) {
	st := c.patchTargetStruct(target)
	if st == nil || len(lit.Fields) == 0 {
		return nil, false
	}
	errs := len(c.errors)
	c.checkStructLitAgainstStruct(lit, st, true)
	if len(c.errors) > errs {
		return nil, true
	}
	fields := make([]FieldDef, len(lit.Fields))
	for i, f := range lit.Fields {
		ty := c.fa.ExprTypes[f.Value]
		if ty == nil {
			return nil, true
		}
		fields[i] = FieldDef{Name: f.Name, Type: ty}
	}
	return &AnonStructType{Fields: fields}, true
}

// checkSetUpdateLit checks a set literal with spreads, `[..base, x]` or
// `[x, ..s, ..t]`: the union of the spreads and the elements. Each spread
// is a `Set<T>` or another Iter of T, and each element a T; the result has
// the first set spread's type (checkSpreadHeads). A lone spread that is a
// set is its base value; a lone other Iter (`[..maybe]`) makes a set of
// it.
func (c *checker) checkSetUpdateLit(n *ast.ListLit, expected Type) Type {
	head, allSets, ok := c.checkSpreadHeads(n.Spreads, expected, setSpreads)
	if !ok {
		for _, item := range n.Items {
			c.checkNode(item)
		}
		return nil
	}
	elemTy, _ := setElemType(head)
	if len(n.Items) == 0 && len(n.Spreads) == 1 && allSets {
		c.reportEmptySpread(n.Spreads[0], "a set spread with no elements")
		return head
	}
	for _, item := range n.Items {
		ty := c.checkNodeExpecting(item, elemTy)
		if ty != nil && !c.litOpenElemFits(elemTy, ty) && !(containsTypeVar(elemTy) && c.unify(elemTy, ty, nil) == nil) {
			c.report(errAt(item, c.typef("set spread element type mismatch: expected %s, got %s", resolveTypeVar(elemTy), ty)))
		}
	}
	return head
}

// checkVectorUpdateLit checks a vector literal with spreads: `[..v, x]`,
// `[x, ..v]`, `[..v, x, ..w]`, the spread vectors and the elements joined
// in the order written; a bare `..` (`[.., x]`, `[x, ..]`) stands for the
// vector at a field or key of an update. Each spread is a `Vector<T>` or
// another Iter of T, and each element a T; the result has the first vector
// spread's type (checkSpreadHeads). elemTy, when not nil, is the element type
// the context expects, which an open spread (`[..[], x]`) takes.
func (c *checker) checkVectorUpdateLit(n *ast.ListLit, elemTy Type) Type {
	var expected Type
	if elemTy != nil && !ContainsTypeParam(elemTy) && !containsTypeVar(elemTy) {
		expected = makeVectorType(elemTy, c.reg)
	}
	head, allVectors, ok := c.checkSpreadHeads(n.Spreads, expected, vectorSpreads)
	if !ok {
		for _, item := range n.Items {
			c.checkNode(item)
		}
		return nil
	}
	elemTy, _ = vectorElemType(head)
	if len(n.Items) == 0 && len(n.Spreads) == 1 && allVectors {
		c.reportEmptySpread(n.Spreads[0], "a vector spread with no elements")
		return head
	}
	for _, item := range n.Items {
		ty := c.checkNodeExpecting(item, elemTy)
		if ty != nil && !c.litElemFits(elemTy, ty, item) && !(containsTypeVar(elemTy) && c.unify(elemTy, ty, nil) == nil) {
			c.report(errAt(item, c.typef("vector element type mismatch: expected %s, got %s", resolveTypeVar(elemTy), ty)).WithHint(c.mismatchHint(elemTy, ty)))
		}
	}
	return head
}

// argCheckOrder is the order a generic call's arguments are checked in: as
// written, except that a positional function literal (a lambda, a capture,
// a field accessor) written before a named argument is checked after the
// named arguments, so `Iter.reduce(xs, 0, fn acc, x -> acc + x)` has
// solved the accumulator's type from `from` before the lambda's parameters
// are typed; and a `Struct.update` literal patch comes last, so the
// original, even when named after it, has solved `self` and the patch's
// fields are checked against it. Arguments are still evaluated as written.
func (c *checker) argCheckOrder(args []ast.Node) []int {
	order := make([]int, 0, len(args))
	var literals, patches []int
	lastNamed := -1
	for i, arg := range args {
		if _, ok := arg.(*ast.NamedArg); ok {
			lastNamed = i
		}
	}
	for i, arg := range args {
		if na, ok := arg.(*ast.NamedArg); ok {
			arg = na.Value
		} else if i < lastNamed && isLambdaArg(arg) {
			literals = append(literals, i)
			continue
		}
		if lit, ok := arg.(*ast.StructLit); ok && c.structUpdatePatches[lit] {
			patches = append(patches, i)
			continue
		}
		order = append(order, i)
	}
	order = append(order, literals...)
	return append(order, patches...)
}
