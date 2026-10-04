package irbuild

// THE DECLARING MODULE IS ITS OWN PROTOCOL. std/iter.nomi is the one file
// whose `Iter` is std's by construction, and loadIter exempts it from both
// provenance rules (iter.go's THE DECLARING MODULE IS ITS OWN PROTOCOL).
//
// loadIter has three rules:
//
//	1. The name must be reached through an IMPORT (`sym.Resolved != nil`).
//	   std/iter.nomi declares `Iter` locally.
//	2. The module must declare no `impl` block headed `Iter`. std/iter.nomi's
//	   whole API is `impl Iter<T> { ... }`.
//	3. The declaration's SHAPE must be the push protocol rt/seq.go implements.
//
// Rules 1 and 2 each refuse std/iter independently, so the test below reads
// all three and requires the anchor to exist anyway:
//
//   - rule 2 SEES the module's own `impl Iter<T>` blocks, which requires the
//     std gen to carry `g.nodes`; a stdlib gen without it makes every reader
//     of `g.nodes` answer about an empty module;
//   - rule 1's `sym.Resolved` is nil, so the exemption is doing the work rather
//     than an import;
//   - rule 3 passes, so a std/rt drift is separated from a provenance question.
//
// `Iter without the std protocol` refusals are reachable only from inside the
// stdlib, because only a stdlib prompt calls the stdlib from the module that
// declares it, so no corpus, example or tour program would show a regression.
//
// The negative half, that no OTHER file can obtain the anchor, is
// TestIterAnchor_Rule2HasNoSubjectRule1DoesNotAlreadyHold below plus
// iteranchorreach_test.go's negative control. A file declaring its own
// `interface Iter<T>` is a FRONT-END error, as is `impl Iter<T>` over std's
// imported `Iter`, so the only file either provenance rule excludes is
// std/iter.nomi.

