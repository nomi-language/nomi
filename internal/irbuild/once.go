package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Module-level `once` bindings.
//
// # What has to survive the lowering, and what breaks each cheap answer
//
// Spec §2 gives `once` three properties, and the two obvious Go encodings each
// break a different one:
//
//   - LAZY. The RHS runs on first access, so `once a = b + 1` may sit ABOVE
//     `once b = 5` and definition order is irrelevant. A Go package-level `var`
//     is eager, and Go's initialization order is its own graph rather than
//     Nomi's — so an RHS reaching through a function call Go cannot see into is
//     ordered wrongly, silently.
//   - EXACTLY ONCE. The RHS's side effects happen once and the first force is
//     serialized across them. An eager `var` also runs the RHS in a program
//     that never reads the binding, which is observable whenever the RHS
//     prints or fails.
//   - CYCLE-REJECTING. `once a = a`, and the interesting form, a cycle through
//     an ordinary function call, are a diagnosed Nomi error rather than a hang.
//     `sync.Once.Do` re-entered on one goroutine DEADLOCKS, so the shape that
//     covers the first two guarantees breaks the third by itself.
//
// So the value lives in an rt.OnceCell — sync.Once's mutex-plus-atomic shape
// with the cycle test in front of the lock. See rt/once.go for why the lineage is a cons-list on
// the frame rather than a flag on the cell, and why a static acyclicity proof
// was rejected.
//
// # Three declarations per binding, and why the RHS is its own function
//
//	func nomiOnceRHS_x(fr *rt.Frame) int64 { ... }
//	var nomiOnceCell_x = rt.NewOnceCell[int64]("x")
//	func NomiOnce_x(fr *rt.Frame) int64 { return nomiOnceCell_x.Get(fr, nomiOnceRHS_x) }
//
// The RHS as a named top-level function rather than a closure at the Get site:
// a closure over nothing still costs a func value at the call, and — the
// reason that matters — the RHS is an ordinary generated function body, built
// as IR and read back like a `fn` body (irOnceLower), with the same scope
// stack, `//line` attribution and refusals. `fr` is the parameter it
// receives, which is what makes the forcing lineage rt.OnceCell.force
// installed reach everything the RHS calls.
//
// The getter is exported (`NomiOnce_`) for the same reason a lowered function
// is: another generated package may read a `pub once`.
//
// # A once is not a local, and the lookup order says so
//
// `max_retries = 5` inside a function SHADOWS the file's `once max_retries`
// (Nomi's ordinary immutable-rebind rule), so the Ident arm tries
// the lexical scope first and only then this table. Getting that backwards
// would make a shadowing local silently read the once.

// onceDef is one module-level `once` as its reference sites see it.
type onceDef struct {
	nomi string
	k    kind
	decl *ast.OnceBinding
	pub  bool
	// why is the reason this binding was refused, empty when it lowered. A
	// reference to a refused once does NOT re-report it: the gap is already in
	// the tally at the declaration, and echoing it per use is the derived-count
	// shape cascade.go exists to prevent.
	why string
}

func (o *onceDef) lowerable() bool { return o.why == "" }

// declareOnces types every module-level `once` before any body is emitted.
//
// Before, for the same reason function signatures are collected before bodies:
// a `once` may be read by a function declared above it, and by another `once`
// declared above it, and Nomi has no forward-declaration rule. Its kind
// therefore has to exist before anything that mentions it is walked.
func (g *gen) declareOnces() {
	for _, n := range g.nodes {
		switch t := n.(type) {
		case *ast.OnceBinding:
			g.declareOnce(t, "")
		case *ast.ImplBlock:
			// An owner-level `once` (`impl Policy { pub once default: Policy
			// = ... }`), read as `Policy.default`. Only an inherent block can
			// hold one: the analyzer rejects a `once` in an interface impl.
			if t.Interface != nil {
				continue
			}
			recv := analysis.TypeExprBaseName(t.Receiver)
			for _, item := range t.Items {
				if ob, isOnce := item.(*ast.OnceBinding); isOnce {
					g.declareOnce(ob, recv)
				}
			}
		}
	}
}

// declareOnce types one `once`. owner is the inherent impl block's receiver
// name, empty for a file-level binding. An owner-level binding is filed under
// `Owner.name`, the spelling a reference site writes and the name rt's cycle
// diagnostic prints (stdOnce.nomiName gives a stdlib one the same name). The
// dotted key cannot collide with a file-level binding, whose name is an
// identifier, so a bare reference never reaches an owner-level one.
func (g *gen) declareOnce(ob *ast.OnceBinding, owner string) {
	name := ob.Name
	if owner != "" {
		name = owner + "." + ob.Name
	}
	if _, dup := g.onces[name]; dup {
		// Two `once`s with one name is an analyzer error, so this is
		// unreachable on checked input; keeping the first is the answer
		// that cannot emit two declarations under one identifier.
		return
	}
	d := &onceDef{
		nomi: name,
		decl: ob,
		pub:  ob.Public,
	}
	switch {
	case ob.Value == nil:
		d.why = "once binding without a value"
	case ob.TypeAnnotation != nil:
		d.k = g.typeOf(ob.TypeAnnotation)
		// kindInvalid: reports — onceDecl rejects `non-scalar once binding type` at the declaration.
		if d.k == kindInvalid {
			d.why = "non-scalar once binding type"
		}
	default:
		d.k = g.inferredOnceKind(ob)
		// kindInvalid: reports — onceDecl rejects `once binding without a determinable type`.
		if d.k == kindInvalid {
			d.why = "once binding without a determinable type"
		}
	}
	g.onces[name] = d
	// And under the node, because a reference from another file resolves
	// to the DECLARATION and not to any spelling of it. The two maps hold
	// one *onceDef, so they cannot describe the binding differently.
	g.oncesByDecl[ob] = d
	g.onceOrder = append(g.onceOrder, d)
}

