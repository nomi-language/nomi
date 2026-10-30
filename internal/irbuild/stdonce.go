package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A `once` binding of a stdlib module: one declared inside an `impl` block,
// read as `Type.name`, and one declared at file level, read file-qualified
// (`io.stdin`, `io.stdout`, `io.stderr`) or, when private, by the file's own
// bodies (std/duration's `max_nanos`, std/uuid's `sign_bit`).
//
// A stream's value is a marker naming the stream (rt.IOStdout), so caching it
// changes nothing about where a write goes: that is decided at each write,
// which is why `io.capture` still redirects a stream read before it began.
//
// # Why the value is emitted, rather than mapped to a Go constant
//
// A registry row pairing a Nomi name with a Go constant would be a SECOND
// encoding of a value the Nomi source already defines, and the two would agree
// by inspection rather than by construction. This lowers the declaration
// instead: the RHS is built as an ordinary function body, so the value the
// program computes is the value the Nomi source computes.
//
// # Semantics come from rt.OnceCell
//
// Spec §2 gives `once` three properties (lazy, exactly-once, cycle-rejecting)
// and once.go explains why no simpler Go encoding holds all three. Nothing about
// those properties changes for a stdlib binding, so the emitted shape is
// onceDecl's, unchanged: an RHS function, an rt.OnceCell, and an exported getter
// that forces through it.
//
// The cell's name is `Type.name` (`typeName + "." + n.Name`), and it is not a
// free choice: rt.CyclicOnceText renders that name in the cycle diagnostic, and
// the golden files record it. A cell named without its type here would print
// a different diagnostic for the same cycle.
//
// # Ordering: onces settle in the LEAST fixed point, never in the recursive one
//
// A stdlib function may read a once (`Duration.parse`) and a once's RHS may call
// a function, so the two settle in one interleaved fixed point — see
// stdLeastFixedPoint, which tries the onces first each round because a once RHS
// reading a SIBLING once needs it published, and declaration order gives that
// within a single round.
//
// They are deliberately absent from stdRecursiveClosure. That phase exists to
// admit mutually-recursive FUNCTIONS behind a tail-call interlock; a `once` cycle
// is a diagnosed Nomi error, not a shape to admit, so a once that has not settled
// by the least fixed point is refused as `unlowered stdlib once binding` and its
// reference sites name that rather than falling through to `type-qualified
// reference`.

// stdOnce is one stdlib `once` binding as a reference site sees it.
type stdOnce struct {
	// key is `<module>.<Type>.<name>`, the same shape stdKey builds for a
	// function, so a refusal names the binding the way the rest of the builder
	// names a stdlib declaration.
	key    string
	module string
	// recv is the impl block's receiver type name ("Int"). Empty for a
	// file-level `once` (std/io's `stdin`, `stdout` and `stderr`), which a
	// program reads file-qualified (`io.stdout`) and its own module bare;
	// stdFileOnceValue resolves both through the checker.
	recv string
	name string
	pub  bool
	k    kind

	// pkg is the stdlib package name that qualifies the three in `expr` text.
	pkg string

	decl *ast.OnceBinding
	// settled is set once the RHS has been shown to lower. PESSIMISTIC, for
	// stdFunc.arityMin's reason: a reference built to an unsettled binding
	// would name a cell the module then declined to retain.
	settled bool
	// why names the refusal when this binding is not lowerable. Exactly one of
	// settled and why is set once lowerStdlibModule has finished.
	why string
	// irCell is the declaring module's IR lazy cell, set when the initializer
	// was retained. Retained reads in any module name this symbol.
	irCell *ir.Symbol
}

// nomiName is the binding as Nomi spells it, which is also the name rt's cyclic
// diagnostic prints. See the file comment.
func (o *stdOnce) nomiName() string {
	if o.recv == "" {
		return o.name
	}
	return o.recv + "." + o.name
}

