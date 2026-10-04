package irbuild

// The match class. irdestructure.go sits beside it.
//
// A pattern is not a node. `internal/ir/match.go`'s header carries the
// argument; the short form is that this IR is linear and a pattern nests, so
// a pattern lowers to a sequence: `ir.Match` for each refutable question,
// `ir.Proj` for each step of navigation, `ir.Bind` for each name.
//
// A map pattern is not a match kind. Its key is probed with a `Map.get` call
// and the answering Maybe is tested like any variant (irmappattern.go).
//
// A match on an enum reads which variant off the node; the tag number is the
// consumer's business, as it is for `make`.
//
// Two choices are made from an operand's type rather than from a flag:
//
//   - which equality a literal test uses, chosen from the literal operand's
//     kind. A literal pattern never dispatches to an `impl Equatable`: a
//     literal pattern is an Int, Float, Decimal or String.
//   - whether a variant test reads a tag. `Bool` has no `*typeDef` to read a
//     tag through, so the test is the value itself, decided from the
//     subject's kind.
//
// No match needs a predicate: what can fail or have an effect at a test is a
// literal operand's own evaluation (`internal/ir/match.go`).
//
// The string prefix pattern, `"prefix" + rest`, is real Nomi (spec §10,
// parsed and checked), and this builder refuses it as `string prefix
// pattern` (case.go). No `MatchKind` exists for it, because a kind with no producer
// would be a rule with no population.

// irBoolEnumToken is the interning token for the prelude's `Bool`.
//
// `Bool` is the one Nomi enum with no `*typeDef` in this builder: it is the
// scalar kind `kindBool`, so a `case b { True -> … }` arrives with
// `subj.k == kindBool` and `subj.k.def == nil`. Every other
// `ir.MatchVariant` interns its enum on the `*typeDef` the producer already
// resolved.
//
// It is a package-level sentinel rather than `ir.NewSymbol("Bool")` at the
// site, because minting per site would give two tests of one enum two
// identities. The token is process-wide and the table is per-lowering, so
// this shares nothing across lowerings: it is a map key, not a map.
var irBoolEnumToken = new(int)
