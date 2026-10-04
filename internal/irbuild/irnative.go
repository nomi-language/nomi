package irbuild

// DECLINES AND WITHHELD BODIES.
//
// Every body a program runs — a module `fn`, an impl function, a `once`
// initializer, a test body and its setups, a boot, and every stdlib body the
// program reaches — is built into an `ir.Func`. A body the builder declines
// retains nothing: the VM reports it as BLOCKED, with the builder's first
// decline reason, when a program reaches it (vm.Machine.Unretained). This file
// holds the decline bookkeeping and the reasons a retained body is withheld
// from the builder's own accounting (a tail-call cycle, a deferred call before
// a tail call, a synthesized or boot body).

import (
	"strconv"

	"github.com/nomi-language/nomi/internal/ast"
)

// irNotLowered prefixes the construct every such refusal is filed under. The
// builder's own reason follows it, so the blocker tally ranks reasons.
const irNotLowered = "not lowered to IR"

// irNotLoweredConstruct is the construct key for one decline reason.
func irNotLoweredConstruct(reason string) string {
	if reason == "" {
		reason = "no reason recorded"
	}
	return irNotLowered + ": " + reason
}

// irTakeDeclineWhy answers the open attempt's first decline and clears it.
func irTakeDeclineWhy() string {
	return irTakeDecline().why
}

// irDeclined is one decline: the builder's reason and, when the decline was
// taken at an expression, that expression's position.
type irDeclined struct {
	why       string
	line, col int
}

// irTakeDecline answers the open attempt's first decline and clears it.
func irTakeDecline() irDeclined {
	d := irDeclined{why: irDeclineWhy, line: irDeclineLine, col: irDeclineCol}
	irDeclineWhy, irDeclineLine, irDeclineCol = "", 0, 0
	return d
}

// irBecause is a decline whose reason is the caller's, at the body's position.
func irBecause(why string) irDeclined { return irDeclined{why: why} }

// unloweredBody closes the builder's decline for one body it did not lower: the
// open attempt's first decline is consumed so it does not leak into the next
// attempt. The body retains nothing, and the VM reports it as BLOCKED if a
// program reaches it (vm.Machine.Unretained).
func (g *gen) unloweredBody(d irDeclined) {
	if g.stdModule != "" {
		return
	}
	if d.why == "" {
		irTakeDecline()
	}
}

// stdUnloweredFunc records, for a stdlib gen, one function whose body the IR
// did not lower.
func (g *gen) stdUnloweredFunc(id, key, reason string) {
	if g.irPrebuilding {
		return
	}
	if g.stdUnlowered == nil {
		g.stdUnlowered = map[string]string{}
	}
	if reason == "" {
		reason = "no reason recorded"
	}
	g.stdUnlowered[id] = key + "|" + reason
}

// stdUnloweredWrappers records every default-argument wrapper and slot accessor
// a stdlib module emitted: neither has Go, since filling a default is Nomi the
// Go reader spells only inside a retained graph (irStdArityRetain builds the
// VM's).
func (g *gen) stdUnloweredWrappers(cands []*stdCandidate) {
	for _, c := range cands {
		f := c.f
		if c.fd == nil || !f.body {
			continue
		}
		for arity := f.arityMin; arity < len(f.params); arity++ {
			g.stdUnloweredFunc(f.key+" arity "+strconv.Itoa(arity), f.key, "a stdlib default-argument wrapper, built for the VM only")
		}
		for slot, filled := range f.defaultFill {
			if filled {
				g.stdUnloweredFunc(f.key+" default "+strconv.Itoa(slot), f.key, "a stdlib default-argument accessor, built for the VM only")
			}
		}
	}
}

// refuseTestBody records a test case whose body has no Go. A user case refuses
// the program; a stdlib prompt case refuses only itself.
func (g *gen) refuseTestBody(name string, d irDeclined, at ast.Node) {
	if g.stdModule != "" {
		if d.why == "" {
			d = irTakeDecline()
		}
		g.reject(irNotLoweredConstruct(d.why), "test "+strconv.Quote(name), at)
		return
	}
	g.unloweredBody(d)
}
