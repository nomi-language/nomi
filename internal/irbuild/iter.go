package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// The `Iter` pipeline: `xs |> Iter.map(f) |> Iter.to_list()`, lowered.
//
// # Why the pipeline has its own arm
//
// `Iter` is the dominant call cluster in the corpus (`Iter.to_list` 85 sites,
// `Iter.count` 68, `Iter.any_match` 42, `Iter.map` 36, `Iter.reduce` 26,
// `Iter.filter` 17). Three things stand between those calls and the stdlib
// index, and only one of them is a representation:
//
//  1. The adapters are GENERIC Nomi bodies whose signatures mention `Iter<T>`
//     and `(T) -> U`, so stdlib.go's scalar boundary refuses every one of them
//     speculatively and files them under one shared reason. That boundary is
//     correct and is not relaxed here (see stdlib.go).
//  2. The stdlib index holds every `Iter.` key
//     (`TestIterRoute_TheStdlibIndexIsASecondRoute`), so there IS a second
//     route from an `Iter.`-qualified call to a lowering, and the only thing
//     keeping this file's anchor authoritative is ORDER: `iterCall` is asked
//     before the stdlib index and CLAIMS the call either way, lowering it or
//     refusing it by name, so the index never sees one.
//
//     A rule that says "X is the one place that decides Y" is not checked by
//     auditing X's callers; it is checked by enumerating the other routes to Y.
//     Two entry points produce a `rt.Seq` lowering (`iterCall` and
//     irloop.go's `iterLoop`), both gate on `g.iter`, and the stdlib
//     index is a third that ordering closes. If that order changes, `Iter.map`
//     in a USER file resolves through `byType["Iter.map"]` and the identity
//     check below no longer applies.
//  3. The `host fn`s the adapter bodies are written over (`map_each`,
//     `filter_each`, `take_each`, `from_each`, `to_list`) need Go
//     implementations the VM can reach, which means rt.
//
// So this file is maps.go's shape one file over: the extern half lives in rt
// (rt/seq.go), and a call site resolves to it directly.
//
// # The representation is `rt.Seq`
//
// rt/seq.go carries that argument in full. In one line: std/iter declares
// exactly one `Iter` implementor, `opaque struct Seq<T> { run: ((T) -> Bool) ->
// Bool }`, and `rt.Seq` is that declaration field for field. A pipeline is
// built as `ir.Iter` nodes (iriterbody.go), which the VM runs over it. Under
// the PUSH protocol `each_while` is the whole interface, so a closure is a
// total representation of an `Iter<T>` and nothing is erased, which is why
// tagSeq is not tagIface and no box is recorded for a pipeline.
//
// # What is refused BY NAME
//
// An `Iter.` function this arm does not lower refuses under `unlowered Iter
// function` with its own name in the detail, so each one is counted
// separately.
//
// # The short-circuiting terminals need no seed
//
// `any_match`, `all_match`, `find`, `count`-over-a-lazy-source and the other terminals std
// writes over `reduce` do not need the lambda-parameter DEFAULT that is
// `reduce`'s seed. In every one of them the seed is exactly the answer for a
// source that ran to EXHAUSTION, and `break v` is exactly "stop, with this
// answer"; `each_while` already returns the first, and `false` from a yield
// already means the second. `any_match` is `!each_while(src, fn x -> !f(x))`: a source
// that completed is a source in which nothing matched. rt/seqterm.go carries
// the whole derivation, one line per function.
//
// # Control flow in a callback
//
// A Nomi iteration callback may answer with five different control signals,
// and `break` is not a value a Nomi function can observe. The lambda body is
// lowered like any other body, so `break` and `continue` in a callback go
// through the ordinary path for them (see ctrlflow.go).
//
// `return` needs nothing: a Nomi lambda's `return` is lambda-scoped, so a
// `return` in a callback produces the same value a tail expression does.

// --- the anchor --------------------------------------------------------------

// iterAnchor is std/iter's `Iter` interface as THIS compilation's analysis sees
// it.
type iterAnchor struct {
	// ty is the checker's type for that declaration, whose `Origin` and
	// `Name` identify a use-site `Iter<T>` the checker solved.
	ty *analysis.InterfaceType
}

// iterElemParam is the type parameter name `Iter<T>` declares, which the
// protocol shape check reads `yield: (T) -> Bool` against.
const iterElemParam = "T"

