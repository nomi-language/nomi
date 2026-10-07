package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A typed literal `<Type>"..."`.
//
// # What the construct is
//
// `<Type>"..."` is sugar for one call. The spec (§16) and std/literals say the
// same thing: the site desugars to
//
//	<Type>.from_fragments([Fragment.Static(…), Fragment.Dynamic(…), …])
//
// backed by `impl Literal for <Type> { fn from_fragments(fragments:
// List<Fragment<I>>): R }`. So there is no typed-literal MACHINERY to lower —
// there is a list to build and a type-qualified call to emit, both of which
// this builder already does for every other spelling.
//
// Every `from_fragments` takes `List<Fragment<I>>`: the checker enforces that
// shape (analysis/checker.go's checkTaggedString rejects any other parameter).
//
// # Why the ARGUMENT is what gets named, and not the handler
//
// stdlibCall's order, unchanged: it builds the arguments first and returns
// bad() when they refuse, WITHOUT reporting the callee's own reason. A typed
// literal is a call site, so it follows that order. This is not a preference —
// reporting the callee instead would give one key to `Toml"…"` (whose handler is
// stdlib: `stdlib function outside the scalar subset`) and another to `Sql"…"`
// (whose handler is the user's own), splitting one gap by where the handler
// happens to live.
//
// # The reason is READ, not derived
//
// The refusal name comes from the analyzer's own answer, through
// sigreason.go's projectRefusal — the channel inferred.go exists to be. The tag
// resolution is checker.go's: `resolveTaggedStringTag` registers a Reference at
// the tag identifier, and `checkTaggedString` replaces it with a proxy carrying
// `DispatchImpl` — the `(Literal, Tag)` impl method it resolved. Reading that
// back is strictly better than re-deriving it here: a second resolution could
// disagree with the front end about which handler a tag names, and a backend
// that disagrees with the front end is the failure this whole refusal machinery
// exists to prevent.
//
// Nothing here spells `Fragment` as a string, because a type identity matched
// by name is silently wrong once two declarations share it; the detail text
// (`std/literals.Fragment`) is rendered by qualifyOrigin off the analyzer's own
// Origin, exactly as every other type-shaped refusal's is.

// literalHandlerDecl is the `from_fragments` DECLARATION the checker dispatched
// this tag to. Split from literalHandler because the lowering wants the node
// and the refusal wants its declared parameter type, and re-deriving the
// position math twice is how the two would drift apart.
func (g *gen) literalHandlerDecl(t *ast.TaggedString) (*ast.FuncDef, bool) {
	if g.fa == nil || g.fa.ProjectImpls == nil {
		return nil, false
	}
	pos := analysis.Pos{Line: t.Line, Col: max(t.Col-len(t.Tag), 1)}
	sym := g.fa.References[pos]
	if sym == nil || sym.DispatchImpl == nil {
		return nil, false
	}
	// A `host fn` handler is an *ast.ExternFunc and carries no resolved
	// FuncType in ImplFuncTypes, so it declines here rather than being read
	// through a nil. Nothing in the repo declares one; the type switch is the
	// cost of not assuming that stays true.
	fd, isFn := sym.DispatchImpl.(*ast.FuncDef)
	if !isFn {
		return nil, false
	}
	return fd, true
}

// --- lowering ---------------------------------------------------------------

// fragmentSpec is `std/literals.Fragment`, the enum a typed literal's
// desugaring builds.
//
// Resolved through the (Origin, Name) rule against the spec table once, rather
// than spelled as a string at each use, since a type identity written as a
// name is silently wrong. TestLiteral_FragmentSpecIsResolved
// pins that it resolves, so a spec-table edit fails a test instead of turning
// every typed literal silently back into a refusal.
var fragmentSpec = preludeSpecFor("std/literals", "Fragment")

// fragmentVariants splits a Fragment instance's two variants by which one
// carries the CONCRETE payload and which the parametric one.
//
// By payload shape rather than by the names `Static` and `Dynamic`, because
// that is the distinction the desugaring actually makes: source text goes in
// the concrete slot and a `${…}` value in the parametric one. At `T = String`
// the two are the same Go type and a positional or name-only split would be
// invisible either way; here a spec whose payloads were swapped puts the text
// where the value belongs and `Fragment<Int>` stops compiling.
func fragmentVariants(d *typeDef) (static, dynamic *variantDef, ok bool) {
	for i := range d.preludeOf.spec.variants {
		vs := &d.preludeOf.spec.variants[i]
		v := d.variant(vs.nomi)
		if v == nil || len(v.payloads) != 1 {
			return nil, nil, false
		}
		if vs.param < 0 {
			static = v
		} else {
			dynamic = v
		}
	}
	if static == nil || dynamic == nil || static == dynamic {
		return nil, nil, false
	}
	if static.payloads[0].k != kindString {
		return nil, nil, false
	}
	return static, dynamic, true
}

