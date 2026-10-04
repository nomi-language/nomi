package irbuild

// ONE construction of a stdlib module's lowering context, for the three callers
// that need it.
//
// lowerStdlibModule's prologue — MarkTailCalls, the anchors, the candidates, the
// `once` bindings, the two fixed points, newStdGen — is six lines that must run
// in one order. It lives in one place because a caller that reassembles a
// module's context is measuring its own reassembly, and two copies of a staging
// step do not double the chance of catching a fault in it: they hide it.
//
// Every line is lowerStdlibModule's, in lowerStdlibModule's order, and
// TestStdModuleView_MatchesLowerStdlibModuleOrder pins the one thing that can
// silently go wrong here: which fixed point the gen is bound against.

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// stdModuleView is one stdlib module's lowering context: the inputs a gen needs
// plus the two fixed points that decide which of its bodies may be lowered.
type stdModuleView struct {
	module string
	path   string
	pkg    string
	nodes  []ast.Node
	fa     *analysis.FileAnalysis
	cands  []*stdCandidate
	onces  []*stdOnce
	// settled is phase 1: the bodies that lower with no recursion at all. This
	// is the set newStdGen is bound against, so it is what an ordinary body
	// resolves siblings through.
	settled map[*stdCandidate]bool
	// withCycles is phase 2: settled plus the members of a recursive cycle.
	// Bound per BODY by bindStdSiblings inside the lowering loop, never at
	// construction — which is what confines a cycle to phase 2 and leaves phase
	// 1's answers unchanged. A caller that binds it at construction is asking
	// a MORE GENEROUS question than the builder asks, and must say so.
	withCycles map[*stdCandidate]bool
	// unlowered names each Go function in this module's package whose body the
	// IR did not lower, with the reason. See irnative.go.
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
	v.onces = collectStdOnces(module, pkg, nodes, fa, anchors)
	v.settled = stdSettle(v.cands, v.onces)
	closeStdOnces(v.onces)
	return v
}

// gen builds the gen this module's bodies are lowered through, bound against
// PHASE 1 exactly as lowerStdlibModule binds it.
func (v *stdModuleView) gen(earlier *stdlibIndex) *gen {
	return newStdGen(v.module, v.path, v.pkg, v.nodes, v.fa, v.cands, v.settled, v.onces, earlier)
}

// stdSettle answers which of a module's declarations are attempted: every host
// binding and every Nomi body and `once` whose signature is representable.
// Whether a body is retained is the IR builder's answer, taken when it is
// built (irPrebuildStdBodies); a body the builder declines is recorded as
// unlowered and a program that reaches it is refused (irreach.go).
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
