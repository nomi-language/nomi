package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// An `or` pattern, `.Circle{r:} or .Square{r:}`.
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

// orPatternTest tests p's alternatives in order against value, going on in
// the block they join at with p's names bound when one matches, and
// branching to next when none does.
func (bl *irScalarBuilder) orPatternTest(p *ast.OrPattern, value ir.Temp, k kind, next *ir.Block) bool {
	outer := bl.orJoin
	join := &irOrJoin{temps: map[string]ir.Temp{}, kinds: map[string]kind{}}
	defer func() { bl.orJoin = outer }()
	arm := bl.f.NewBlock(bl.g.irNodePos(p), "or matched")
	for i, alt := range p.Alternatives {
		failed := next
		if i+1 < len(p.Alternatives) {
			failed = bl.f.NewBlock(bl.g.irNodePos(p.Alternatives[i+1]), "or alternative")
		}
		bl.orJoin = join
		if !bl.patternTest(alt, value, k, failed) {
			return false
		}
		bl.orJoin = outer
		bl.b.SetTerm(ir.NewJump(bl.g.irNodePos(alt), arm.ID()))
		if failed != next && bl.g.patternAlwaysMatches(alt) {
			// This alternative matches every value (`n or n`, `_ or .A`),
			// so no later one is tested. Its failure block is one no value
			// reaches, and ends as a mismatch does.
			at := bl.g.irNodePos(p.Alternatives[i+1])
			failed.Append(ir.NewNoMatch(at))
			failed.SetTerm(ir.NewJump(at, arm.ID()))
			break
		}
		bl.b = failed
	}
	bl.orJoin = outer
	bl.b = arm
	if join.mismatch != "" {
		bl.g.dc.note("an `or` pattern whose alternatives bind " + join.mismatch + " at two kinds")
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
}