// lastDynamicPart is the index of the final `${…}` slot, or -1.
func lastDynamicPart(parts []ast.StringPart) int {
	last := -1
	for i, p := range parts {
		if _, isExpr := p.(ast.StringExpr); isExpr {
			last = i
		}
	}
	return last
}

// literalTarget is the `from_fragments` a tagged literal calls: this module's
// own impl item, a SIBLING FILE's, or a stdlib declaration.
//
// One shape for all three so taggedLiteral (irliteral.go) has a single path.
// They differ only in how the call is written, and the std one goes through
// stdlibInvoke rather than formatting a call here, because the arity check, the
// argument coercion and the generated package's import accounting have exactly
// one implementation there.
type literalTarget struct {
	params []kind
	result kind
	// label is `<Tag>.from_fragments`, what a refusal names it by.
	label string
	// std is the stdlib declaration, nil for a handler a user file declares.
	std *stdFunc
	// unit is the sibling file's index when the handler is another generated
	// package's, and -1 for this module's own block and for std.
	// taggedLiteral declines a handler whose unit is not -1.
	unit int
	// pkg is that sibling's Go-spelled package, empty when it is this one.
	pkg string
	// sym is a sibling handler's retained body, interned in the declaring
	// unit's table as qualSiblingImplPlan interns it; nil when unknown.
	sym *ir.Symbol
	// rivalled marks a receiver with MORE THAN ONE written provider of this
	// method, program-wide. Carried on the target rather than asked at the
	// refusal site because the count needs the method NAME, which only the
	// resolution has. Never set for a std handler; see literalHandlerRivals.
	rivalled bool
}

// literalImpl is the impl function `<Tag>.from_fragments` resolves to, plus the
// label a refusal names it by.
//
// Three routes in one order — this file's own block, a SIBLING file's, then
// std — and each declines rather than refusing, so the caller's one refusal
// keeps naming the handler however far down the chain the miss happened.
//
// # This module's own block: DECLARATION-NODE identity
//
// The checker recorded the `*ast.FuncDef` it dispatched to on the tag's
// reference, and this looks for the impl block that literally contains that
// pointer. No name is compared — two `impl Literal for X` blocks in a program
// could otherwise both answer to `from_fragments`, and the one the front end
// chose is the only correct answer.
//
// # A SIBLING file's block: the same identity, one boundary out
//
// `g.implOrder` is the blocks of the FILE BEING LOWERED, and a user's tag type
// is not rtDeclared, so a handler declared in a sibling file (as in
// `17-typed-literals/typed_literals/typed_literals_test.nomi`, whose `Joiner`,
// `Safe`, `Raw` and `SQL` handlers live beside it) is found only through the
// sibling index.
//
// It is the route siblingimpl.go provides for `Owner.method(args)`, applied to
// this spelling. What differs is the resolution rule, and the
// difference makes this route the stronger of the two: a type-qualified call
// has only a name pair, so it needs writtenImplMembers and a rival refusal; a typed literal's tag reference carries the `*ast.FuncDef`
// the front end chose, so the site is matched by NODE and at most one site can
// answer. No rival rule, no ordering, nothing for two blocks to be ambiguous
// about.
//
// # A STDLIB handler: the anchor, because node identity is unavailable
//
// A std handler lowers in the stdlib index: `calendar.DateTime.from_fragments`
// is there, and a hand-written `DateTime.from_fragments([…])` routes through
// stdlibCall and lowers. So the std route is asked HERE, and the local route's
// failure is a decline rather than a refusal.
//
// Node identity is not available across this boundary: `std.Load()` re-parses
// and hands out FRESH pointers per call, so the stdlib index's `*ast.FuncDef`
// is never the one the user module's analysis resolved. The identity used
// instead is the one stdlibCall already relies on, and it is grounded in the
// analyzer rather than in a name: the TAG must resolve to a type whose Go type
// rt declares (an anchored std type, established through the analyzer's own
// (Origin, Name) rule — see stdstruct.go, opaque.go, stdenum.go), and the
// handler is then that type's `from_fragments` in the index.
//
// That pairing is sound because a type implements an interface at most once
// PROGRAM-WIDE: `detectImplCollisions` rejects a user `impl Literal for
// DateTime` beside std's, so for an anchored std type there is exactly one
// handler and it is std's. The check is enforced by the front end, so no test
// here needs to repeat it.
//
// The declared name is still read off the resolved handler, so a tag whose
// checker-chosen dispatch is not a `from_fragments` cannot reach the index
// entry by type alone.
func (g *gen) literalImpl(t *ast.TaggedString) (literalTarget, bool) {
	fd, found := g.literalHandlerDecl(t)
	if !found {
		return literalTarget{}, false
	}
	label := t.Tag + "." + fd.Name
	rivalled := g.literalHandlerRivals(t.Tag, fd.Name)
	for _, d := range g.implOrder {
		if !d.lowerable || d.decl == nil || !implBlockDeclares(d.decl, fd) {
			continue
		}
		it := d.items[fd.Name]
		if it == nil || !it.lowerable {
			return literalTarget{}, false
		}
		return literalTarget{params: it.params, result: it.result, label: label, unit: -1, rivalled: rivalled}, true
	}
	if h, found := g.siblingLiteralImpl(t, fd, label); found {
		h.rivalled = rivalled
		return h, true
	}
	return g.stdLiteralImpl(t.Tag, fd, label)
}

