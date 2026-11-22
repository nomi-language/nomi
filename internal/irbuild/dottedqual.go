package irbuild

import "github.com/nomi-language/nomi/internal/ast"

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
// The candidate is the OUTERMOST FieldAccess's Object — one chain, not a set
// of prefixes to try. `Probe.Reading.Steady` offers `Probe.Reading`
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

// dottedTypeName reports whether n, a FieldAccess chain rooted at a type
// name, a module qualifier or a directory path, names a type: the checker
// resolved the chain's last name to a type declaration (dottedTypeRef).
//
// The checker's reference is the arm's identity check: `random.Seed` has the
// same shape and is NOT a type this program declares, so it must fall
// through to the std module's route rather than be lowered on the strength
// of looking dotted.
//
// # The Ident root is a module qualifier
//
// The checker resolves `shapes.Shape` through the qualifier's own module
// scope, so an Ident-rooted chain names a type exactly when the root is a
// whole-file import. A loop that stopped at the Ident would send
// `shapes.Colour.Red` in expression position to native.go's `file member`
// refusal.
//
// THE LOCAL-BINDING CHECK IS WHAT KEEPS AN ORDINARY FIELD CHAIN OUT, and it is
// a decline rather than a lookup so `a.b.c` over a local `a` costs nothing new.
// It is also the shadowing rule: a binding of the qualifier's name, which the
// checker resolved the root to (namesLocal), shadows the file's import of
// it, and resolving through the import anyway would read a module where the
// programmer wrote a value.
//
// THE AUTHORITY IS THE ANALYZER, NOT THE PARSER: `parser.
// parseQualifiedTypeDeclName` ACCEPTS a `token.IDENT` segment, so
// `pub enum probe.Reading { … }` parses. It is `analysis/checker.go`'s
// `isPascalCase` loop that refuses it, with `enum "probe.Reading" must use
// PascalCase segments`.
func (bl *irScalarBuilder) dottedTypeName(n ast.Node) bool {
	fa, isFA := n.(*ast.FieldAccess)
	if !isFA || fa.Field == nil {
		return false
	}
	obj := fa.Object
	for {
		switch o := obj.(type) {
		case *ast.TypeIdent, *ast.FilePath:
			// A namespaced declaration's root, or a path at a use, whose
			// directory import binds the file under it.
		case *ast.Ident:
			// A MODULE QUALIFIER, or the root of an ordinary field chain. A
			// name this scope binds is a VALUE and never a qualifier.
			if bl.namesLocal(o) {
				return false
			}
		case *ast.FieldAccess:
			if o.Field == nil {
				panic("irbuild: dottedTypeName: parser always sets FieldAccess.Field (parser.go:1803/1817/2734/3450); a nil Field never reaches the builder from an accepted program")
			}
			obj = o.Object
			continue
		default:
			return false
		}
		_, namespaced := obj.(*ast.TypeIdent)
		return bl.g.dottedTypeRef(fa, namespaced)
	}
}

// dottedTypeRef reports whether the checker resolved fa's last name to a
// type declaration this gen has a def of (typeDeclDef). Through a module
// qualifier (namespaced false) it must be a program file's declaration
// (programTypeDef): a std type named through its module's qualifier is the
// std owner route's, which names it by its module.
func (g *gen) dottedTypeRef(fa *ast.FieldAccess, namespaced bool) bool {
	sym := g.checkedRef(fa.Field.Line, fa.Field.Col)
	if g.typeDeclDef(sym) == nil {
		return false
	}
	if namespaced {
		return true
	}
	_, program := g.programTypeDef(sym.Type)
	return program
}