// inferredOnceKind is the type the checker solved for an unannotated `once`,
// projected into a kind.
//
// checkOnce records it on the binding's own symbol (`sym.Type`) at the
// binding's position — the same record LSP hover reads to render
// `once default_port: Int = 8080`. This is inferred.go's doctrine applied to
// it: the answer is plumbed, never re-derived, and an analysis.Type the
// builder cannot represent projects to kindInvalid so the caller refuses.
func (g *gen) inferredOnceKind(ob *ast.OnceBinding) kind {
	if g.fa == nil {
		return kindInvalid
	}
	sym, found := g.fa.Definitions[analysis.Pos{Line: ob.Line, Col: ob.Col}]
	if !found || sym.Kind != analysis.SymbolOnce || sym.Name != ob.Name {
		// The predicate mirrors what checkOnce wrote — position, kind and NAME
		// — so this asks the same question the recording answered and cannot
		// read some other symbol sharing a position.
		return kindInvalid
	}
	return g.project(sym.Type)
}

// onceDecl emits one `once` binding's three declarations.
func (g *gen) onceDecl(ob *ast.OnceBinding) {
	g.at(ob.Line)
	d := g.oncesByDecl[ob]
	if d == nil {
		// A `once` nested in a function body or a type body — neither of which
		// declareOnces walked, and both of which the analyzer rejects, so this
		// is belt and braces rather than a live path.
		g.rejectWhole("once binding", ob.Name, ob)
		return
	}
	if !d.lowerable() {
		detail := d.nomi
		if ob.TypeAnnotation != nil {
			detail = d.nomi + ": " + ob.TypeAnnotation.TypeString()
		}
		g.reject(d.why, detail, ob)
		return
	}

	// The RHS is a function body, emitted through the machinery a `fn` uses:
	// its own result kind, its own scope, no enclosing loop boundary and no
	// enclosing lambda's result inference. They are saved rather than
	// assumed, so a caller's state never leaks in or out.
	// g.tail alongside g.ctrl, for tail.go's reason: a `once` initializer is
	// its own Go function, so a tail label from elsewhere does not reach it.
	prevResult, prevInfer, prevCtrl, prevTail := g.result, g.inferResult, g.ctrl, g.tail
	g.result, g.inferResult, g.ctrl, g.tail = d.k, nil, nil, nil
	defer func() {
		g.result, g.inferResult, g.ctrl, g.tail = prevResult, prevInfer, prevCtrl, prevTail
	}()

	g.pushScope()
	produced, retained := g.irOnceLower(d)
	if !retained {
		g.unloweredBody(irDeclined{})
		produced = d.k
	}
	switch {
	case produced == kindInvalid:
		// kindInvalid: reports — the RHS named its own blocker; declining here
		// avoids reporting the same gap twice under a type-mismatch key.
		g.suppress(ob)
	case produced != d.k:
		g.reject("once binding type mismatch",
			d.nomi+" is "+d.k.nomi()+" but its value is "+produced.nomi(), ob)
	}
	g.popScope()
}

// refSiblingOnce lowers a resolved cross-file `once` read. Shared by the
// qualified and the bare spelling, because everything past resolution is
// identical: the same declaration, the same visibility rule, the same cycle
// question and the same kind translation. Two spellings of one reference must
// not be able to answer differently — the reason siblingCall routes
// through callSibling rather than growing a second body.
func (g *gen) refSiblingOnce(at ast.Node, to int, d *onceDef) (kind, bool) {
	// Not a `once` at all, or a private one — the latter falls through to the
	// caller's own refusal rather than getting a key of its own, because `pub`
	// governs cross-file visibility and the analyzer rejects the import first
	// ("'secret' is private and cannot be imported"), so a privacy key here
	// would report a gap nothing can reach.
	if d == nil || (!d.pub && to != g.fileUnit) {
		return kindInvalid, false
	}
	unit := g.files.units[to]
	key := unit.key + "." + d.nomi
	if !d.lowerable() {
		g.reject(d.why, key, at)
		return kindInvalid, true
	}
	// The kind belongs to the DECLARING package, so it is translated before
	// use: a mirror is a different pointer from the owner's def. A translation
	// that fails names its own reason.
	result, ok := g.importKind(d.k)
	if !ok {
		g.reject(g.importWhy, key+": "+d.k.nomi(), at)
		return kindInvalid, true
	}
	return result, true
}