// loadIter resolves std/iter's `Iter` against this module's analysis, once.
//
// Identity, on exactly the footing prelude.go establishes for `Maybe` and
// `Result`: a table keyed on the bare name `Iter` would be a type identity
// spelled as a string. A user file may declare its own, and lowering that to
// `rt.Seq` would mix two Nomi types the checker keeps apart.
//
// Three things have to hold, and each one closes a different door. The first two
// are PROVENANCE and the third is PROTOCOL, and that split is what decides how
// std/iter.nomi itself is treated — see THE DECLARING MODULE below.
//
//  1. The name is reached through an IMPORT (`sym.Resolved != nil`), or this IS
//     the declaring module. A locally declared `interface Iter<T>` in any other
//     file produces no anchor here. The check is a claim about the
//     declaration, and holds whatever order the call's resolvers run in.
//  2. This module declares no `impl` block headed `Iter`, unless this IS the
//     declaring module. A program that adds its own owner function to std's
//     interface (`impl Iter<T> { fn map(...) }`) would have `Iter.map` resolve
//     to ITS declaration, and lowering that to rt.SeqMap would run a different
//     function than the one the programmer wrote. Declining the whole arm is the
//     only safe answer, because the shadowing is per-NAME and the builder would
//     otherwise have to decide which names the user block claimed.
//  3. The declaration's SHAPE is the push protocol rt implements. A std edit
//     that changed `each_while`'s signature must produce NO anchor and refuse
//     every mention loudly, rather than lower against a protocol nobody wrote.
//     Same two steps prelude.go and opaque.go take.
//
// # THE DECLARING MODULE IS ITS OWN PROTOCOL
//
// Both provenance rules would refuse std/iter.nomi, which is the one file
// whose `Iter` is std's BY CONSTRUCTION: it declares `Iter` locally, so rule 1's
// `sym.Resolved` is nil, and its whole API is one `impl Iter<T>` block, so rule
// 2 sees it. The SHAPE, rule 3, passes there.
//
// Relaxing both rules for that one module cannot reach a user program: a user
// file declaring its own `interface Iter<T>` is a FRONT-END error ("type name
// 'Iter' is reserved"), and `impl Iter<T>` over std's imported `Iter` is
// another ("orphan impl"). Both are pinned with the front end's own message in
// iterselfanchor_test.go.
//
// What replaces the provenance proxy for that module is not weaker. `declaresStdIter`
// is the CALLER's statement of which embedded stdlib module is being lowered,
// and rule 3 then runs against that module's own declaration — so the anchor is
// admitted only when std/iter's `Iter` still IS the push protocol rt implements.
// Following an import trusts the analyzer's resolution; this is std's
// declaration by construction, which is the same argument the `default:` arm
// below already makes for reading the anchor out of `StdlibModuleScopes`.
func (g *gen) loadIter() {
	if g.iterLoaded {
		return
	}
	g.iterLoaded = true
	if g.fa == nil || g.fa.ModuleScope == nil {
		return
	}
	self := g.declaresStdIter()
	if !self && g.declaresIterImpl() {
		return
	}
	sym := g.fa.ModuleScope.Lookup("Iter")
	switch {
	case sym != nil && sym.Resolved != nil:
		sym = sym.Resolved
	case sym != nil && self:
		// std/iter.nomi's own declaration. Rule 1 is satisfied by BEING std
		// rather than by reaching it, and rule 3 below still reads the shape off
		// this very declaration. See THE DECLARING MODULE above.
	case sym != nil:
		// Declared locally rather than imported. Rule 1 above: produce no
		// anchor.
		return
	default:
		// `Iter` is not in this file's scope at all, which happens for exactly
		// one legal spelling: `import std::iter.Iter.loop` imports the FUNCTION
		// and not its owner. The anchor is then read out of std/iter's own
		// scope, which the analyzer hands every file that can reach the module.
		// At least as strong as rule 1 — it is std's declaration by
		// construction rather than by a resolution this builder followed — and
		// rule 2 still gates it, so a file with its own `impl Iter` block
		// declines here regardless.
		scope := g.fa.Program().StdlibModuleScopes["iter"]
		if scope == nil {
			return
		}
		if sym = scope.Lookup("Iter"); sym == nil {
			return
		}
	}
	ty, isIface := sym.Type.(*analysis.InterfaceType)
	if !isIface {
		return
	}
	decl, isDecl := sym.Node.(*ast.InterfaceDef)
	if !isDecl || !iterProtocolMatches(decl) {
		return
	}
	g.iter = &iterAnchor{ty: ty}
}

