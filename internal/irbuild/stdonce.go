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
// # Why the value is lowered, rather than mapped to a Go constant
//
// A registry row pairing a Nomi name with a Go constant would be a SECOND
// encoding of a value the Nomi source already defines, and the two would agree
// by inspection rather than by construction. This lowers the declaration
// instead: the RHS is built as an ordinary function body, so the value the
// program computes is the value the Nomi source computes.
//
// # Semantics come from the IR's lazy cell
//
// Spec §6 *`once` Bindings* gives `once` three properties (lazy,
// exactly-once, cycle-rejecting). Nothing about those properties changes for
// a stdlib binding, so its initializer is lowered into a lazy cell by the
// call a program's `once` goes through (irOnceCellLower).
//
// The cell's name is `Type.name` (nomiName), and it is not a free choice:
// rt.CyclicOnceText renders that name in the cycle diagnostic, and the golden
// files record it. A cell named without its type here would print a
// different diagnostic for the same cycle.
//
// # Ordering: onces settle with the bodies
//
// A stdlib function may read a once (`Duration.parse`) and a once's RHS may call
// a function of its module, so the two settle in one fixed point, the
// prebuild's (irPrebuildStdBodies): each binding's cell is named ahead so a
// body reading it builds, its RHS builds once the bodies it calls have, and a
// binding whose RHS does not build is withdrawn with the bodies and refused as
// `unlowered stdlib once binding`, which its reference sites name rather than
// falling through to `type-qualified reference`. The settled bindings are
// then lowered before the bodies, inside the window where every offered
// sibling is bound.

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
	k    kind

	decl *ast.OnceBinding
	// settled is set once the RHS has been shown to lower. PESSIMISTIC, for
	// stdFunc.canon's reason: a reference built to an unsettled binding
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
func collectStdOnces(module string, nodes []ast.Node, fa *analysis.FileAnalysis) []*stdOnce {
	var out []*stdOnce
	add := func(recv string, ob *ast.OnceBinding) {
		o := &stdOnce{
			key:    stdKey(module, recv, "", ob.Name),
			module: module,
			recv:   recv,
			name:   ob.Name,
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
			// records at the declaration, projected with no gen.
			o.k = projectShared(onceSymbolType(fa, ob))
			// kindInvalid: no-position — classifies a stdlib declaration into o.why; no Nomi position.
			if o.k == kindInvalid {
				o.why = "once binding without a determinable type"
			}
		default:
			o.k = projectShared(stdTypeExprType(fa, ob.TypeAnnotation))
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

// addOnce files a binding under the type-qualified spelling a reference site
// writes.
//
// One slot rather than byType's overload set: a `once` takes no arguments, so
// there is nothing for a reference to select on.
//
// A collision KEEPS THE FIRST, in lowering order. Within one module there is
// none: two `once`s of one name in one impl block is an analyzer error. Two
// modules can collide, because the spelling carries the receiver type's NAME
// and several modules declare a type of one name (18 declare an `Error`;
// std/http and std/process each declare a `Status`). No two std modules
// declare a `once` of one spelling today, and nothing holds that: if two did,
// a reference to the later module's binding would read the earlier one's.
// Functions are found by identity (stdlibIndex.byID), which carries the
// module; a type-qualified `once` is still found by spelling.
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
// order in stdOnceValue is this table then the index, which is the same
// this-module-then-earlier-modules order stdSiblingDecls and g.std already have.
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
	// The RHS is its own body, so no enclosing result reaches it. Saved
	// rather than assumed for onceDecl's reason: this also runs under
	// speculate while something else is being walked.
	prevResult := g.result
	g.result = o.k
	defer func() { g.result = prevResult }()

	sym := g.irTypes().Symbol(o, o.nomiName())
	produced, retained := g.irOnceCellLower(o.decl, o.k, sym, o.nomiName())
	if !retained {
		g.stdUnloweredFunc("once "+o.key, o.key, g.dc.take().why)
		produced = o.k
	}
	// Set only when the cell is there: the prebuild names it ahead
	// (irPrebuildStdBodies), and a reference must not outlive a refusal.
	o.irCell = nil
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
}

// stdOnceValue reads a stdlib `once` an owner declares (`Locale.root`),
// whose initializer the declaring module retained as an IR lazy cell: the
// one the checker resolved the name to (stdOnceOf).
func (bl *irScalarBuilder) stdOnceValue(t *ast.FieldAccess) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	g := bl.g
	if _, ok := t.Object.(*ast.TypeIdent); !ok || t.Field == nil {
		return no()
	}
	sym := g.checkedRef(t.Field.Line, t.Field.Col)
	if sym == nil || sym.Kind != analysis.SymbolOnce {
		return no()
	}
	ob, isOnce := sym.Node.(*ast.OnceBinding)
	if !isOnce {
		return no()
	}
	o := g.stdOnceOf(ob)
	if o == nil || !o.settled || o.irCell == nil || !irOnceKind(o.k) {
		return no()
	}
	n := ir.NewRefOnce(g.irNodePos(t), bl.f.NewTemp(), o.irCell)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: o.k})
	return n.Dst(), o.k, false, true
}

// stdOnceOf is the std `once` ob declares: this std gen's own, or one the
// std index holds; nil for none.
func (g *gen) stdOnceOf(ob *ast.OnceBinding) *stdOnce {
	for _, c := range g.stdOnces {
		if c.decl == ob {
			return c
		}
	}
	if g.std != nil {
		for _, c := range g.std.byOnce {
			if c.decl == ob {
				return c
			}
		}
	}
	return nil
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
	o := g.stdOnceOf(ob)
	if o == nil && fa.Definitions[analysis.Pos{Line: ob.Line, Col: ob.Col}] == sym {
		// The module's own binding read bare in its source checked anew
		// (a reference editor's `stdout` in std/io, vmhost.StdlibReference):
		// the checker records the edited copy's declaration, and the cell is
		// the shared copy's. A file declares one file-level `once` of a name.
		if c := g.stdOnces[ob.Name]; c != nil && c.recv == "" {
			o = c
		}
	}
	if o == nil || o.recv != "" || !o.settled || o.irCell == nil || !irOnceKind(o.k) {
		return no()
	}
	n := ir.NewRefOnce(g.irNodePos(at), bl.f.NewTemp(), o.irCell)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: o.k})
	return n.Dst(), o.k, false, true
}