// collectStdOnces reads every `once` a stdlib module declares, at top level and
// inside an impl block.
//
// Impl blocks are walked whatever their header, registering under the receiver
// regardless. The
// analyzer forbids a `once` in an INTERFACE impl block ("interface impl blocks
// hold only `fn` / `host fn` implementation items", asserted by
// once_bindings_test.nomi), so the qualification would change nothing on checked
// input and adding it would be a second place for that rule to live.
func collectStdOnces(module, pkg string, nodes []ast.Node, fa *analysis.FileAnalysis, anchors stdAnchors) []*stdOnce {
	var out []*stdOnce
	add := func(recv string, ob *ast.OnceBinding) {
		o := &stdOnce{
			key:    stdKey(module, recv, "", ob.Name),
			module: module,
			recv:   recv,
			name:   ob.Name,
			pub:    ob.Public,
			pkg:    pkg,
			decl:   ob,
		}
		// The keys are once.go's OWN, not a parallel `stdlib once binding …`
		// family. Three of the four reasons below are the ones declareOnces
		// gives for a user-code `once`, and a second spelling would count one
		// obstacle as two. Only the fixed-point outcome (closeStdOnces) is new,
		// because user code has no fixed point.
		switch {
		case ob.Value == nil:
			o.why = "once binding without a value"
		case ob.TypeAnnotation == nil:
			// The checker's solved type, which the module's FileAnalysis
			// records at the declaration. Same keys declareOnces uses.
			o.k = stdInferredOnceKind(fa, ob)
			// kindInvalid: no-position — classifies a stdlib declaration into o.why; no Nomi position.
			if o.k == kindInvalid {
				o.why = "once binding without a determinable type"
			}
		default:
			o.k = stdTypeKind(ob.TypeAnnotation, anchors)
			// The annotation names a type with no representation, exactly as a
			// signature position would.
			// kindInvalid: no-position — classifies a stdlib declaration into o.why; no Nomi position.
			if o.k == kindInvalid {
				o.why = "non-scalar once binding type"
			}
		}
		out = append(out, o)
	}
	for _, n := range nodes {
		switch t := n.(type) {
		case *ast.OnceBinding:
			add("", t)
		case *ast.ImplBlock:
			recv := analysis.TypeExprBaseName(t.Receiver)
			for _, item := range t.Items {
				if ob, isOnce := item.(*ast.OnceBinding); isOnce {
					add(recv, ob)
				}
			}
		}
	}
	return out
}

// stdInferredOnceKind is the kind of an unannotated stdlib `once`: the type
// the checker solved for it (checkOnce records it on the declaration's
// symbol), projected as stdTypeKind's scalar arm projects an annotation.
// Nothing else, as there: a stdlib once of any other type would need its own
// representation here, and refusing it names that.
func stdInferredOnceKind(fa *analysis.FileAnalysis, ob *ast.OnceBinding) kind {
	if fa == nil {
		return kindInvalid
	}
	sym, found := fa.Definitions[analysis.Pos{Line: ob.Line, Col: ob.Col}]
	if !found || sym.Kind != analysis.SymbolOnce || sym.Name != ob.Name {
		return kindInvalid
	}
	t := sym.Type
	for {
		tv, isVar := t.(*analysis.TypeVar)
		if !isVar || tv.Resolved == nil {
			break
		}
		t = tv.Resolved
	}
	switch t {
	case analysis.TypeInt:
		return kindInt
	case analysis.TypeFloat:
		return kindFloat
	case analysis.TypeString:
		return kindString
	case analysis.TypeBool:
		return kindBool
	case analysis.TypeUnit:
		return kindUnit
	}
	return kindInvalid
}

// addOnce files a binding under the type-qualified spelling a reference site
// writes.
//
// One slot rather than byType's overload set: a `once` takes no arguments, so
// there is nothing for a reference to select on.
//
// A collision KEEPS THE FIRST rather than refusing both, which is the opposite
// of addOverload and deliberately so. addOverload refuses because two functions
// with the same parameter kinds are genuinely indistinguishable at a call site,
// so picking one would answer by map arrival order. Here a collision is not
// reachable at all: two `once`s of one name in one impl block is an analyzer
// error, and two modules cannot share a spelling because the spelling carries
// the receiver TYPE and a type is declared in one module. So an `ambiguous
// stdlib once binding` key would be a refusal with a structurally empty
// population — a name in the report that nothing can ever produce — and this
// repo has already paid for one guard that could not fire. Keeping the first is
// declareOnces' own answer for the same unreachable case, and it is the one that
// cannot emit two Go declarations under one identifier.
func (x *stdlibIndex) addOnce(o *stdOnce) {
	if x.byOnce == nil {
		return
	}
	key := o.nomiName()
	if o.recv == "" {
		// A file-level binding (`io.stdout`) is reached through the checker's
		// record of its declaration (stdFileOnceValue), never by spelling,
		// so its key carries the module and two modules' names cannot meet.
		key = o.key
	}
	if _, taken := x.byOnce[key]; taken {
		return
	}
	x.byOnce[key] = o
}

// bindStdOnces installs the module's OWN once table on the gen.
//
// A field of its own rather than a slot in the local stdlibIndex, because
// bindStdSiblings REBUILDS that index for phase 2 and a once is not part of the
// phase split: it settled in the least fixed point or it did not. The lookup
// order in stdOnceRef is this table then the index, which is the same
// this-module-then-earlier-modules order stdSiblings and g.std already have.
func (g *gen) bindStdOnces(onces []*stdOnce) {
	if len(onces) == 0 {
		return
	}
	g.stdOnces = make(map[string]*stdOnce, len(onces))
	for _, o := range onces {
		g.stdOnces[o.nomiName()] = o
	}
}