// declaresStdIter reports whether this gen is lowering std/iter itself — the
// module that DECLARES the `Iter` protocol.
//
// `stdModule` is set only by newStdGen, from the module name buildStdlibIndex
// reads off `std.Load()`'s keys, so a user gen answers false here without any
// path or name being compared. That is the whole predicate, and the reason it
// is enough is that it replaces only the provenance rules: the
// caller says which embedded module this is, and loadIter's rule 3 then reads
// the SHAPE off that module's own declaration. A std edit that changed
// `each_while` still produces no anchor.
//
// Deliberately NOT a path check. `isStdTasksFile` (analysis/concurrent_scope.go)
// is the closest precedent for the question "is the current file the std module
// that declares this", and it has to compare `filepath.Base` twice AND check a
// six-name fingerprint of the module scope, because the only thing it is handed
// is a path. Here the module name is already in hand from the one constructor
// that can produce a stdlib gen, and re-deriving it from `nomiPath` would be a
// weaker fact wearing more code.
//
// One name and not a table: `Iter` is the only interface whose anchor this
// builder resolves by provenance. If a second one arrives, the shape to reach
// for is one predicate that takes the module name, and not a copy of this.
func (g *gen) declaresStdIter() bool {
	return g.stdModule == "iter"
}

// declaresIterImpl reports whether this module writes an `impl` block headed
// `Iter` — either `impl Iter<T> { … }` (adding owner functions to std's
// interface) or `impl Iter for MyType { … }`.
//
// The second is not a hazard: implementing the protocol for a user
// type is the documented extension point, and it does not change what
// `Iter.map` means. iterCall drives a user source through its own impl
// (iteruser.go). So the narrow form is used: only an `Iter`-HEADED owner
// block answers true.
func (g *gen) declaresIterImpl() bool {
	for _, n := range g.nodes {
		ib, isImpl := n.(*ast.ImplBlock)
		if !isImpl {
			continue
		}
		if ib.Interface == nil && analysis.TypeExprBaseName(ib.Receiver) == "Iter" {
			return true
		}
	}
	return false
}

// iterProtocolMatches reports whether decl is the push-protocol `Iter` rt/seq.go
// is written against.
//
// Everything checked here decides what `rt.Seq[T]` MEANS. One type parameter
// fixes the element type; `each_while(collection: self, yield: (T) -> Bool):
// Bool` fixes the closure field's Go signature; and `known_count` has to be
// present because `Iter.count` over a List is lowered to the List's cached
// length, which is only the right answer while std keeps declaring an O(1)
// override for it.
func iterProtocolMatches(decl *ast.InterfaceDef) bool {
	if decl.Name != "Iter" || len(decl.TypeParams) != 1 || decl.TypeParams[0].Name != iterElemParam {
		return false
	}
	var each, known *ast.InterfaceMethod
	for i := range decl.Methods {
		switch decl.Methods[i].Name {
		case "each_while":
			each = &decl.Methods[i]
		case "known_count":
			known = &decl.Methods[i]
		}
	}
	if each == nil || known == nil {
		return false
	}
	// `each_while(collection: self, yield: (T) -> Bool) -> Bool`, and nothing
	// else: a third parameter or a different result is a different protocol.
	if len(each.Params) != 2 || !isSelfType(each.Params[0].TypeAnnotation) ||
		simpleTypeName(each.ReturnTypeExpr) != "Bool" {
		return false
	}
	fn, isFn := each.Params[1].TypeAnnotation.(*ast.FuncType)
	if !isFn || len(fn.Params) != 1 ||
		simpleTypeName(fn.Params[0]) != iterElemParam || simpleTypeName(fn.Return) != "Bool" {
		return false
	}
	// `known_count(collection: self) -> Maybe<Int>`, still `open` — a source that
	// stores its own count overrides it, which is the whole basis of the O(1)
	// answer `Iter.count` gives.
	if !known.Open || len(known.Params) != 1 || !isSelfType(known.Params[0].TypeAnnotation) {
		return false
	}
	gt, isGeneric := known.ReturnTypeExpr.(*ast.GenericType)
	return isGeneric && gt.Name == "Maybe" && len(gt.Params) == 1 && simpleTypeName(gt.Params[0]) == "Int"
}

