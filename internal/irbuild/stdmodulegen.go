package irbuild

// ONE construction of a stdlib module's lowering context, for the three callers
// that need it.
//
// lowerStdlibModule's prologue — MarkTailCalls, the anchors, the candidates, the
// `once` bindings, the settled set, newStdGen — is six lines that must run
// in one order. It lives in one place because a caller that reassembles a
// module's context is measuring its own reassembly, and two copies of a staging
// step do not double the chance of catching a fault in it: they hide it.
//
// Every line is lowerStdlibModule's, in lowerStdlibModule's order.

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// stdModuleView is one stdlib module's lowering context: the inputs a gen needs
// plus the settled set that decides which of its bodies are attempted.
type stdModuleView struct {
	module string
	path   string
	pkg    string
	nodes  []ast.Node
	fa     *analysis.FileAnalysis
	cands  []*stdCandidate
	onces  []*stdOnce
	// settled is the declarations that are attempted (stdSettle). It is the
	// set newStdGen is bound against, so it is what a body resolves siblings
	// through.
	settled map[*stdCandidate]bool
	// embedded is set by buildStdlibIndex: this is the binary's own stdlib
	// module, lowered the same way in every process. See lintEmbeddedStdlib.
	embedded bool
	// unlowered names each function and `once` of this
	// module whose body the builder did not retain, with the reason. See
	// irnative.go.
	unlowered map[string]string
}

// stdModuleContext runs lowerStdlibModule's prologue over one module.
//
// `earlier` is the stdlib index the module resolves cross-module references
// against. In buildStdlibIndex that is the modules already lowered; a caller
// may hand the complete index instead, which resolves more references.
func stdModuleContext(module, path, pkg string, nodes []ast.Node, fa *analysis.FileAnalysis, earlier *stdlibIndex) *stdModuleView {
	// A checked AST is supposed to carry tail-position marks and std's does not:
	// std.Load() runs the sweeps and CheckTypes but never MarkTailCalls, so
	// `Call.IsTailCall` is FALSE on every stdlib node this builder reads and any
	// tail-position reasoning about a stdlib body is VACUOUS without this line.
	// See lowerStdlibModule, which is where the measurement behind it lives.
	analysis.MarkTailCalls(nodes)

	anchors := stdAnchorsOf(fa)
	v := &stdModuleView{module: module, path: path, pkg: pkg, nodes: nodes, fa: fa}
	v.cands = collectStdCandidates(module, nodes, anchors)
	v.onces = collectStdOnces(module, nodes, fa, anchors)
	v.settled = stdSettle(v.cands, v.onces)
	closeStdOnces(v.onces)
	return v
}

// gen builds the gen this module's bodies are lowered through, bound against
// the settled set exactly as lowerStdlibModule binds it.
func (v *stdModuleView) gen(earlier *stdlibIndex) *gen {
	return newStdGen(v.module, v.path, v.pkg, v.nodes, v.fa, v.cands, v.settled, v.onces, earlier)
}

// stdSettle answers which of a module's declarations are attempted: every host
// binding and every Nomi body and `once` whose signature is representable.
// Whether a body is retained is the IR builder's answer, taken when it is
// built (irPrebuildStdBodies); a body the builder declines is recorded as
// unlowered and a program that reaches it is refused.
func stdSettle(cands []*stdCandidate, onces []*stdOnce) map[*stdCandidate]bool {
	settled := map[*stdCandidate]bool{}
	for _, c := range cands {
		if c.f.rtCall != "" || (c.fd != nil && c.f.why == "") {
			settled[c] = true
		}
	}
	for _, o := range onces {
		if o.why == "" {
			o.settled = true
		}
	}
	return settled
}