// closeStdOnces refuses every binding the fixed point never settled, so a
// reference names the binding instead of falling through to the generic
// `type-qualified reference`.
func closeStdOnces(onces []*stdOnce) {
	for _, o := range onces {
		if !o.settled && o.why == "" {
			o.why = "unlowered stdlib once binding"
		}
	}
}

// emitStdOnce lowers one binding's initializer into the module's IR lazy cell.
//
// onceDecl's shape, and deliberately not onceDecl itself: that function resolves
// its *onceDef through g.onces, which a stdlib gen does not populate, and it
// reports `once binding` for a declaration it cannot find. The body lowering is
// shared: irOnceCellLower is the same call, so the RHS gets the same scope
// stack and the same refusals any other body gets.
func (g *gen) emitStdOnce(o *stdOnce) {
	g.at(o.decl.Line)
	// The RHS is its own body, so no enclosing result, loop boundary,
	// lambda result inference or tail label reaches it. Saved rather than
	// assumed for onceDecl's reason: this also runs under speculate while
	// something else is being walked.
	prevResult, prevInfer, prevCtrl, prevTail := g.result, g.inferResult, g.ctrl, g.tail
	g.result, g.inferResult, g.ctrl, g.tail = o.k, nil, nil, nil
	defer func() {
		g.result, g.inferResult, g.ctrl, g.tail = prevResult, prevInfer, prevCtrl, prevTail
	}()

	g.pushScope()
	sym := g.irTypes().Symbol(o, o.nomiName())
	produced, retained := g.irOnceCellLower(o.decl, o.k, sym, o.nomiName())
	if !retained {
		g.stdUnloweredFunc("once "+o.key, o.key, irTakeDeclineWhy())
		produced = o.k
	}
	if g.irMod != nil && g.irMod.Cell(sym) != nil {
		o.irCell = sym
	}
	switch {
	case produced == kindInvalid:
		// kindInvalid: reports — the RHS named its own blocker; declining here
		// avoids reporting the same gap twice under a type-mismatch key.
		g.suppress(o.decl)
	case produced != o.k:
		g.reject("once binding type mismatch",
			o.key+" is "+o.k.nomi()+" but its value is "+produced.nomi(), o.decl)
	}
	g.popScope()
}

// stdOnceValue reads a stdlib `once` whose initializer the declaring module
// retained as an IR lazy cell. Go delivery is stdOnceRef's getter.
func (bl *irScalarBuilder) stdOnceValue(t *ast.FieldAccess) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	g := bl.g
	owner, ok := t.Object.(*ast.TypeIdent)
	if !ok || t.Field == nil {
		return no()
	}
	key := owner.Name + "." + t.Field.Name
	o := g.stdOnces[key]
	if o == nil && g.std != nil {
		o = g.std.byOnce[key]
	}
	if o == nil || !o.settled || o.irCell == nil || !irRetainedValueKind(o.k) {
		return no()
	}
	n := ir.NewRefOnce(g.irNodePos(t), bl.f.NewTemp(), o.irCell)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: o.k})
	return n.Dst(), o.k, false, true
}

// stdFileOnceValue reads a file-level stdlib `once` (`io.stdout`, or bare
// `stdout` inside std/io), at the name's position: the checker records the
// binding's declaration there whatever qualifier spelled the module, so the
// read resolves through that record and the declaration, not the text.
func (bl *irScalarBuilder) stdFileOnceValue(at ast.Node, line, col int) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	g := bl.g
	fa := g.srcFA()
	if fa == nil {
		return no()
	}
	sym := fa.References[analysis.Pos{Line: line, Col: col}]
	for sym != nil && sym.Resolved != nil {
		sym = sym.Resolved
	}
	if sym == nil || sym.Kind != analysis.SymbolOnce || sym.OwningType != "" {
		return no()
	}
	ob, isOnce := sym.Node.(*ast.OnceBinding)
	if !isOnce {
		return no()
	}
	var o *stdOnce
	for _, c := range g.stdOnces {
		if c.decl == ob {
			o = c
		}
	}
	if o == nil && g.std != nil {
		for _, c := range g.std.byOnce {
			if c.decl == ob {
				o = c
			}
		}
	}
	if o == nil && fa.Definitions[analysis.Pos{Line: ob.Line, Col: ob.Col}] == sym {
		// The module's own binding read bare in its source checked anew
		// (a reference editor's `stdout` in std/io, vmhost.StdlibReference):
		// the checker records the edited copy's declaration, and the cell is
		// the shared copy's. A file declares one file-level `once` of a name.
		if c := g.stdOnces[ob.Name]; c != nil && c.recv == "" {
			o = c
		}
	}
	if o == nil || o.recv != "" || !o.settled || o.irCell == nil || !irRetainedValueKind(o.k) {
		return no()
	}
	n := ir.NewRefOnce(g.irNodePos(at), bl.f.NewTemp(), o.irCell)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: o.k})
	return n.Dst(), o.k, false, true
}
