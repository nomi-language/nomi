package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// An `or` pattern, `#.Circle{r} or #.Square{r}`.
//
// Its alternatives are tested in turn against the one value. An alternative
// that matches copies each name it binds into a join temporary the
// alternatives share and jumps to the pattern's success block; one that fails
// falls to the next alternative's test, and the last one's failure is the
// pattern's. On the success block each name is bound once, to its join
// temporary. So what follows the pattern (an arm's guard and body, the rest
// of a list pattern) is lowered once, whichever alternative matched, and no
// body is duplicated.
//
// Inside an alternative a name is not a declaration: patternBinding writes
// the join temporary with an `ir.Copy`, and only the success block's `ir.Bind`
// declares the name and records its debugger variable. An `or` nested in an
// alternative of another binds its names on its own success block, which is
// inside the outer alternative, so those Binds write the outer join in turn.
//
// The checker requires every alternative to bind the same names at one type,
// so each join temporary is written on every path into the success block.

// irOrJoin is the join temporaries of the `or` pattern whose alternatives are
// being tested: one per name, with its kind.
type irOrJoin struct {
	temps map[string]ir.Temp
	kinds map[string]kind
	// mismatch is set when an alternative binds a name at another kind than
	// an earlier one did.
	mismatch string
}

// orPatternTest tests p's alternatives in order against value, branching to
// arm with p's names bound when one matches and to next when none does.
// bl.b is left at arm.
func (bl *irScalarBuilder) orPatternTest(p *ast.OrPattern, value ir.Temp, k kind, arm, next *ir.Block) bool {
	outer := bl.orJoin
	join := &irOrJoin{temps: map[string]ir.Temp{}, kinds: map[string]kind{}}
	scope := bl.saveScope()
	defer func() {
		bl.orJoin = outer
		bl.restoreScope(scope)
	}()
	for i, alt := range p.Alternatives {
		// Each alternative sees the names in scope before the pattern, as a
		// map key in it must, and never an earlier alternative's.
		bl.restoreScope(scope)
		bl.cloneScope()
		matched := bl.f.NewBlock(bl.g.irNodePos(alt), "or alternative matched")
		failed := next
		if i+1 < len(p.Alternatives) {
			failed = bl.f.NewBlock(bl.g.irNodePos(p.Alternatives[i+1]), "or alternative")
		}
		bl.orJoin = join
		if !bl.patternValueTest(alt, value, k, matched, failed) {
			return false
		}
		bl.orJoin = outer
		bl.b.SetTerm(ir.NewJump(bl.g.irNodePos(alt), arm.ID()))
		if failed != next && !irBlockTargeted(bl.f, failed.ID()) {
			// This alternative matches every value (`n or n`, `_ or .A`),
			// so no later one is tested. Its failure block is unreachable,
			// reads nothing, and ends as a mismatch does.
			at := bl.g.irNodePos(p.Alternatives[i+1])
			failed.Append(ir.NewNoMatch(at))
			failed.SetTerm(ir.NewJump(at, arm.ID()))
			break
		}
		bl.b = failed
	}
	bl.orJoin = outer
	bl.restoreScope(scope)
	bl.b = arm
	if join.mismatch != "" {
		irDeclineNote("an `or` pattern whose alternatives bind " + join.mismatch + " at two kinds")
		return false
	}
	for _, name := range patternBinders(p.Alternatives[0]) {
		t, bound := join.temps[name]
		if !bound {
			continue
		}
		bl.patternBinding(p, name, t, join.kinds[name])
	}
	return true
}

// orJoinCopy writes part, the value an alternative of the `or` pattern being
// tested binds name to, into the name's join temporary.
func (bl *irScalarBuilder) orJoinCopy(at ast.Node, name string, part ir.Temp, k kind) {
	j := bl.orJoin
	t, seen := j.temps[name]
	if !seen {
		t = bl.f.NewTemp()
		bl.side(t, irScalarSide{k: k})
		j.temps[name], j.kinds[name] = t, k
	} else if j.kinds[name] != k && j.mismatch == "" {
		j.mismatch = name
	}
	bl.b.Append(ir.NewCopy(bl.g.irNodePos(at), t, part))
	bl.bound[name], bl.boundK[name] = t, k
}

// irOrParam is an `or` pattern inside a destructuring parameter: the pattern
// and the temporary the prologue held the value it matches in, of kind k.
type irOrParam struct {
	pat *ast.OrPattern
	t   ir.Temp
	k   kind
}

// orParamTests tests each `or` pattern the prologue held a value for
// (g.destructure), binding its names before the body. The checker admitted
// each only when its alternatives together match every value, so the
// mismatch block is never reached; it ends in NoMatch as a binding's does.
func (bl *irScalarBuilder) orParamTests() bool {
	for _, op := range bl.sh.orParams {
		at := bl.g.irNodePos(op.pat)
		bl.side(op.t, irScalarSide{k: op.k})
		cont := bl.f.NewBlock(at, "parameter pattern matched")
		nomatch := bl.f.NewBlock(at, "parameter pattern mismatch")
		nomatch.Append(ir.NewNoMatch(at))
		nomatch.SetTerm(ir.NewJump(at, cont.ID()))
		if !bl.orPatternTest(op.pat, op.t, op.k, cont, nomatch) {
			return false
		}
		bl.b = cont
	}
	return true
}
