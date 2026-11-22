package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Module-level `once` bindings.
//
// # What the lowering keeps
//
// Spec §6 *`once` Bindings* gives `once` three properties:
//
//   - Lazy. The RHS runs on first access, so `once a = b + 1` may sit above
//     `once b = 5` and definition order is irrelevant.
//   - Exactly once. The RHS's side effects happen once and the first force is
//     serialized across them. A program that never reads the binding never
//     runs the RHS, which is observable whenever the RHS prints or fails.
//   - Cycle-rejecting. `once a = a`, and a cycle through an ordinary function
//     call, are a diagnosed Nomi error rather than a hang.
//
// So the value lives in an rt.OnceCell, which the VM makes one of per cell
// (internal/vm/once.go): sync.Once's mutex-plus-atomic shape with the cycle
// test in front of the lock. See rt/once.go for why the lineage is a
// cons-list on the frame rather than a flag on the cell, and why a static
// acyclicity proof was rejected.
//
// # The cell and its initializer
//
// A binding lowers to a lazy cell the module declares under the binding's
// symbol (`ir.Module.DeclareLazyCell`, irOnceCellLower) and an initializer
// that is an `ir.Func` of its own. The RHS is built as a `fn` body is, with
// the same scope stack and refusals. A read is an `ir.RefOnce` naming the
// cell's symbol, which the VM forces on the reading activation's frame, so
// the forcing lineage reaches everything the RHS calls.
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

func (o *onceDef) lowerable() bool { return o.whyNot() == "" }

// whyNot is why the binding does not lower, empty when it does: the reason
// declareOnce refused it, or a kind no cell holds (irOnceKind). A kind the
// value domain does not hold is a gap in the builder, refused at the
// declaration as the reading sites decline it.
func (o *onceDef) whyNot() string {
	if o.why != "" {
		return o.why
	}
	if !irOnceKind(o.k) {
		return "a once of a kind outside the domain: " + o.k.nomi()
	}
	return ""
}

// declareOnces types every module-level `once` before any body is lowered.
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
		// that cannot declare two cells under one name.
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
	return g.project(onceSymbolType(g.fa, ob))
}

// onceDecl lowers one `once` binding: its cell and its initializer.
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
		g.reject(d.whyNot(), detail, ob)
		return
	}

	// The RHS is a function body, lowered through the machinery a `fn` uses:
	// its own result kind and its own scope. They are saved rather than
	// assumed, so a caller's state never leaks in or out.
	prevResult := g.result
	g.result = d.k
	defer func() { g.result = prevResult }()

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
}

// refSiblingOnce lowers a resolved cross-file `once` read. Shared by the
// qualified and the bare spelling, because everything past resolution is
// identical: the same declaration, the same visibility rule, the same cycle
// question and the same kind translation. Two spellings of one reference must
// not be able to answer differently.
func (g *gen) refSiblingOnce(at ast.Node, to int, d *onceDef) (kind, bool) {
	// Not a `once` at all, or a private one read from a file that is not its
	// own or its partner test (spec §14 *Visibility*), as for a private
	// function (siblingPrivate). The latter falls through to the caller's
	// own refusal rather than getting a key of its own, because the analyzer
	// rejects that read first ("'secret' is private and cannot be
	// imported"), so a privacy key here would report a gap nothing can
	// reach.
	if d == nil || (!d.pub && to != g.fileUnit && g.namesFrom != to+1 && !g.partnerTestOf(to)) {
		return kindInvalid, false
	}
	unit := g.files.units[to]
	key := unit.key + "." + d.nomi
	if !d.lowerable() {
		g.reject(d.whyNot(), key, at)
		return kindInvalid, true
	}
	// The kind belongs to the declaring file's gen, so it is translated before
	// use: a mirror is a different pointer from the owner's def. A translation
	// that fails names its own reason.
	result, ok := g.importKind(d.k)
	if !ok {
		g.reject(g.importWhy, key+": "+d.k.nomi(), at)
		return kindInvalid, true
	}
	return result, true
}
