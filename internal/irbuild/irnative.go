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

// irDeclined is one decline: the builder's reason.
type irDeclined struct {
	why string
}

// take answers the open attempt's first decline and clears it.
func (dc *declineCensus) take() irDeclined {
	d := irDeclined{why: dc.why}
	dc.why, dc.line, dc.col = "", 0, 0
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
		g.dc.take()
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
