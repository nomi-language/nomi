package irbuild

import (
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// Lambdas and closures.
//
// A Nomi lambda lowers to an `ir.FuncValue`, which is the whole of the
// representation decision — and it is only defensible because of three
// properties of Nomi's closures that have to be checked one at a time rather
// than assumed as a family.
//
// # 1. Capture
//
// A closure captures the VALUE each free name holds when the closure is made
// (spec §23). A later rebinding of the name, in the same scope or a nested
// one, is a new binding (spec §2) that the closure never sees. In the IR a
// capture is an operand of the `ir.FuncValue` that builds the closure, read
// once when it runs, and a rebinding is a fresh `ir.Bind`; nothing more is
// needed. testdata/closures.nomi and testdata/nested_fn.nomi observe both
// kinds of rebinding.
//
// A nested `fn` that names itself cannot capture its own name, which is bound
// only once the closure exists. It refers to itself through its own closure:
// the closure's first capture is the closure itself (ir.NewRecursiveFuncValue,
// irnestedfn.go).
//
// # 2. `return` is lambda-scoped
//
// A `return` inside a Nomi lambda returns from the
// LAMBDA, not from the enclosing `fn` — the opposite of what the same word
// would do inside a Go func literal's enclosing function, and the same as what
// it does inside a Go func literal itself. That is the one place the target
// language hands us the Nomi rule for free: a Go `return` in a func literal
// returns from the func literal, at any nesting depth. So the encoding is
// literally `return`, and the work is entirely in the TYPE side — a lambda
// declares no return type, so the builder has to discover one (resultInference
// below) and make every `return` in the body check against the LAMBDA's answer
// instead of the enclosing function's declared one.
//
// Getting this wrong produces a plausible answer: hoisting the lambda body
// into the enclosing function would make `return "positive"` return from
// `scoped`, which is a well-typed program that prints the wrong thing.
//
// # 3. The frame
//
// A lambda takes `fr *rt.Frame` exactly as a module function does, so an
// activation allocates NOTHING. The Go func literal's own `fr` parameter
// shadows the enclosing one, which is correct: a frame belongs to an
// activation, and the caller supplies it. The closure allocation Go performs is
// per CLOSURE CREATION, not per call; TestLambdaCallAllocatesNothing measures
// the call.

// resultInference is a lambda's result type, discovered rather than declared.
//
// A `fn` states its result and every `return` is checked against it. A lambda
// states nothing, so the first thing that produces a value — a `return`, or the
// body's final expression — decides, and everything after it is checked against
// that. Which one comes first is not fixed: `|x| if p { return a } else { b }`
// settles on the `return`, and `|x| b` settles on the tail.
type resultInference struct {
	k   kind
	set bool
}

func (r *resultInference) settle(k kind) (kind, bool) {
	if !r.set {
		r.set, r.k = true, k
	}
	return r.k, k == r.k
}

// funcKind is the kind of a Nomi function type.
//
// Structural, so its identity is its interned *compKind and not its tag: two
// function types are the same type exactly when their Go types are identical.
// See composite.go.
func (g *gen) funcKind(params []kind, result kind) kind {
	return funcKindIn(g, params, result)
}

// funcKindIn is funcKind for a caller that may have NO gen — the stdlib
// signature boundary — following listKindIn's and mapKindIn's shape.
//
// tupleKind, listKindIn and mapKindIn all route their only gen access through
// internComp, which handles nil by interning process-wide whenever every
// component is package-neutral, and this does the same. A
// `stdGenStructSpecs` field whose kind is a FUNCTION (`std/random.Generator<T>`'s
// `run`) reaches it with no gen.
//
// A nil gen needs no `usesRT` mark and skipping it loses nothing: the mark says
// "this generated package must import rt", and there is no package being
// generated. Any REAL gen that later names this kind marks itself when it reads
// it, and the rendered text carries `*rt.Frame` regardless, which is what
// mentionsRT answers from.
func funcKindIn(g *gen, params []kind, result kind) kind {
	// kindInvalid: propagates — a kind CONSTRUCTOR; the lambda`s own decline is counted below.
	if result == kindInvalid {
		return kindInvalid
	}
	nomiParams := make([]string, 0, len(params))
	for _, p := range params {
		// kindInvalid: propagates — a kind CONSTRUCTOR; the parameter`s decline is reported in lambdaParams.
		if p == kindInvalid {
			return kindInvalid
		}
		nomiParams = append(nomiParams, p.nomi())
	}
	nomi := "(" + strings.Join(nomiParams, ", ") + ") -> " + result.nomi()
	parts := make([]kind, 0, len(params)+1)
	parts = append(parts, params...)
	parts = append(parts, result)
	return kind{tag: tagFunc, comp: internComp(g, nomi, parts...)}
}

// funcParams and funcResult split an interned function kind back apart. The
// result is the last part; see funcKind.
func funcParams(k kind) []kind { return k.comp.parts[:len(k.comp.parts)-1] }
func funcResult(k kind) kind   { return k.comp.parts[len(k.comp.parts)-1] }

// --- destructuring parameters -----------------------------------------------
//
// A destructuring parameter is one value and several names. Go has no
// destructuring, so the value arrives in one anonymous parameter and each name
// the pattern introduces becomes an ordinary local read out of it, with no
// runtime checks: the pattern is IRREFUTABLE (the checker rejects a refutable one with
// a "use a `case` in the body" diagnostic), so there is no mismatch to report
// and no branch to emit.
//
// Written against a `src expr` rather than a parameter so a nested pattern
// recurses through the same code, and so a `fn` parameter can be routed here
// unchanged.

// patternParamKind is the kind of the value a destructuring parameter receives.
//
// The annotation wins when there is one. When there is not, the self-typing
// rule applies: a pattern whose head NAMES a type is its own annotation
// (`Duration(x)`, `Point{x, y}`, `E.V(x)`). A head-less pattern — a tuple, an
// anonymous struct, a dotted variant, a bare identifier — has nothing to read a
// type off, and Nomi requires an annotation there, so its absence here means
// the annotation named a type outside the subset.
func (g *gen) patternParamKind(p ast.Param, at ast.Node) (kind, bool) {
	if p.TypeAnnotation != nil {
		k := g.typeOf(p.TypeAnnotation)
		// kindInvalid: reports — rejectTypeAnnotation names the type's own gap.
		if k == kindInvalid {
			g.rejectTypeAnnotation(p.TypeAnnotation, patternText(p.Destructure), at)
			g.probePattern(p.Destructure)
			return kindInvalid, false
		}
		return k, true
	}
	var head ast.TypeExpr
	switch pat := ast.WithoutAs(p.Destructure).(type) {
	case *ast.StructPattern:
		head = pat.TypeName
	case *ast.EnumPattern:
		head = pat.Variant
	}
	if head == nil {
		// No annotation and no head to self-type from — but the CHECKER may
		// still know, and for a lambda it usually does: the expected type comes
		// from the call site, exactly as it does for an unannotated plain
		// parameter. `inferredParamKind` cannot answer here because it matches
		// on the parameter's NAME and a destructuring parameter has none, so
		// the checker records the pattern's type separately. See
		// inferredPatternKind.
		if k, found := g.inferredPatternKind(p.Destructure); found {
			return k, true
		}
		g.reject("parameter without a declared type", patternText(p.Destructure), at)
		g.probePattern(p.Destructure)
		return kindInvalid, false
	}
	owner, member, ok := patternHead(head)
	if !ok {
		g.reject("destructuring parameter", "pattern head "+typeText(head), at)
		g.probePattern(p.Destructure)
		return kindInvalid, false
	}
	// `E.V(x)` names the ENUM through one of its variants; `Duration(x)` and
	// `Point{x, y}` name the type directly.
	name := owner
	if name == "" {
		name = member
	}
	if d, found := g.types[name]; found && d.lowerable {
		return named(d), true
	}
	g.reject("destructuring parameter", "self-typed by "+name+", which is not a lowered type", at)
	g.probePattern(p.Destructure)
	return kindInvalid, false
}

// destructure emits the bindings an irrefutable pattern introduces from src,
// and reports whether the whole pattern was lowered.
func (g *gen) destructure(pat ast.Node, src expr, at ast.Node) bool {
	switch p := pat.(type) {
	case *ast.WildcardPattern:
		return true

	case *ast.IdentPattern:
		g.bindDestructured(p.Name, src, p)
		return true

	case *ast.AsPattern:
		// The pattern's names, then the name for the whole value.
		if !g.destructure(p.Pattern, src, at) {
			return false
		}
		g.bindDestructured(p.Name, src, p)
		return true

	case *ast.StructPattern:
		return g.destructureStruct(p, src, at)

	case *ast.EnumPattern:
		return g.destructureEnum(p, src, at)

	case *ast.TuplePattern:
		return g.destructureTuple(p, src, at)

	default:
		// A LIST or MAP pattern. Both are REFUTABLE — a list pattern's arity is
		// not in its type and a map pattern's keys may be absent — so the
		// checker rejects both in a parameter with "refutable pattern in
		// parameter; bind the parameter and use a `case` in the body", and this
		// arm has no source that reaches it: `|[a, b]: List<Int>|` and
		// `|{"a" => v}: Map<String, Int>|` are both front-end errors.
		//
		// It refuses rather than being omitted, because the alternative for a switch
		// default is falling through to `return true` and binding nothing,
		// which is a wrong ANSWER where this is a refusal. No test row claims
		// it is reachable; see destructureshape_test.go.
		g.reject("destructuring parameter", constructName(pat), at)
		g.probePattern(pat)
		return false
	}
}

// bindDestructured introduces one name from a destructured value.
//
// It takes a node because `ir.Bind` needs a position and a position is never
// fabricated, so the three callers pass the node they already have: the ident
// pattern itself, the struct pattern field's own, or the enum pattern.
func (g *gen) bindDestructured(nomi string, src expr, at ast.Node) {
	if ast.IsDiscardName(nomi) {
		return
	}
	g.irBindLocal(at, nomi, src)
}

func (g *gen) destructureStruct(p *ast.StructPattern, src expr, at ast.Node) bool {
	if p.TypeName == nil {
		if src.k.tag == tagAnonStruct {
			// `{x, y} = point` over a RECORD. See anonstruct.go.
			return g.destructureAnonStruct(p, src, at)
		}
		// `|{x, y}: Point|` — an anonymous struct pattern over a NAMED struct.
		// A different feature from the record above: the pattern's field set is
		// a SUBSET of a DECLARATION's rather than the whole of a record's type,
		// so the field list comes from the def and the match is against a type
		// the pattern does not name. See destructureshape.go.
		return g.destructureNamedFields(p, src, at)
	}
	d := src.k.def
	if src.k.tag != tagNamed || d == nil || d.isDistinct {
		g.reject("pattern type mismatch",
			typeText(p.TypeName)+" against "+src.k.nomi(), at)
		g.probePattern(p)
		return false
	}
	if d.isEnum {
		return g.destructureVariantBraces(p, d, src, at)
	}
	if g.typeOf(p.TypeName) != src.k {
		g.reject("pattern type mismatch",
			typeText(p.TypeName)+" against "+d.nomi, at)
		g.probePattern(p)
		return false
	}
	return g.destructureFields(d, src, p, at)
}

// destructureVariantBraces binds `Enum.Variant{...}` in a destructuring
// parameter — the BRACE-shaped counterpart of destructureEnum's tuple shape.
//
// This is the third place the same match runs: `case` reaches the shape
// through case.go's structArm, and destructureStruct routes an enum here rather
// than refusing it as a pattern type mismatch. The corpus witness is
// 10-generics-and-type-wrappers/embedded_variants_test.nomi:78,
// `fn int_area(IntShape.IntCircle{r}): Int`, two lines below a tuple-shaped
// `NumericIdentifier.NumericUserId(n)`.
//
// NO TAG TEST IS EMITTED, and that is the whole difference from structArm: the
// pattern is IRREFUTABLE (variantPreamble admits only a single-variant enum, so
// there is no other variant the value could hold) and a destructuring parameter
// has no next arm to fall through to. structArm's `if subj.tag == v.tag {` opens
// a block precisely because a `case` arm may fail; here failure is unreachable,
// so the bindings are emitted straight into the enclosing block.
func (g *gen) destructureVariantBraces(p *ast.StructPattern, d *typeDef, src expr, at ast.Node) bool {
	owner, member, ok := patternHead(p.TypeName)
	if !ok {
		g.reject("destructuring parameter", "struct head "+typeText(p.TypeName), at)
		g.probeStructFields(p)
		return false
	}
	v, fine := g.variantPreamble(d, owner, member, at, func() { g.probeStructFields(p) })
	if !fine {
		return false
	}
	switch v.kind {
	case "embedded":
		// RULE (`embeds` payload coercion): the field patterns address the
		// EMBEDDED value's fields, one level down from the enum. Both
		// construction spellings arrive here — `Box.Extent{wide: 1}`, which
		// wraps at the literal, and a bare `Extent{wide: 1}` coerced at the
		// argument — because coerce() has already wrapped the second by the
		// time this runs. case.go's structArm says the same thing about the
		// `case` path.
		if v.embeds.isDistinct {
			g.reject("pattern type mismatch",
				d.nomi+"."+v.nomi+" embeds the distinct type "+v.embeds.nomi+", which has no fields", at)
			g.probeStructFields(p)
			return false
		}
		if v.embeds.isEnum {
			// case.go's structArm carries the reasoning; this is the
			// irrefutable-pattern half of the same rule. Both arms or neither:
			// one of two spellings guarded is the missing-arm defect, and an
			// unguarded `destructureFields` over an enum's empty field list
			// binds nothing and succeeds, which is a wrong answer rather than a
			// missing feature.
			g.reject("pattern type mismatch",
				d.nomi+"."+v.nomi+" embeds the enum "+v.embeds.nomi+", which has no fields", at)
			g.probeStructFields(p)
			return false
		}
		return g.destructureFields(v.embeds, g.payloadValue(at, d, v, 0, src), p, at)

	case "struct":
		return g.destructureVariantFields(d, v, src, p, at)

	default:
		g.reject("pattern type mismatch",
			d.nomi+"."+v.nomi+" is a "+v.kind+" variant and is not matched with braces", at)
		g.probeStructFields(p)
		return false
	}
}

// variantPreamble is the three questions a destructuring parameter asks of an
// enum before it can bind anything: does the qualifier name THIS enum, is the
// enum single-variant (which is what makes the pattern irrefutable), and does
// the member exist.
//
// One implementation, shared by the tuple shape and the brace shape, because
// the three refusals are properties of the ENUM and the HEAD and nothing about
// them depends on which node spelled the pattern. The probe differs — a payload
// sub-pattern against a field list — so it arrives as a closure, which is the
// shape attach.go already uses for the same reason.
//
// `len(d.variants) != 1` is a guard the checker also enforces (a refutable
// parameter pattern gets a "use a `case` in the body" diagnostic), kept here
// because "the checker rejects it" and "the builder cannot produce a wrong
// answer for it" are different claims and only the second is enforceable from
// here. See destructureEnum's header.
func (g *gen) variantPreamble(d *typeDef, owner, member string, at ast.Node, probe func()) (*variantDef, bool) {
	if owner != "" && !g.declaredAs(owner, d) {
		g.reject("pattern type mismatch", owner+"."+member+" against "+d.nomi, at)
		probe()
		return nil, false
	}
	if len(d.variants) != 1 {
		g.reject("refutable parameter pattern",
			d.nomi+" has "+strconv.Itoa(len(d.variants))+" variants", at)
		probe()
		return nil, false
	}
	v := d.variant(member)
	if v == nil {
		g.reject("type-qualified member", d.nomi+"."+member, at)
		probe()
		return nil, false
	}
	return v, true
}

// destructureFields binds a struct pattern's fields from a struct value —
// case.go's structFieldArms with the block-opening removed, since an
// irrefutable pattern has no arm to abandon.
func (g *gen) destructureFields(d *typeDef, src expr, p *ast.StructPattern, at ast.Node) bool {
	ok := true
	for _, f := range p.Fields {
		fd := d.field(f.Name)
		if fd == nil {
			g.reject("unknown struct field", d.nomi+"."+f.Name, at)
			ok = false
			continue
		}
		if !g.destructureField(f, g.irProjField(at, src, fd, f.Name), at) {
			ok = false
		}
	}
	return ok
}

// destructureVariantFields is destructureFields for a struct-shaped variant,
// whose payloads are fields in every respect except that they live on the
// variant and are read out of the enum's deduped slots — case.go's
// variantFieldArms, again without the block-opening.
//
// The slot is found by NAME rather than by the pattern's position, so
// `Cell.Span{hi, lo}` and `Cell.Span{lo, hi}` bind the same two values. That is
// the field-identity reading; reading by position would be the slot reading,
// and testdata/fn_destructure_variant.nomi lets the two disagree.
func (g *gen) destructureVariantFields(d *typeDef, v *variantDef, src expr, p *ast.StructPattern, at ast.Node) bool {
	ok := true
	for _, f := range p.Fields {
		idx := -1
		for i := range v.payloads {
			if v.payloads[i].nomi == f.Name {
				idx = i
				break
			}
		}
		if idx < 0 {
			g.reject("unknown struct field", d.nomi+"."+v.nomi+"."+f.Name, at)
			ok = false
			continue
		}
		if !g.destructureField(f, g.payloadValue(at, d, v, idx, src), at) {
			ok = false
		}
	}
	return ok
}

// destructureField is what one matched field does with the value it addresses:
// recurse into a sub-pattern, bind a rename, or pun on the field's own name.
func (g *gen) destructureField(f ast.StructPatternField, fe expr, at ast.Node) bool {
	switch {
	case f.Pattern != nil:
		return g.destructure(f.Pattern, fe, at)
	case f.Binding != "":
		g.bindDestructured(f.Binding, fe, at)
	default:
		// Punning: `Point{x, y}` binds each field to its own name.
		g.bindDestructured(f.Name, fe, at)
	}
	return true
}

// destructureAnonStruct binds `{x, y: p} = record` — destructureStruct with the
// field list read from the interned record kind rather than from a declaration.
func (g *gen) destructureAnonStruct(p *ast.StructPattern, src expr, at ast.Node) bool {
	ok := true
	for _, f := range p.Fields {
		fk, found := anonFieldKind(src.k, f.Name)
		if !found {
			g.reject("unknown struct field", src.k.nomi()+"."+f.Name, at)
			ok = false
			continue
		}
		if !g.destructureField(f, g.irProjRecordField(at, src, f.Name, fk), at) {
			ok = false
		}
	}
	return ok
}

// destructureEnum unwraps a distinct type or a single-variant enum.
//
// Only a SINGLE-variant enum: with two variants the pattern names one of them
// and could fail to match, which is a refutable pattern and the checker's job
// to reject. The guard is here anyway, because "the checker rejects it" and
// "the builder cannot produce a wrong answer for it" are different claims and
// only the second one is enforceable from here.
func (g *gen) destructureEnum(p *ast.EnumPattern, src expr, at ast.Node) bool {
	owner, member, ok := patternHead(p.Variant)
	if !ok {
		g.reject("destructuring parameter", "variant head "+typeText(p.Variant), at)
		g.probePattern(p.Payload)
		return false
	}
	d := src.k.def
	if src.k.tag != tagNamed || d == nil {
		g.reject("pattern type mismatch", member+" against "+src.k.nomi(), at)
		g.probePattern(p.Payload)
		return false
	}

	var inner expr
	switch {
	case d.isDistinct:
		if owner != "" || member != d.nomi {
			g.reject("pattern type mismatch", member+" against the distinct type "+d.nomi, at)
			g.probePattern(p.Payload)
			return false
		}
		// kindInvalid: reports — rejects `marker destructuring`; a bare marker pattern is fine.
		if d.inner == kindInvalid {
			// `type Expired` carries nothing, so `Expired(x)` has no value to
			// bind. A bare `Expired` pattern binds nothing and is fine.
			if p.Binding == "" && p.Payload == nil {
				return true
			}
			g.reject("marker destructuring", d.nomi+" carries no value", at)
			g.probePattern(p.Payload)
			return false
		}
		// A wrapping distinct is a Go DEFINED type, so unwrapping is a
		// conversion and costs nothing. `ir.ProjInner`, and NO `ir.Match`: a
		// value of that type can only be that type.
		inner = g.irProjInner(at, src, d, d.inner)

	case d.isEnum:
		v, fine := g.variantPreamble(d, owner, member, at, func() { g.probePattern(p.Payload) })
		if !fine {
			return false
		}
		if p.Binding == "" && p.Payload == nil {
			return true
		}
		if len(v.payloads) != 1 {
			// A struct-shaped variant carries several values and the pattern
			// names one binding for them, which only a nested struct pattern
			// can express. `case` owns that shape.
			g.reject("destructuring parameter",
				d.nomi+"."+member+" carries "+strconv.Itoa(len(v.payloads))+" values", at)
			g.probePattern(p.Payload)
			return false
		}
		inner = g.payloadValue(at, d, v, 0, src)

	default:
		g.reject("variant pattern on a struct", d.nomi+" against "+member, at)
		g.probePattern(p.Payload)
		return false
	}

	if p.Payload != nil {
		return g.destructure(p.Payload, inner, at)
	}
	g.bindDestructured(p.Binding, inner, at)
	return true
}

// patternText is a pattern's spelling for a refusal's detail, which the AST
// does not carry: a pattern node has no TypeString and reconstructing one would
// be a second pretty-printer. The node kind plus the position the refusal
// already carries is enough to find it.
func patternText(pat ast.Node) string {
	if isNilNode(pat) {
		return "<pattern>"
	}
	return constructName(pat)
}

// --- capture ----------------------------------------------------------------