// literalHandlerRivals reports whether MORE THAN ONE written impl block
// provides `method` for the tag's type, anywhere in the program.
//
// PROGRAM-wide, through the sibling impl index (siblingimpl.go), for
// foreignImplRivals' reason one level in: the orphan rule admits `impl I for
// T` in T's module, a Nomi module is several files, so "how many blocks
// provide this" is not a question one file can answer. It is asked of the
// index rather than of `foreignImplRivals` itself because that predicate is
// built for a site COMMITTING to a local candidate — it answers true for every
// sibling handler by construction, which is the case this route exists to
// serve.
//
// Non-lowerable blocks COUNT. resolveImplMembers indexes them deliberately,
// and a rival this builder cannot call is still a rival the front end can:
// what makes the site unresolvable is that two declarations answer to one
// spelling, not that both are emittable.
//
// The local scan is the fallback for a gen with no file index, and it is the
// same shape typeQualifiedCall's is. Fail-SAFE either way: a route that could
// not count would have to answer "no rivals", and that is the answer that
// emits.
func (g *gen) literalHandlerRivals(tag, method string) bool {
	d, isNamed := g.namedType(tag)
	if !isNamed || d == nil {
		return false
	}
	if g.files != nil && d.decl != nil {
		sites := g.files.implMembers[implMemberKey{recv: d.decl, method: method}]
		return len(writtenImplMembers(sites)) > 1
	}
	k, n := named(d), 0
	for _, b := range g.implOrder {
		if b.recv == k && !b.synth && b.items[method] != nil {
			n++
		}
	}
	return n > 1
}

// siblingLiteralImpl is the sibling half of literalImpl: the handler the
// checker chose, declared in ANOTHER FILE of this program.
//
// # Every miss declines
//
// Four things can go wrong past resolution: the block is one the declaring
// file refuses, the reference closes a Go import cycle, a parameter kind does
// not translate into this package, or the result does not. Here they all
// decline rather than report, because each is a property of the callee, not
// of the spelling that reached it, and a second report would size one gap as
// two. taggedLiteral declines every sibling handler in any case.
func (g *gen) siblingLiteralImpl(t *ast.TaggedString, fd *ast.FuncDef, label string) (literalTarget, bool) {
	if g.files == nil || g.fileUnit < 0 {
		return literalTarget{}, false
	}
	// namedType and not the analyzer alone: a mirror this builder REFUSES — a
	// type closing a type cycle — is already reported at its own position.
	d, isNamed := g.namedType(t.Tag)
	if !isNamed || d == nil || !d.lowerable || d.decl == nil {
		return literalTarget{}, false
	}
	for _, s := range g.files.implMembers[implMemberKey{recv: d.decl, method: fd.Name}] {
		if s.unit == g.fileUnit || s.block == nil || s.fn == nil {
			// This file's own impl. The local walk above owns it and has
			// already had its chance, so it is not a candidate here.
			//
			// A SURVIVING MUTANT, and the SIXTH reading of this exact shape —
			// siblingimpl.go's own same-unit `continue` records the previous
			// five. Deleting the `s.unit == g.fileUnit` test changes nothing
			// measurable, and the reason is worth stating rather than the
			// stronger claim: the only way to arrive here with a same-unit
			// site is a LOCAL block the walk above skipped, which it skips
			// only when `!d.lowerable` or `d.decl == nil` — and those are
			// exactly the two the `s.fn.lowerable()` and `s.block == nil`
			// tests below decline for. So the two routes reach one answer by
			// different roads and the exclusion asserts something stronger
			// than true. Kept as a `continue` rather than a `return` for the
			// same reason siblingimpl.go keeps its own.
			continue
		}
		if !implBlockDeclares(s.block, fd) {
			continue
		}
		if !s.fn.lowerable() {
			return literalTarget{}, false
		}
		params, result, ok := g.importLiteralSig(s.fn)
		if !ok {
			return literalTarget{}, false
		}
		unit := g.files.units[s.unit]
		pkg := ""
		if unit.pkg != g.pkg {
			pkg = unit.pkg
		}
		var sym *ir.Symbol
		if g.reg != nil && s.item != nil && s.unit < len(g.reg.gens) && g.reg.gens[s.unit] != nil {
			sym = g.reg.gens[s.unit].irCalleeSym(s.item, s.symName)
		}
		return literalTarget{params: params, result: result, label: label, unit: s.unit, pkg: pkg, sym: sym}, true
	}
	return literalTarget{}, false
}