import (
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// stdIterGen builds the gen std/iter.nomi's own bodies are lowered through.
//
// The context comes from stdModuleContext — the ONE copy of lowerStdlibModule's
// prologue — for the reason stdmodulegen.go states: reassembling a module's
// context in a test measures the reassembly, and a second copy of a staging
// step hides a fault in it rather than doubling the chance of catching it.
func stdIterGen(t *testing.T) (*gen, []ast.Node, *analysis.FileAnalysis) {
	t.Helper()
	lib := std.Load()
	nodes := lib.Nodes["iter"]
	if len(nodes) == 0 {
		t.Fatalf("std.Load() carries no nodes for std/iter")
	}
	fa := lib.Files["iter"]
	if fa == nil {
		t.Fatalf("std.Load() carries no FileAnalysis for std/iter")
	}
	names := make([]string, 0, len(lib.Nodes))
	for name := range lib.Nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	pkg := stdPackage(sort.SearchStrings(names, "iter"))
	path := strings.TrimPrefix(lib.FileURI("iter"), "file://")

	full := stdlibLowering()
	v := stdModuleContext("iter", path, pkg, nodes, fa, full)
	return v.gen(full), nodes, fa
}

// TestIterAnchor_DeclaringModuleIsItsOwnProtocol reads loadIter's three rules
// against std/iter.nomi and requires the anchor to exist. See the header.
func TestIterAnchor_DeclaringModuleIsItsOwnProtocol(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	g, nodes, fa := stdIterGen(t)

	// ANTI-VACUITY FIRST, so every reading below is known to have run against
	// std/iter and not against an empty tree.
	implBlocks := 0
	for _, n := range nodes {
		if ib, ok := n.(*ast.ImplBlock); ok && ib.Interface == nil &&
			analysis.TypeExprBaseName(ib.Receiver) == "Iter" {
			implBlocks++
		}
	}
	if implBlocks == 0 {
		t.Fatalf("no inherent `impl Iter` block among std/iter.nomi's %d top-level nodes — this test "+
			"cannot have been reading std/iter", len(nodes))
	}

	// RULE 2 SEES THE BLOCKS. `declaresIterImpl` walks `g.nodes`, so this holds
	// only while newStdGen populates that field (TestStdModuleView_PopulatesNodes).
	// Rule 2 fires first for std/iter, ahead of the ModuleScope lookup.
	if !g.declaresIterImpl() {
		t.Fatalf("declaresIterImpl answers FALSE for a stdlib gen carrying %d inherent `impl Iter` "+
			"blocks, so newStdGen does not populate g.nodes and every predicate in this package that "+
			"reads it is vacuous for every stdlib module", implBlocks)
	}

	// RULE 1. `Iter` is declared locally, so `sym.Resolved` is nil.
	sym := fa.ModuleScope.Lookup("Iter")
	if sym == nil {
		t.Fatalf("std/iter.nomi's module scope does not resolve `Iter`")
	}
	if sym.Resolved != nil {
		t.Fatalf("std/iter.nomi's `Iter` resolves through an import (Resolved != nil), so rule 1 does " +
			"not refuse it and the exemption is not what admits the anchor")
	}

	// RULE 3, THE DISCRIMINATOR: provenance versus protocol.
	decl, isDecl := sym.Node.(*ast.InterfaceDef)
	if !isDecl {
		t.Fatalf("std/iter.nomi's `Iter` symbol carries a %T rather than an *ast.InterfaceDef", sym.Node)
	}
	if !iterProtocolMatches(decl) {
		t.Fatalf("std/iter.nomi's own `Iter` does not match the push protocol rt/seq.go implements. " +
			"That is a std/rt DRIFT rather than the provenance question this file reads, and it " +
			"has to be fixed there first")
	}

	// THE OUTCOME. The anchor must EXIST, and the readings above say which rule
	// admits it: the exemption, because rule 1's `sym.Resolved` is nil and rule
	// 2 sees the impl blocks.
	g.loadIter()
	if g.iter == nil {
		t.Fatalf("std/iter.nomi produces NO `Iter` anchor. Both provenance rules refuse it "+
			"independently (rule 2 sees all %d of the module's own `impl Iter` blocks and rule 1 "+
			"finds `Iter` declared locally) and the exemption for the DECLARING MODULE does not "+
			"apply. Every `Iter.` mention in std/iter refuses `Iter without the std protocol`. "+
			"Rule 3, the SHAPE, passed above, so this is a provenance answer "+
			"and not a std/rt drift. See iter.go's declaresStdIter", implBlocks)
	}
	t.Logf("std/iter.nomi produces its own anchor: rule 3 (SHAPE) passes on the module's OWN "+
		"declaration, rule 1's sym.Resolved is nil and rule 2 sees all %d `impl Iter` blocks, so "+
		"the declaring-module exemption is what admits it.", implBlocks)
}

// TestIterAnchor_Rule2HasNoSubjectRule1DoesNotAlreadyHold measures whether rule
// 2 excludes anything rule 1 does not.
//
// The two shapes a file other than std/iter can present:
//
//   - Its OWN `interface Iter<T>`. Rule 1 declines it on its own — the
//     declaration is local, so `sym.Resolved` is nil — whether the file writes
//     an `impl Iter` block or not. So rule 2 adds nothing there.
//   - `impl Iter<T>` over STD's IMPORTED `Iter`, which is the case rule 2's
//     comment is actually about: "a program that adds its own owner function to
//     std's interface would have `Iter.map` resolve to ITS declaration".
//
// The second is the one that decides whether rule 2 is load-bearing for user
// code, so the front end's answer for it is logged here. It is reported rather
// than asserted, because the orphan rule is not this file's to legislate.
func TestIterAnchor_Rule2HasNoSubjectRule1DoesNotAlreadyHold(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	// (1) A file declaring its OWN Iter and no impl block. Rule 1 alone must
	// decline it, and rule 2 must have nothing to say.
	const ownIter = `interface Iter<T> {
  fn each_while(collection: self, yield: (T) -> Bool): Bool
}

fn main() {
  Unit
}
`
	if prog, err := AnalyzeSource("own_iter", ownIter); err != nil {
		t.Logf("(1) a file declaring its own `interface Iter<T>` is a FRONT-END error, so neither rule "+
			"has a subject here:\n%s", err.Error())
	} else {
		g := newUserGenFor(prog)
		g.loadIter()
		if g.iter != nil {
			t.Fatalf("(1) a file's OWN `interface Iter<T>` produced std's anchor — rule 1 is not " +
				"holding, and a non-std `Iter` would lower to rt.Seq by name")
		}
		t.Logf("(1) a file declaring its own `interface Iter<T>` produces no anchor with "+
			"declaresIterImpl = %v, so RULE 1 ALONE is what declines it", g.declaresIterImpl())
	}

	// (2) `impl Iter<T>` over std's imported Iter — rule 2's stated subject.
	const ownerBlock = `
impl Iter<T> {
  pub fn mine(_source: Iter<T>): Int {
    0
  }
}

fn main() {
  Unit
}
`
	prog, err := AnalyzeSource("iter_owner_block", ownerBlock)
	if err != nil {
		t.Logf("(2) `impl Iter<T>` over std's imported `Iter` is a FRONT-END error, so RULE 2 HAS NO "+
			"REACHABLE SUBJECT that rule 1 does not already hold, and the only file it excludes is "+
			"std/iter.nomi itself:\n%s", err.Error())
		return
	}
	g := newUserGenFor(prog)
	t.Logf("(2) `impl Iter<T>` over std's imported `Iter` PASSES the front end, so rule 2 is "+
		"load-bearing for user code and must keep declining it: declaresIterImpl = %v",
		g.declaresIterImpl())
	if !g.declaresIterImpl() {
		t.Fatalf("(2) the front end accepted an `impl Iter<T>` block and declaresIterImpl does not see " +
			"it, so rule 2 is not doing what its comment says")
	}
	g.loadIter()
	if g.iter != nil {
		t.Fatalf("(2) a user `impl Iter<T>` block produced std's anchor; rule 2 is not holding")
	}
}

// newUserGenFor builds a gen for the entry module the way the ordinary user
// path does, so loadIter is asked in a user file's context rather than a std
// module's.
func newUserGenFor(p *Program) *gen {
	m := p.Entry()
	g := &gen{nomiPath: m.Path, pkg: unitPackage(0), fa: m.FA, nodes: m.Nodes, funcs: map[string]*fnSig{}, types: map[string]*typeDef{}, ifaces: map[string]*ifaceDef{}, implsByIface: map[string]map[kind]*implDef{}, tids: map[kind]bool{}, anonTids: map[kind]bool{}}
	g.pushScope()
	return g
}
