package irbuild

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// captureShapeHost is the VM intrinsic behind std/strings' private
// `capture_shape<C>(re: Regex<C>, groups: List<Maybe<String>>) -> C`, which
// `String.captures` calls on a match's groups: the value of C they are, the
// tuple, anonymous struct, text, Unit or list the checker gave the regex's
// type (analysis/regex_literal.go). `Regex<C>` is one handle whatever C is
// (stdhost.go's phantom row), so C reaches the machine as the call's
// second operand, its shape spelled for internal/vm's captureshape.go; the
// third is the regex, whose group names place a struct's fields.
const captureShapeHost = "strings.capture_shape"

// captureShapeCall lowers a call to std/strings' `capture_shape`, in a body
// built per program, where C is a concrete type. handled is false for any
// other call.
func (bl *irScalarBuilder) captureShapeCall(t *ast.Call) (v ir.Temp, k kind, mobile, ok, handled bool) {
	id, bare := t.Func.(*ast.Ident)
	if !bare || id.Name != "capture_shape" || len(t.Args) != 2 {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	fa := bl.g.srcFA()
	if fa == nil || fa.Origin != "std/strings" {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	if sym := resolvedBareSymbolAt(fa, id); sym == nil {
		return ir.NoTemp, kindInvalid, false, false, false
	} else if _, host := sym.Node.(*ast.ExternFunc); !host {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	result := bl.g.checkedKind(t)
	shape, shaped := captureShape(bl.g.irValType(result))
	if !shaped {
		irDeclineNote("capture_shape at a type that is not a regex capture type")
		return ir.NoTemp, kindInvalid, false, false, true
	}
	re, _, _, lowered := bl.lower(argExpr(t.Args[0]))
	if !lowered {
		return ir.NoTemp, kindInvalid, false, false, true
	}
	groups, _, _, lowered := bl.lower(argExpr(t.Args[1]))
	if !lowered {
		return ir.NoTemp, kindInvalid, false, false, true
	}
	pos := bl.g.irNodePos(t)
	spec := ir.NewString(pos, bl.f.NewTemp(), shape)
	bl.b.Append(spec)
	c := ir.NewHostCall(pos, bl.f.NewTemp(), irCallSite(t), bl.g.irCalleeSym(captureShapeHost, captureShapeHost), groups, spec.Dst(), re)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: result})
	return c.Dst(), result, false, true, true
}

// captureShape spells a capture type for the VM (internal/vm's
// captureshape.go): `L` for the run-time List<Maybe<String>>, `U` for Unit,
// `S` or `M` for one group's String or Maybe<String>, `T` and a letter per
// tuple component, `R` and `name:letter` per struct field.
func captureShape(t *ir.ValType) (string, bool) {
	if t == nil {
		return "", false
	}
	part := func(e *ir.ValType) (string, bool) {
		switch {
		case e.Kind() == ir.KindString:
			return "S", true
		case e.Kind() == ir.KindEnum && e.NumElems() == 1 && e.Elem(0).Kind() == ir.KindString:
			return "M", true
		}
		return "", false
	}
	switch t.Kind() {
	case ir.KindList:
		return "L", true
	case ir.KindUnit:
		return "U", true
	case ir.KindString, ir.KindEnum:
		return part(t)
	case ir.KindTuple:
		var b strings.Builder
		b.WriteByte('T')
		for i := range t.NumElems() {
			p, ok := part(t.Elem(i))
			if !ok {
				return "", false
			}
			b.WriteString(p)
		}
		return b.String(), true
	case ir.KindRecord:
		fields := make([]string, t.NumElems())
		for i, name := range t.Names() {
			p, ok := part(t.Elem(i))
			if !ok {
				return "", false
			}
			fields[i] = name + ":" + p
		}
		return "R" + strings.Join(fields, ","), true
	}
	return "", false
}