// implBlockDeclares reports whether `fd` is one of the block's own items.
//
// Pointer identity against the block's `Items`, the same test the local walk
// makes. It is what stops the (receiver declaration, method name) index key
// from being the resolution: the key can collide — an inherent `impl Joiner {
// pub fn from_fragments(…) }` beside `impl Literal for Joiner` is two entries
// under one key — and only the node says which one the checker dispatched to.
func implBlockDeclares(b *ast.ImplBlock, fd *ast.FuncDef) bool {
	for _, item := range b.Items {
		if item == ast.Node(fd) {
			return true
		}
	}
	return false
}

// importLiteralSig translates a sibling handler's one parameter and its result
// into THIS package's kinds.
//
// The declaring package's kinds are a different pointer for the same type
// (typeFileFunc), so `arg.k != h.params[0]` would be true for two spellings of
// one type and the site would refuse an argument it had just built correctly.
// importKind does the translation.
//
// The arity is asserted rather than assumed: the desugaring passes exactly one
// fragment list, `checkTaggedString` admits exactly that shape, and a handler
// this builder resolved to some other arity is not the callee — so it declines
// instead of lowering against a signature it guessed. taggedLiteral makes the
// same check on the kinds and for the same reason.
func (g *gen) importLiteralSig(f *fileFunc) ([]kind, kind, bool) {
	if len(f.params) != 1 {
		return nil, kindInvalid, false
	}
	p, ok := g.importKind(f.params[0])
	if !ok {
		return nil, kindInvalid, false
	}
	r, ok := g.importKind(f.result)
	if !ok {
		return nil, kindInvalid, false
	}
	return []kind{p}, r, true
}

// stdLiteralImpl is the stdlib half of literalImpl: the anchored type's own
// `from_fragments`, fd being the handler the checker resolved.
//
// Declines rather than refusing for every miss, so the caller's refusal keeps
// naming the handler. A tag that is not an anchored std type, a std module that
// declares no such handler, and a handler the stdlib index itself refused are
// three different misses and none of them is this function's to report.
//
// A tag whose name this unit's scope does not anchor (a std attached test that
// imports `std/regex.Regex` inside its own block) is still std's when the
// handler the checker resolved is the index's declaration itself.
func (g *gen) stdLiteralImpl(tag string, fd *ast.FuncDef, label string) (literalTarget, bool) {
	if g.std == nil {
		return literalTarget{}, false
	}
	method := fd.Name
	d, isNamed := g.namedType(tag)
	anchored := isNamed && d.rtDeclared
	// stdSole, not a pick: the desugaring has no argument kinds yet — it is
	// deciding what the handler's parameter type IS — so a spelling several
	// declarations answer to has nothing here to choose between them with, and
	// declining keeps the caller's refusal naming the handler.
	f := stdSole(g.std.byType[tag+"."+method])
	if f == nil || !f.lowerable() || (!anchored && f.decl != fd) {
		return literalTarget{}, false
	}
	if len(f.params) != 1 || f.result.def == nil {
		// A handler whose shape this builder did not resolve to one parameter
		// returning a named type is not the desugaring's callee. Declining
		// keeps the refusal rather than emitting against a guessed signature.
		return literalTarget{}, false
	}
	return literalTarget{params: f.params, result: f.result, label: label, std: f, unit: -1}, true
}
