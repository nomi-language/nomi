package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// What DERIVE SYNTHESIS wrote, resolved without a source position.
//
// Two arms, one cause. Derive synthesis builds a fake AST and stamps every node
// in it with a position from a reserved band, and the front end deliberately
// treats those positions as non-source. Any builder lookup keyed on a position
// therefore misses for every node in a synthesized body or signature, and the
// miss surfaces four positions away from its cause: the item refuses, the whole
// impl block refuses, the block is not indexed, and the CALL is reported as
// `type-qualified member | T.method: T is a struct`.
//
// # (1) A prelude enum named by a type annotation
//
// `derive FromJson for User` synthesizes
// `fn from_json(json: Json): Result<User, Json.ShapeError>`. Both type
// arguments are representable — `User` is a declared struct and
// `Json.ShapeError` is a `stdStructSpecs` row over `rt.JsonShapeError` — and the
// SAME annotation in a hand-written `fn f(): Result<User, Json.ShapeError>`
// lowers with no refusal at all. If the synthesized one answered kindInvalid,
// implItemSig would refuse the whole block `generic type`, `d.order` would be
// empty, resolveImplMembers would index nothing, and `User.from_json(x)` would
// find no candidate and be reported at the CALL as
// `type-qualified member | User.from_json: User is a struct`.
//
// # Why the position route cannot answer, and why that is not a bug to fix there
//
// preludeAt resolves a prelude enum by reading `fa.References` at the node's
// own (line, col). Derive synthesis stamps every node it writes into a fake
// position band starting at `synthLineBase = 1<<30` (derive_synthesis.go), and
// analysis/builder.go DELIBERATELY keeps that band out of the
// References/Definitions maps: a synthesized type reference is not navigable
// source, and admitting it would make LSP rename and find-references emit a
// position no editor can open. builder.go drops synth-band manifest recordings
// for the same class of reason.
//
// So this is a STRUCTURAL wall rather than unwritten work, and the front end is
// right. The builder asked for a promise it was never given, and widening the
// exclusion to satisfy a builder lookup would trade a real user-facing
// behaviour for a builder convenience. The fix belongs on this side of the line.
//
// # Why resolving by NAME is sound here
//
// preludeByName is built by preludeAnchorsOf from `fa.ModuleScope` — a
// different map from `fa.References`, untouched by the exclusion, and already
// the identity oracle for every other prelude construct that has no position to
// offer (iter.go's `Iter` terminals, lists.go's maybeOf, maps.go's `Map.get`,
// preludefn.go's owner check).
//
// SHADOWING is the question a reader should ask, and the answer is already
// pinned by somebody else's test: TestLocalDeclarationIsNotAnchored asserts
// that a module declaring its own `Fragment` gets NO entry in preludeByName,
// because preludeAnchorsOf takes the declaration ModuleScope resolves the name
// to and compares it against the spec. A module that declares its own `Result`
// therefore yields no anchor here, this function declines, and the synthesized
// signature keeps refusing. The error direction is over-refusal and never
// mis-resolution, which is the direction that cannot produce a wrong answer.
//
// One further case is worth naming because it looks like a hole and is not: the
// name in a synthesized signature came from the INTERFACE's declaring file
// (std/json's `Result<self, Json.ShapeError>`), not from the user's, so in a
// module that shadows `Result` the two scopes genuinely disagree about the name
// and this function's decline is the only correct answer available without
// carrying the declaring file's scope through synthesis.
//
// # What it does NOT do
//
// It resolves a TYPE name, and nothing else. A synthesized body may also
// CONSTRUCT a prelude variant (`Ok(...)`, `Err(...)`), and those reach
// preludeCall / preludeBare, which resolve through `fa.References` too and
// therefore still miss for a synth-band position. That is deliberately left
// alone: nothing in the corpus needs it, the derived bodies that matter reach
// their constructors through positions the front end DOES record, and inventing
// a second "what variant is this" answer beside variantalias.go's would be the
// divergence this package's ledger rows are about. If a program ever needs it,
// the route is the anchor's own `variant(name)` with the canonical name taken
// from the DECLARATION rather than from a reference.
func (g *gen) synthPreludeAnchor(t *ast.GenericType) (*preludeAnchor, bool) {
	// The front end's own published marker for the band, rather than a local
	// copy of the constant: derive_lowering.go and builder.go both read it, and
	// a second spelling of one boundary is how the two drift.
	if !analysis.IsSynthesizedLine(t.Line) {
		return nil, false
	}
	return g.preludeNamed(t.Name)
}
