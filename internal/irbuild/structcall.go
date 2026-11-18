package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// `Foo({a: 1, b: "x"})` — the struct CALL form.
//
// # TWO argument shapes, and one field matcher
//
// `checkStructCallForm` (analysis/checker.go) admits exactly one argument in
// either of two shapes: an ANONYMOUS STRUCT LITERAL — a `*ast.StructLit`
// with no TypeName, or the empty `*ast.Block` that `{}` parses as — or a
// NON-LITERAL expression whose inferred type is a matching `AnonStructType`,
// which is what makes `MyApp(defaults())` legal beside `MyApp({...})`.
//
// The literal shape is the brace literal it already is, and structCallForm
// (irfuncbody.go) hands it to structMakeOf. The record shape has no
// field-value node to hand anywhere, only a value to read fields off. So
// structFromRecord evaluates the argument once, binds it to a name no program
// can spell, and builds a literal whose fields read that name's fields by
// name, which joins the literal path. Two ways in, one field matcher and one
// default fill, so the default rules have one copy.
//
// # Field order
//
// A record's field order is canonical, sorted by Nomi field name
// (anonstruct.go), while a nominal struct's is declaration order. The two
// disagree for any struct whose declaration is not alphabetical, so a
// positional copy of the record's fields into the struct's is wrong, and
// silently so when the field types agree. testdata/struct_forms.nomi builds a
// `Rev{z: Int, a: String}` from `{a: "s", z: 1}`: the two orders are reversed
// and the types cross.
//
// Going through the literal path means no order is computed here at all.
//
// # What is NOT claimed
//
// A PascalCase callee that is not a local struct — an enum variant spelled
// bare (`JInt(4)`, `Just(x)`), a generic struct, a stdlib struct — is declined,
// not refused, so `constructor call` keeps naming it. Those are different
// callee shapes with different causes, and one key for all of them would
// hide which one a program hits.

// anonArgAsLiteral is the call form's single argument as an anonymous struct
// literal WHEN IT IS ONE, positioned at the CALL's own site. A false answer
// is the record shape, which structFromRecord takes.
//
// A missing required field is reported against the literal this answers.
// A `{}` is a block, never a record: an anonymous struct
// has at least one field.
func anonArgAsLiteral(arg ast.Node) (*ast.StructLit, bool) {
	switch a := arg.(type) {
	case *ast.StructLit:
		if a.TypeName != nil {
			// `Foo(Bar{...})` — a NOMINAL literal in the argument. A type
			// error, and declined here rather than mis-read as a field set.
			return nil, false
		}
		return a, true
	}
	return nil, false
}
