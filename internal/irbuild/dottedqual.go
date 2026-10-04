package irbuild

import (
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// The VALUE side of a NAMESPACED (dotted) type name: `Probe.Reading.Steady` and
// `Probe.Reading.Spike(3)`.
//
// The TYPE side resolves elsewhere: `buildTypes` registers a namespaced
// declaration under its whole dotted spelling, and typeOf reads
// *ast.QualifiedType, so a dotted name lowers in a parameter, a return, inside
// a container and inside a tuple. This file handles the one node shape in
// expression position.
//
// # The shape
//
// A dotted name in expression position is a `*ast.FieldAccess` whose Object is
// itself a `*ast.FieldAccess` — `((Probe . Reading) . Steady)`. An arm that
// reads the Object as an `*ast.TypeIdent` gives up on it, and which refusal it
// reports then depends on whether the LEADING segment happens to resolve and
// whether the variant carries data, not on the dotted name:
//
//	Probe.Reading.Steady     type-qualified member
//	Probe.Reading.Spike(3)   qualified call
//	Probe.Local.On           type-qualified reference (owner absent, as when
//	                         `import telemetry.Probe.Reading` binds
//	                         `Probe.Reading` and never `Probe`)
//
// The other dotted spellings go through their own arms: the struct-shaped
// variant literal `Probe.Reading.Blip{at: 9}` (structLit has a
// *ast.QualifiedType arm), every PATTERN spelling including
// `Some(Probe.Reading.Spike(n))` nested inside a prelude variant, the
// dot-shorthand `.Spike(5)` under an annotation, and `==` on two dotted values.
//
// # WHY RESOLUTION IS EXACT RATHER THAN A SEARCH
//
// The candidate is the OUTERMOST FieldAccess's Object, flattened — one string,
// not a set of prefixes to try. `Probe.Reading.Steady` offers `Probe.Reading`
// and nothing else, because the last segment is the member being named and
// every earlier one is part of the type's own name. Nomi has no `.method()`
// postfix form and no nested-module member access, so there is no second
// reading of the same syntax for a longer or shorter split to find.
//
// The root is a `*ast.TypeIdent` for a namespaced declaration and an
// `*ast.Ident` for a MODULE qualifier. An ordinary `a.b.c` field chain has an
// Ident root too, so the two are told apart by the lookup rather than by the
// node shape: a locally bound root is a value and declines before any lookup,
// and a root that is not bound resolves only if the whole spelling names a
// type.

// dottedTypeQualifier flattens a namespaced type name written as a FieldAccess
// chain rooted at a type name, and answers only when the whole spelling names a
// type this gen can resolve.
//
// The resolution is part of the answer rather than left to the caller, and that
// is the arm's identity check: `random.Seed` flattens to the same shape and is
// NOT a type this gen holds, so it must fall through to the refusal that says so
// rather than be lowered on the strength of looking dotted.
//
// # The Ident root is a module qualifier
//
// `namedType` resolves `shapes.Shape` through the qualifier's own module scope
// (modulequaltype.go), so an Ident-rooted spelling names a type exactly when
// the root is a whole-file import. A loop that stopped at the Ident would send
// `shapes.Colour.Red` in expression position to native.go's `file member`
// refusal.
//
// THE LOCAL-BINDING CHECK IS WHAT KEEPS AN ORDINARY FIELD CHAIN OUT, and it is
// a decline rather than a lookup so `a.b.c` over a local `a` costs nothing new.
// It is also the shadowing rule: `moduleScopeOf` reads the FILE's module scope,
// which a block-local binding of the same name shadows, and resolving through
// it anyway would read a module where the programmer wrote a value.
//
// THE AUTHORITY IS THE ANALYZER, NOT THE PARSER: `parser.
// parseQualifiedTypeDeclName` ACCEPTS a `token.IDENT` segment, so
// `pub enum probe.Reading { … }` parses. It is `analysis/checker.go`'s
// `isPascalCase` loop that refuses it, with `enum "probe.Reading" must use
// PascalCase segments`.
func (g *gen) dottedTypeQualifier(n ast.Node) (string, bool) {
	fa, isFA := n.(*ast.FieldAccess)
	if !isFA || fa.Field == nil {
		return "", false
	}
	segs := []string{fa.Field.Name}
	obj := fa.Object
	for {
		var root string
		switch o := obj.(type) {
		case *ast.TypeIdent:
			root = o.Name
		case *ast.Ident:
			// A MODULE QUALIFIER, or the root of an ordinary field chain. A
			// name this scope binds is a VALUE and never a qualifier.
			if _, bound := g.lookup(o.Name); bound {
				return "", false
			}
			root = o.Name
		case *ast.FieldAccess:
			if o.Field == nil {
				return "", false
			}
			segs = append(segs, o.Field.Name)
			obj = o.Object
			continue
		default:
			return "", false
		}
		segs = append(segs, root)
		slices.Reverse(segs)
		name := strings.Join(segs, ".")
		if _, known := g.namedType(name); !known {
			return "", false
		}
		return name, true
	}
}