// isSelfType reports whether a type annotation is the interface's own `self`.
// `self` is its own AST node rather than a SimpleType named "self", which is
// what a name-based check would have got wrong.
func isSelfType(te ast.TypeExpr) bool {
	_, isSelf := te.(*ast.SelfType)
	return isSelf
}

// --- the kind ----------------------------------------------------------------

func seqKindIn(g *gen, elem kind) kind {
	return kind{tag: tagSeq, comp: g.intern("Iter<"+elem.nomi()+">", elem)}
}

// seqElem is the element kind of a lowered `Iter<T>`.
func seqElem(k kind) kind { return k.comp.parts[0] }

// --- call sites --------------------------------------------------------------

// iterOwns reports whether `owner.method(...)` is a call on STD's `Iter`, so
// iterCall and iterLoop claim it.
//
// # Why std's `Iter` is claimed whole
//
// A module that declares `impl Iter for SomeType` has a non-empty
// `g.implsByIface["Iter"]`, and an `Iter.<method>` call in that file is still
// std's owner function:
//
//	struct Box { v: Int }
//	impl Iter for Box { fn each_while(b: Box, yield: (Int) -> Bool) -> Bool { yield(b.v) } }
//	countdown = Iter.loop(5, fn n -> if n == 0 { break n } else { n - 1 })
//
// `Iter`'s OWNER FUNCTIONS are std's, not a dispatch on a user
// implementor: `loop`, `map`, `to_list` are declared in `impl Iter<T>` and
// belong to iterCall and iterLoop. `each_while` is the interface's one genuine
// requirement, and iterCall drives a user implementor itself (iteruser.go), so
// claiming the call gives nothing up.
//
// # What the gate excludes, and why the anchor is the right question
//
// `g.iter != nil` is loadIter's three-part identity check, so this claims a
// call ONLY when the qualifier resolves to std's push-protocol `Iter`. A module
// declaring its own `interface Iter<T>`, or reaching a sibling file's, produces
// no anchor and is left to the resolver that dispatches a user interface. The
// anchor is asked rather than the spelling because a type identity matched by
// name is silently wrong.
func (g *gen) iterOwns(owner string) bool {
	if owner != "Iter" {
		return false
	}
	g.loadIter()
	return g.iter != nil
}

// --- the seed ----------------------------------------------------------------
//
// `Iter.reduce(xs, 100, fn acc, x -> acc + x)` folds from 100. The 100 is written
// as the callback's first-parameter DEFAULT and it is not an ordinary default:
// nothing is applied on an arity mismatch. The CALLEE reads it, evaluated off
// the lambda's own closure, so it is a value flowing from the caller's syntax
// into the terminal, and a function value has nowhere to carry one.
//
// The seed is lifted out at the call site and is an operand of the
// terminal's `ir.Iter` node, beside the source and the callback
// (iterReduceCall, iriterbody.go). It is not carried on the lowered `Seq`,
// for a correctness reason: the seed belongs to the callback, so the same
// source may be reduced twice with two different seeds.
//
// Three properties of the seed, each pinned by a fixture:
//
//  1. The seed is evaluated in the lambda's closure, not in its parameter
//     scope. `x = 5; Iter.reduce([1, 2, 3], x, fn acc, x -> acc + x)` is 11, so the
//     `x` in the seed is the outer one. The seed is lowered by the calling
//     body's builder, where the outer `x` is the one in scope; lowering it
//     inside the callback's body would answer 6.
//  2. The seed is evaluated once per reduce call, after the source expression
//     and before the first element. Its instructions are appended to the
//     calling block after the source's, so
//     `Iter.reduce(source(), fn acc = if flag { note(…) } else { 0 }, x -> …)`
//     evaluates the source ahead of the seed.
//  3. A DESTRUCTURING first parameter carries a seed too:
//     `fn (a, b) = (0, 0), x -> …` runs, so the claim is on the parameter's
//     default and not on it having a plain name.

// --- the std-source guard ----------------------------------------------------
