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
// The literal shape is re-spelled as the literal it already is and handed to
// `structValue`. The record shape cannot be: there is no field-value NODE to
// hand anywhere, only a value to read fields off. So it spills the argument
// to one temporary and projects each declared field by NAME, then joins the
// literal path at `fillFieldDefaults` and `structFromValues` — which is
// where every default rule and the declaration-order lowering live. Two ways
// in, one field matcher and one default fill, because a second copy of the
// default rules is the shape this package keeps deleting.
//
// ONE Go EVALUATION of the argument. A projection's Go code re-embeds its
// subject TEXTUALLY (irproj.go), so an impure operand read once per field
// would be called once per field. The spill in structCallRecord is what
// makes that impossible. It is a backstop: `g.operand` already hands back a
// named local for every record shape the front end admits here (a call, a
// binding, an `if`, a `case`). It stays for the reason coercetuple.go keeps
// the identical guard where it spreads a tuple: the multi-read is correct by
// construction instead of by a purity fact about a neighbouring pass.
//
// # Field ORDER is where a plausible wrong implementation lands
//
// A record's Go layout is CANONICAL — sorted by Nomi field name (anonstruct.go)
// — while a nominal struct's is DECLARATION order. The two disagree for any
// struct whose declaration is not alphabetical, so a positional copy of the
// record's fields into the struct's is wrong. It is not silently wrong when the
// field types differ (Go rejects it) and it IS silently wrong when they do not,
// which is why testdata/struct_forms.nomi builds a `Rev {z: Int, a: String}`
// from `{a: "s", z: 1}`: the two orders are reversed and the types cross.
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
// is the record shape, which structCallRecord takes.
//
// The position matters and is not cosmetic: a missing required field is
// reported against the node handed to `litFieldValues`, and the empty form has
// no literal node of its own at all — `Foo({})` parses its argument as a
// zero-statement `*ast.Block`, which is the same accommodation
// `checkStructCallForm` makes and for the same reason.
func anonArgAsLiteral(arg ast.Node) (*ast.StructLit, bool) {
	switch a := arg.(type) {
	case *ast.StructLit:
		if a.TypeName != nil {
			// `Foo(Bar{...})` — a NOMINAL literal in the argument. A type
			// error, and declined here rather than mis-read as a field set.
			return nil, false
		}
		return a, true
	case *ast.Block:
		if len(a.Stmts) != 0 {
			return nil, false
		}
		return &ast.StructLit{Line: a.Line, Col: a.Col}, true
	}
	return nil, false
}
