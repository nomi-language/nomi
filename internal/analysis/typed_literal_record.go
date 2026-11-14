package analysis

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// A typed literal reads like a String: `Html"<p>${name}</p>"` is written
// as an interpolated string is, and nothing in it says that it is an
// `Html` built by a handler, or what its `${}` slots take (spec §18 *Typed
// literals*). The checker records both for each literal it resolves a
// handler for (FileAnalysis.TypedLiterals), and the editor's hover says
// them (internal/hoverdoc, typed_literals.go). A literal written where
// another type is expected gets a help line saying what it is
// (typedLiteralHint).

// TypedLiteral is what builds a typed literal and what its slots take.
type TypedLiteral struct {
	// Interface is the interface whose impl builds the literal, `Literal`
	// or `CheckedLiteral`, or "" for an `Html` template, which the
	// compiler reads itself (spec §18 *HTML templates*).
	Interface string
	// Slot is what each `${}` takes: the impl's `I`. Nil for an `Html`
	// template, whose slots take what their place in the HTML shows.
	Slot Type
	// HtmlSlots maps each slot expression of an `Html` template to what it
	// takes where it stands: the parameter of the std/html function its
	// context calls.
	HtmlSlots map[ast.Node]Type
}

// recordTypedLiteral records that n's handler is an impl of `Literal`, or
// of `CheckedLiteral` when checked is set, whose slots take slot.
func (c *checker) recordTypedLiteral(n *ast.TaggedString, checked bool, slot Type) {
	iface := "Literal"
	if checked {
		iface = "CheckedLiteral"
	}
	c.noteTypedLiteral(n, &TypedLiteral{Interface: iface, Slot: slot})
}

// recordHtmlLiteral records what each slot of the `Html` literal n takes,
// from the slot calls of its checked expansion. A `_maybe` slot function
// takes a `Maybe` of what its plain twin takes, which is what the slot is
// said to take.
func (c *checker) recordHtmlLiteral(n *ast.TaggedString, expansion ast.Node) {
	rec := &TypedLiteral{}
	for _, slot := range htmlSlotCalls(expansion) {
		name := htmlSlotCallee(slot)
		if name == nil {
			continue
		}
		sym, _ := c.htmlSlotFunc(slot, strings.TrimSuffix(*name, "_maybe"))
		if sym == nil {
			continue
		}
		ft, ok := sym.Type.(*FuncType)
		if !ok || len(ft.Params) == 0 {
			continue
		}
		if rec.HtmlSlots == nil {
			rec.HtmlSlots = map[ast.Node]Type{}
		}
		rec.HtmlSlots[slot.Args[len(slot.Args)-1]] = ft.Params[len(ft.Params)-1]
	}
	c.noteTypedLiteral(n, rec)
}

func (c *checker) noteTypedLiteral(n *ast.TaggedString, rec *TypedLiteral) {
	if c.fa.TypedLiterals == nil {
		c.fa.TypedLiterals = map[*ast.TaggedString]*TypedLiteral{}
	}
	c.fa.TypedLiterals[n] = rec
}

// typedLiteralHint is the help for a typed literal expr, of type got,
// written where expected is expected: the literal is what its handler
// builds, not the expected type, and when String is expected and the
// literal's type has a function that gives its text, that function.
func (c *checker) typedLiteralHint(expected, got Type, expr ast.Node) string {
	lit, ok := ungroupExpr(expr).(*ast.TaggedString)
	if !ok || got == nil || expected == nil {
		return ""
	}
	want, have := resolveTypeVar(expected), resolveTypeVar(got)
	if have == nil || want == nil || TypesEqual(want, have) || TypesEqual(have, TypeString) {
		return ""
	}
	if _, isVar := want.(*TypeVar); isVar {
		return ""
	}
	shown := c.typef("%s", want)
	wantText := articleFor(shown) + " `" + shown + "`"
	if TypesEqual(want, TypeString) {
		wantText = "a String"
	}
	name, origin := declaredTypeName(have)
	if name != lit.Tag {
		// The handler builds another type than the prefix, a
		// `Result<Date, Error>` for `Date"..."`.
		builder := "handler"
		if rec := c.fa.TypedLiterals[lit]; rec != nil && rec.Interface != "" {
			builder = "`" + rec.Interface + "` impl"
		}
		return c.typef("a typed literal is what `%s`'s %s builds (`%s`), not %s", lit.Tag, builder, have, wantText)
	}
	hint := c.typef("a typed literal is its prefix's type (`%s`), not %s", have, wantText)
	if !TypesEqual(want, TypeString) {
		return hint
	}
	if c.hasTextFunction(name, origin) {
		return hint + "; `" + name + ".to_string` gives its text"
	}
	if c.typeImplementsInterface(have, "Display", Pos{Line: lit.Line, Col: lit.Col}, RecordingKindCallSite) {
		return hint + "; `Display.to_string` gives its text"
	}
	return hint
}

// hasTextFunction reports whether the type name declared in origin has
// its own `to_string`, a function of one parameter that returns a String.
func (c *checker) hasTextFunction(name, origin string) bool {
	sym, _ := c.resolveTypeMethodSymbol(name, "to_string", origin)
	for sym != nil && sym.Resolved != nil {
		sym = sym.Resolved
	}
	if sym == nil || sym.IsImplMethod {
		return false
	}
	ft, ok := sym.Type.(*FuncType)
	return ok && len(ft.Params) == 1 && resolveTypeVar(ft.Return) == TypeString
}

// declaredTypeName is the name and declaring module of a declared type, or ""
// for any other type.
func declaredTypeName(t Type) (name, origin string) {
	switch v := t.(type) {
	case *StructType:
		return v.Name, v.Origin
	case *EnumType:
		return v.Name, v.Origin
	case *DistinctType:
		return v.Name, v.Origin
	}
	return "", ""
}
