package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"
)

// A bare `..` (*ast.CurrentValue) is the spread of an update literal with no
// head: `#{.., city: "NYC"}`, `%{.., "k" => v}`, `%[.., x]`, `[t, ..]`. It
// stands for the value already at the field or key the literal is written
// at, so
//
//	#{..user, address: #{.., city: "NYC"}}
//
// changes `address.city` and keeps the rest of `address`. A nested literal
// without `..` is a whole new value of the field's type.
//
// A bare `..` has a value only at a field or key of a literal that is itself
// an update: one with a named `..base`, one with a bare `..` of its own, or
// the literal patch of a `Struct.update` call. The update that owns the
// position records what the `..` stands for (atUpdatePosition) before it
// checks the value, and checking the `..` reads that record. Anywhere else
// the record is missing and the `..` is an error.

// currentAt is what one bare `..` stands for.
type currentAt struct {
	// ty is the type of the value at the position.
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
	if cv := ast.UpdateHole(value); cv != nil {
		if c.currentValues == nil {
			c.currentValues = map[*ast.CurrentValue]currentAt{}
		}
		c.currentValues[cv] = currentAt{ty: ty, missing: missing}
		return
	}
	if lit, ok := value.(*ast.StructLit); ok && lit.TypeName == nil && lit.Spread == nil && len(lit.Fields) > 0 {
		if c.wholeAtUpdate == nil {
			c.wholeAtUpdate = map[*ast.StructLit]bool{}
		}
		c.wholeAtUpdate[lit] = true
	}
}

// checkCurrentValue types a bare `..`: the type of the value at its
// position, or an error when the position has none.
func (c *checker) checkCurrentValue(n *ast.CurrentValue) Type {
	at, ok := c.currentValues[n]
	if !ok {
		c.report(errAt(n, "a bare `..` has no value to update here").
			WithHint("a bare `..` stands for the value already at a field or key of an update, as in `#{..user, address: #{.., city: \"NYC\"}}`; anywhere else, name the base: `#{..base, field: value}`"))
		return nil
	}
	return at.ty
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
	cp.Spread = &ast.CurrentValue{Line: lit.Line, Col: lit.Col + 2}
	cp.SpreadLine, cp.SpreadCol = lit.Line, lit.Col+2
	suggestion := format.RenderNode(&cp)
	if strings.Contains(suggestion, "\n") || len(suggestion) > 60 {
		names := make([]string, len(lit.Fields))
		for i, f := range lit.Fields {
			names[i] = f.Name + ": …"
		}
		suggestion = "#{.., " + strings.Join(names, ", ") + "}"
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
	for _, arg := range call.Args {
		if na, ok := arg.(*ast.NamedArg); ok {
			arg = na.Value
		}
		if lit, ok := arg.(*ast.StructLit); ok && lit.TypeName == nil && lit.Spread == nil {
			if c.structUpdatePatches == nil {
				c.structUpdatePatches = map[*ast.StructLit]bool{}
			}
			c.structUpdatePatches[lit] = true
		}
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

// checkSetUpdateLit checks a set update, `%[..base, x]`, which means
// `Set.union(base, %[x])`. The base must be a `Set<T>` and each element a T;
// the result has the base's type.
func (c *checker) checkSetUpdateLit(n *ast.SetLit, expected Type) Type {
	var head Type
	if expected != nil {
		head = c.checkNodeExpecting(n.Spread, expected)
	} else {
		head = c.checkNode(n.Spread)
	}
	head = resolveTypeVar(head)
	line, col := n.SpreadLine, n.SpreadCol
	if line == 0 {
		line, col = n.Line, n.Col
	}
	elemTy, ok := setElemType(head)
	if !ok {
		if head != nil {
			c.addError(line, col, fmt.Sprintf("set spread `..` requires a set, got %s", formatTypeForError(head)))
		}
		for _, item := range n.Items {
			c.checkNode(item)
		}
		return nil
	}
	if len(n.Items) == 0 {
		if _, bare := n.Spread.(*ast.CurrentValue); bare {
			c.addError(line, col, emptyUpdateMessage)
		} else {
			base := "the value itself"
			if id, ok := n.Spread.(*ast.Ident); ok {
				base = "`" + id.Name + "`"
			}
			c.addError(line, col, "a set spread with no elements is its base value; write "+base)
		}
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

// checkVectorUpdateLit checks a vector with a spread at either end:
// `#[..v, x]` is `Vector.concat(v, #[x])` and `#[x, ..v]` is
// `Vector.concat(#[x], v)`; a bare `..` (`#[.., x]`, `#[x, ..]`) stands for
// the vector at a field or key of an update. The spread must be a
// `Vector<T>` and each element a T; the result has the spread's type. elemTy,
// when not nil, is the element type the context expects, which an open
// spread (`#[..#[], x]`) takes.
func (c *checker) checkVectorUpdateLit(n *ast.VectorLit, elemTy Type) Type {
	_, bare := n.Spread.(*ast.CurrentValue)
	var head Type
	if elemTy != nil && !bare && !ContainsTypeParam(elemTy) && !containsTypeVar(elemTy) {
		head = c.checkNodeExpecting(n.Spread, makeVectorType(elemTy, c.reg))
	} else {
		head = c.checkNode(n.Spread)
	}
	head = resolveTypeVar(head)
	elemTy, ok := vectorElemType(head)
	if !ok {
		if head != nil {
			e := TypeError{Line: n.SpreadLine, Col: n.SpreadCol, Message: fmt.Sprintf("vector spread `..` requires a vector, got %s", formatTypeForError(head))}
			if !bare {
				e = e.WithHint("make a vector of another collection's elements with `Iter.to_vector`, as in `xs |> Iter.to_vector()`")
			}
			c.report(e)
		}
		for _, item := range n.Items {
			c.checkNode(item)
		}
		return nil
	}
	if len(n.Items) == 0 {
		if bare {
			c.addError(n.SpreadLine, n.SpreadCol, emptyUpdateMessage)
		} else {
			base := "the value itself"
			if id, ok := n.Spread.(*ast.Ident); ok {
				base = "`" + id.Name + "`"
			}
			c.addError(n.SpreadLine, n.SpreadCol, "a vector spread with no elements is its base value; write "+base)
		}
		return head
	}
	for _, item := range n.Items {
		ty := c.checkNodeExpecting(item, elemTy)
		if ty != nil && !c.litElemFits(elemTy, ty, item) && !(containsTypeVar(elemTy) && c.unify(elemTy, ty, nil) == nil) {
			c.report(errAt(item, c.typef("vector element type mismatch: expected %s, got %s", resolveTypeVar(elemTy), ty)).WithHint(mismatchHint(elemTy, ty)))
		}
	}
	return head
}

// argCheckOrder is the order a generic call's arguments are checked in: as
// written, except that a positional function literal (a lambda, a capture,
// a field accessor) written before a named argument is checked after the
// named arguments, so `Iter.reduce(xs, fn acc, x -> acc + x, from: 0)` has
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
