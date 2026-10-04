package irbuild

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"
)

// Test declarations and assertions.
//
// # What a test file compiles to
//
// A .nomi file that declares tests is not a program. Each `test "name" { ... }`
// body is built as its own test body, and the cases run through rt's reporter
// — the one `nomi test` uses — in declaration order under the NOMI_ENV
// default. `fn main` is lowered if the file has one and is never called.
//
// # Where the failure text comes from
//
// Nowhere in this file. The `line N: <reason>` header, the `values:` rows, the
// `defined as:` block, the pipeline stages and the ok/FAIL/summary lines are all
// rt's (rt/assertion.go, rt/testreport.go). What the builder contributes is the
// DATA: the absolute source line, the asserted expression's source text, and
// the operand values. Source text surviving into a built program is the one
// place that is legitimate — the output is a byte-for-byte contract, and it
// embeds how the assertion was written.
//
// # The trace boundary is resolved at build time
//
// A callee's comparisons must not leak into the caller's failure report.
// Recording is emitted only for operators lexically inside an assertion's
// subject, and a callee's body is a separate declaration that is never
// lexically inside one — so a callee emits no recording at all and the
// boundary costs nothing. No frame field, no per-call frame, no flag consulted
// at run time. See gen.traceVar.
//
// The one place lexical containment is the WRONG test is a lambda body, which is
// lexically inside the subject but is a separate ACTIVATION and is therefore
// excluded. That seam is the only exception — every other construct that
// is lexically inside a subject is also in the assertion's own activation.
//
// The consequence is that the boundary still costs the run nothing while being
// right about a lambda: suspension is a build-time fact about where the
// builder is, not a flag the program reads. testdata/tests_lambda_boundary.nomi
// is the pin, and TestTests_NoFrameIsAllocatedPerCall is the measurement that
// the cost stayed at zero.

// loweredTest is one `test "name" { ... }` as the builder's test table records it.
type loweredTest struct {
	name string
	// virtual marks a case under `clock Clock.Virtual`, whose table entry
	// carries a Bubble. See testclock.go.
	virtual bool
}

// --- collecting the cases --------------------------------------------------

// testSetup is a `tests` group's `boot` line and `setup`, as a case in the
// group sees them.
//
// boot is the group's `boot` call; body is the setup, whose value the case's
// pattern binds. A frame exists where `Boot != nil || Setup != nil`, which is
// the exact condition frontend's collectTestCasesInBlock pushes one under.
//
// boot rides along even when a group is refused, because a refusal must not
// hide what is inside it and the boot line's argument is inside it.
type testSetup struct {
	// group is the `tests` declaration the frame came from, and the identity
	// probeTestBody dedupes on: a frame is shared by every case in the group,
	// so probing it per case would report one blocker once per sibling.
	group ast.Node
	boot  ast.Node
	body  ast.Node
}

// testCaseDecl is one runnable case, as the reporter will name it.
//
// names is the path from the group down to the case; the front end joins it
// with " / " (TestCase.FullName) and so does this. setups holds the group's
// frame, if it has one.
type testCaseDecl struct {
	names  []string
	setups []testSetup
	ctxPat ast.Node
	body   *ast.Block
	line   int
	// owner is the FILE-SCOPE declaration the case is collected at, which is
	// where it is built from so that the table follows declaration order.
	owner ast.Node
	// attached distinguishes a `//!` prompt case from a `test` declaration's.
	// A node is never both — AttachedTestsOf has no TestDecl arm — so the two
	// build points cannot interleave for one node.
	attached bool
	// self is the self type of a prompt inside a type's items (the owning
	// type's name), or "".
	self string
	// clock is the group's `clock Clock.Virtual` / `clock Clock.System`, or ""
	// when the group declares none. It mirrors frontend.TestCase's clock
	// exactly, including that "" and "System" mean the same thing to every
	// consumer.
	//
	// Read from the clause by ast.TestClockVariant — the ONE implementation of
	// that rule, shared with the front end, because a second copy could plan
	// and run one group under different clocks. That is a wrong ANSWER, so it
	// is not a place for two implementations kept in step by a test.
	clock string
	// refusedBy is the construct name this case is refused under, or "" when it
	// lowers. A refused case is not in the table; its body is still walked for
	// blockers, so the refusal does not hide what is inside it.
	refusedBy string
	// refusalDetail is the refusal's operand.
	refusalDetail string
	// refusalAt is the node the refusal is blamed on: the GROUP for a `clock`
	// or `boot` declaration, the owning declaration for a prompt test.
	refusalAt ast.Node
	// reportRefusal marks the one case that reports it, so a refused group of
	// six cases is one blocker rather than six.
	reportRefusal bool
	// probeSetups are the frames THIS case is responsible for walking when it
	// is refused, deduped across the module: a chain is shared by every case
	// beneath it, so the first case under each group carries it and its
	// siblings carry none. Only used on the refused path.
	probeSetups []testSetup
	// refusalProbe is the refused CLAUSE's own operand — a group's `clock`
	// expression — walked by the reporting case only. `boot` needs no entry
	// here because it rides on a setup frame.
	//
	// It exists because `clock Clock.Virtual` is an ordinary enum reference
	// that the builder cannot lower; without the probe its refusal would go
	// unreported whenever the clock declaration is the only occurrence.
	refusalProbe ast.Node
}

// collectTestCases is the builder's half of frontend.CollectTestCases, and it
// is deliberately a mirror rather than a shared function: the two produce
// different things from one traversal rule, and the rule is the contract.
//
// What it mirrors, member for member: the name path, the setup chain,
// declaration order, and the position an attached test occupies — before its
// own declaration and after everything above it, because the front end calls
// collectAttachedTestCases first and then descends.
//
// The recursion into a type's or an impl block's ITEMS is mirrored too, with
// the owner's name as the prefix (attachedTestCasesIn). A stdlib module's case
// gets the owner as its self type (stdtests.go); a user file's case gets none
// here, so a bare same-owner call in it declines.
//
// PURE: it records refusals on the cases rather than reporting them, so the
// list can be collected once — before any body is built, which is when the
// duplicate-name question has to be answered — while every refusal is still
// reported from inside the dispatch arm that owns the construct.
func collectTestCases(nodes []ast.Node, declaresTests bool) []testCaseDecl {
	var out []testCaseDecl
	for _, n := range nodes {
		out = append(out, attachedTestCases(n, declaresTests)...)
		if t, ok := n.(*ast.TestDecl); ok {
			ref := testRefusal{}
			if !declaresTests {
				ref = importedModuleRefusal(t.Name, t)
			}
			out = append(out, collectTestDecl(t, n, nil, nil, "", ref)...)
		}
	}
	// One refusal per refused CONSTRUCT rather than per case beneath it, and
	// the first case carries it so the report is in declaration order. The
	// inherited setup chain is deduped the same way and for the same reason.
	reported := map[ast.Node]bool{}
	probed := map[ast.Node]bool{}
	for i := range out {
		if out[i].refusedBy == "" {
			continue
		}
		if !reported[out[i].refusalAt] {
			reported[out[i].refusalAt] = true
			out[i].reportRefusal = true
		}
		for _, s := range out[i].setups {
			if probed[s.group] {
				continue
			}
			probed[s.group] = true
			out[i].probeSetups = append(out[i].probeSetups, s)
		}
	}
	return out
}

// testRefusal is the refusal a case inherits from the construct it sits in.
type testRefusal struct {
	construct string
	detail    string
	at        ast.Node
	// probe is the refused clause's own operand, walked once by the reporting
	// case so the refusal does not hide what is inside the clause itself.
	probe ast.Node
}

// collectTestDecl collects one `test` or `tests` declaration and its
// descendants. ref is inherited: a group this builder turns down refuses every
// case beneath it, and the reason and position are the group's. clock is the
// inherited `clock` variant, "" at the top level.
func collectTestDecl(t *ast.TestDecl, owner ast.Node, prefix []string, setups []testSetup, clock string, ref testRefusal) []testCaseDecl {
	names := append(append(make([]string, 0, len(prefix)+1), prefix...), t.Name)
	if !t.Group {
		return []testCaseDecl{{
			names:         names,
			setups:        setups,
			ctxPat:        t.ContextPattern,
			body:          t.Body,
			line:          t.Line,
			owner:         owner,
			clock:         clock,
			refusedBy:     ref.construct,
			refusalDetail: ref.detail,
			refusalAt:     ref.at,
			refusalProbe:  ref.probe,
		}}
	}
	if t.Clock != nil {
		// `clock Clock.Virtual` LOWERS: the case's rt.Test carries
		// vclock.RunCase as its Bubble, which runs the body inside a
		// testing/synctest bubble under the four-layer discipline rt/vclock
		// owns.
		// `clock Clock.System` lowers by emitting nothing, because the real
		// clock is already what a case with no clause gets.
		//
		// THE CLAUSE EXPRESSION IS NOT LOWERED, AND THAT IS COMPLETE RATHER
		// THAN PARTIAL. Nothing evaluates it: the clock has to be known
		// before a case runs while `boot` and `setup` run inside it, so
		// the front end reads the variant syntactically too. There is no `probe`
		// entry for it any more for the same reason — an expression nobody
		// evaluates has no operands to refuse.
		//
		// An unreadable spelling REFUSES rather than guessing a clock or
		// emitting a failure. The front end marks that case InvalidClock and
		// the runner reports it as failed with its own observable text, and
		// mirroring the string here would be a second copy of observable text
		// (trap.go's rule); a refusal is never a wrong answer. Defensive in
		// both places — checker.checkTestClock type-checks
		// the clause against `testing.Clock` first — so it should be
		// unreachable from legal source. TestClock_UnreadableSpellingRefuses
		// is the witness, and it says what to do when its population empties.
		if name, ok := ast.TestClockVariant(t.Clock); ok {
			clock = name
		} else if ref.construct == "" {
			ref = testRefusal{
				construct: "test clock",
				detail:    "a `clock` clause that does not name a Clock variant",
				at:        t,
				probe:     t.Clock,
			}
		}
	}
	// A `boot` constructs the active App value its descendants see. This walk
	// does not stage it: a case under a boot or a setup chain is refused
	// (testCase -> refuseTestBody), and the VM starts the boot
	// (vm.Machine.BootTest).
	local := setups
	if t.Boot != nil || t.Setup != nil {
		// The front end's own condition for pushing a frame, `boot` included: a
		// boot-only group has a frame with no setup body, and dropping it
		// would make the boot EXPRESSION unreachable to the probe.
		//
		// Appended to a fresh slice, because the inherited chain is shared
		// with every sibling subtree and appending in place would let one
		// child's frame reach another's.
		local = append(append(make([]testSetup, 0, len(setups)+1), setups...),
			testSetup{group: t, boot: t.Boot, body: t.Setup})
	}
	if t.Body == nil {
		return nil
	}
	var out []testCaseDecl
	for _, stmt := range t.Body.Stmts {
		child, ok := stmt.(*ast.TestDecl)
		if !ok {
			// The parser admits only boot, setup, test and tests in a group
			// body ("tests body may contain only boot, setup, test, and tests
			// declarations"), so this is unreachable from legal source. Carried
			// as a refused case rather than dropped, so a parser change
			// surfaces in the tally instead of losing a declaration silently.
			out = append(out, testCaseDecl{
				names:     append(append([]string(nil), names...), constructName(stmt)),
				line:      stmt.LineNum(),
				owner:     owner,
				refusedBy: constructName(stmt),
				refusalAt: stmt,
			})
			continue
		}
		out = append(out, collectTestDecl(child, owner, names, local, clock, ref)...)
	}
	return out
}

// attachedTestCases collects the `//!` prompt tests a file-scope declaration
// carries, its own and its items', in frontend.CollectTestCases' order and
// under its names (attachedTestCasesIn). Every case's owner is the file-scope
// declaration, which is where it is built from.
//
// A case inside a type's items has that type as its self type, through which
// a bare same-owner call resolves. The builder installs it for a stdlib module
// (stdtests.go) and not for a user file, where such a call declines rather
// than resolving differently.
func attachedTestCases(n ast.Node, declaresTests bool) []testCaseDecl {
	cases := attachedTestCasesIn([]ast.Node{n}, nil, "")
	if len(cases) == 0 {
		return nil
	}
	ref := testRefusal{}
	if !declaresTests {
		ref = importedModuleRefusal(attachedTestOwner(n), n)
	}
	for i := range cases {
		cases[i].owner = n
		cases[i].refusedBy = ref.construct
		cases[i].refusalDetail = ref.detail
		cases[i].refusalAt = ref.at
	}
	return cases
}

// attachedTestCasesIn collects the prompt cases of nodes and of their items, in
// the order and under the names frontend.CollectTestCases gives them: a
// declaration's own prompts first, then its items with the owner's name as a
// prefix. self is the self type of nodes' prompts, the front end's
// attachedTestSelfTypeName answer. A bodyless prompt is skipped, as the front
// end's collector skips it.
func attachedTestCasesIn(nodes []ast.Node, prefix []string, self string) []testCaseDecl {
	var out []testCaseDecl
	for _, n := range nodes {
		owner := attachedTestOwner(n)
		for _, at := range ast.AttachedTestsOf(n) {
			if at.Body == nil {
				continue
			}
			out = append(out, testCaseDecl{
				names:    appendTestName(prefix, attachedTestCaseName(owner, at)),
				body:     at.Body,
				line:     at.Line,
				owner:    n,
				attached: true,
				self:     attachedTestSelf(self),
			})
		}
		switch v := n.(type) {
		case *ast.StructDef:
			out = append(out, attachedTestCasesIn(v.Items, appendTestName(prefix, v.Name), v.Name)...)
		case *ast.EnumDef:
			out = append(out, attachedTestCasesIn(v.Items, appendTestName(prefix, v.Name), v.Name)...)
		case *ast.TypeDef:
			out = append(out, attachedTestCasesIn(v.Items, appendTestName(prefix, v.Name), v.Name)...)
		case *ast.ExternType:
			out = append(out, attachedTestCasesIn(v.Items, appendTestName(prefix, v.Name), v.Name)...)
		case *ast.ImplBlock:
			inner := appendTestName(prefix, attachedTestOwner(v))
			recv := analysis.TypeExprBaseName(v.Receiver)
			if v.InferInterfaceMethods && v.Interface != nil {
				// The front end collects only the items claiming the block's
				// interface (testImplItemClaimsInterface).
				iface := analysis.TypeExprBaseName(v.Interface)
				for _, item := range v.Items {
					if implItemClaimsIface(item, iface) {
						out = append(out, attachedTestCasesIn([]ast.Node{item}, inner, recv)...)
					}
				}
				continue
			}
			out = append(out, attachedTestCasesIn(v.Items, inner, recv)...)
		}
	}
	return out
}

// attachedTestSelf is frontend's attachedTestSelfTypeName: only a type name is a
// self type.
func attachedTestSelf(owner string) string {
	if owner == "" {
		return ""
	}
	if first, _ := utf8.DecodeRuneInString(owner); !unicode.IsUpper(first) {
		return ""
	}
	return owner
}

// appendTestName is frontend's appendName: a fresh slice every time, so two
// sibling declarations cannot share one backing array.
func appendTestName(prefix []string, name string) []string {
	next := make([]string, 0, len(prefix)+1)
	next = append(next, prefix...)
	return append(next, name)
}

// implItemClaimsIface is frontend's testImplItemClaimsInterface.
func implItemClaimsIface(item ast.Node, iface string) bool {
	switch it := item.(type) {
	case *ast.FuncDef:
		return analysis.TypeExprBaseName(it.ImplIface) == iface
	case *ast.ExternFunc:
		return analysis.TypeExprBaseName(it.ImplIface) == iface
	default:
		return false
	}
}

// importedModuleRefusal is the refusal a case in a module that is not the entry
// carries. `nomi test <file>` collects the named file's cases and no others, so
// lowering these would build a table nothing runs — and refusing them keeps the
// built case list identical to the one `nomi test` plans.
func importedModuleRefusal(name string, at ast.Node) testRefusal {
	return testRefusal{construct: "test declaration in an imported module", detail: name, at: at}
}

// attachedTestCaseName is the front end's label for a prompt test, restated:
// the owner, the prompt marker, the prompt KIND, and the source lines the
// prompt occupies. It is part of the report's byte-for-byte contract, so the
// two spellings have to agree exactly — see attachedTestLineLabel and
// collectAttachedTestCases in internal/frontend/tests.go.
func attachedTestCaseName(owner string, at ast.AttachedTest) string {
	kind := at.Kind
	if kind == "" {
		kind = "test"
	}
	span := "line " + strconv.Itoa(at.Line)
	if at.EndLine > at.Line {
		span = "lines " + strconv.Itoa(at.Line) + "-" + strconv.Itoa(at.EndLine)
	}
	return owner + " //! " + kind + " " + span
}

// attachedTestOwner is frontend's attachedTestOwnerName, restated — the OWNER
// SEGMENT of a prompt case's reported name, which is part of the report's
// byte-for-byte contract.
//
// A `constructName` default would be WRONG for every other owner:
// `constructName` produces the builder's REFUSAL label, so a prompt on a
// `host fn` would be named "host fn declaration //! test line 50" where
// `nomi test` reports "to_string //! test line 50". Such a wrong name is
// invisible while the case is also refused, because a refused case's name is
// never added to the table; a pass COUNT does not see it either.
//
// Every arm below is the front end's, including that an ast.ImplBlock is the literal
// "impl" in all three of its shapes and that an unrecognised declaration is
// "declaration" rather than its construct name.
func attachedTestOwner(n ast.Node) string {
	switch v := n.(type) {
	case *ast.FuncDef:
		return v.Name
	case *ast.ExternFunc:
		return v.Name
	case *ast.StructDef:
		return v.Name
	case *ast.EnumDef:
		return v.Name
	case *ast.TypeDef:
		return v.Name
	case *ast.ExternType:
		return v.Name
	case *ast.TypeAlias:
		return v.Name
	case *ast.InterfaceDef:
		return v.Name
	case *ast.ImplBlock, *ast.ImplConformance:
		return "impl"
	case *ast.OnceBinding:
		return v.Name
	default:
		return "declaration"
	}
}

// --- building the cases ----------------------------------------------------

// testCasesByOwner indexes a collected case list by the declaration it is
// built from.
//
// Built as each declaration is built rather than from the list, so the table's order is the
// declaration order the report uses — and so every refusal is reported from
// inside the dispatch arm that owns the construct rather than from a pass
// beside it. Collection is separate because the duplicate-name question has to
// be answered before any body is built, and answering it needs every case.
func testCasesByOwner(cases []testCaseDecl) map[ast.Node][]testCaseDecl {
	if len(cases) == 0 {
		return nil
	}
	byOwner := make(map[ast.Node][]testCaseDecl, len(cases))
	for _, c := range cases {
		byOwner[c.owner] = append(byOwner[c.owner], c)
	}
	return byOwner
}

// attachedTestDecls emits the `//!` prompt cases a declaration carries.
func (g *gen) attachedTestDecls(cases []testCaseDecl) {
	for _, c := range cases {
		if c.attached {
			g.testCase(c)
		}
	}
}

// testDecl emits the cases one file-scope `test` or `tests` declaration
// contributes, in declaration order.
func (g *gen) testDecl(cases []testCaseDecl) {
	for _, c := range cases {
		if !c.attached {
			g.testCase(c)
		}
	}
}

// testCase emits one case: the inherited setup chain, the case's own context
// pattern, then its body.
func (g *gen) testCase(c testCaseDecl) {
	g.at(c.line)
	if c.refusedBy != "" {
		g.rejectTestBody(c)
		return
	}

	name := strings.Join(c.names, " / ")
	virtual := c.clock == "Virtual"
	g.tests = append(g.tests, loweredTest{name: name, virtual: virtual})

	prevInTest, prevResult := g.inTest, g.result
	g.inTest, g.result = true, kindInvalid
	g.pushScope()
	// THE RETAINED GRAPH IS BUILT BEFORE THE READ-BACK.
	// `bl.lower` asks `g.lookup`, so the build must happen before the reader
	// binds any of this body's names: a name already bound would resolve to
	// an `ir.Ref` naming a Go local no machine can read. A nil plan means the
	// builder declined the body and the case is refused below. See
	// irtestbody.go.
	plan := g.irTestBody(c, name)
	testDecline := irTakeDecline()
	// rt.TestFailure, not *rt.AssertionFailure: a test body has two
	// value-shaped exits, because a `try` that propagates inside one ends the
	// case as an rt.EarlyReturnFailure (try.go). A failing assertion still
	// returns its own concrete pointer unchanged — the interface widens what
	// MAY be returned without changing any existing return site.
	//
	// The body is read back from its graph. A case whose body the builder
	// declines, or that carries a setup chain or a group boot (which the
	// read-back does not stage), is refused. Inside a
	// stdlib module the case is refused on its own, so its prompt is dropped
	// rather than the module (stdtests.go).
	g.irBodyObserve(irBodyTestBody, true)
	switch {
	case len(c.setups) > 0:
		g.refuseTestBody(name, irBecause("a test's setup chain, built for the VM only"), c.body)
	case plan == nil:
		g.refuseTestBody(name, testDecline, c.body)
	}
	g.popScope()
	g.inTest, g.result = prevInTest, prevResult
}

// rejectTestBody refuses a case and collects the blockers inside it — as a TEST
// body, for the reason probeTestBody states.
//
// The refusal itself is reported by ONE case per refused construct
// (reportRefusal), at that construct's own position: a `clock` group of six
// cases is one `test clock` blamed on the group, not six blamed on its bodies.
// Every case is still probed, because the blocker set must not be truncated by
// the outermost refusal.
//
// The key is a LITERAL at each g.reject, and the switch is what makes it one
// rather than passing c.refusedBy straight through. That is not style: the
// coverage report derives the builder's vocabulary by parsing this package and
// reading the first argument of every reject site (inventory_guard_test.go), so
// a variable there is a key the report cannot name.
//
// `test boot` has no arm here: collectTestDecl never sets it, because the
// construct lowers. The key is still in the report's vocabulary, through
// appfield.go's stageAppValue, which holds two literal reject sites for it,
// both naming the reason a boot value could not be established as an app
// struct.
func (g *gen) rejectTestBody(c testCaseDecl) {
	if c.reportRefusal {
		switch c.refusedBy {
		case "test clock":
			g.reject("test clock", c.refusalDetail, c.refusalAt)
		case "test declaration in an imported module":
			g.reject("test declaration in an imported module", c.refusalDetail, c.refusalAt)
		default:
			// The construct name of a node the parser cannot put in a group
			// body. Computed rather than literal because the node decides it,
			// which is the same status every other `constructName(n)` refusal
			// has.
			g.reject(c.refusedBy, c.refusalDetail, c.refusalAt)
		}
	}
}

// refuseDuplicateTestNames refuses a module whose cases do not have distinct
// RUNNABLE names.
//
// `nomi test` reports duplicates instead of running anything
// (frontend.DuplicateTestNames), which is a different report from the one a
// built table would produce. Refusing the file is the honest answer: the
// builder does not lower that report, so it must not pretend the file is
// covered.
//
// The name compared is the JOINED path, which is what makes this a group
// question rather than a `test` question: `tests "a" { test "x" }` twice is one
// collision under `a / x`, and two bare `test "x"` in different groups are not
// a collision at all. It reads the same list the cases are built from, so the names
// in the table and the names checked here cannot disagree.
func (g *gen) refuseDuplicateTestNames(cases []testCaseDecl) {
	seen := map[string]int{}
	type dup struct {
		name string
		at   ast.Node
	}
	var dups []dup
	for _, c := range cases {
		if c.refusedBy != "" {
			// Not in the table, so it cannot collide with anything in it.
			continue
		}
		name := strings.Join(c.names, " / ")
		seen[name]++
		if seen[name] == 2 {
			// Blamed on the SECOND occurrence, which is the one the reader has
			// to move, and reported once however many times it repeats.
			dups = append(dups, dup{name: name, at: c.body})
		}
	}
	sort.Slice(dups, func(i, j int) bool { return dups[i].name < dups[j].name })
	for _, d := range dups {
		g.reject("duplicate test name", d.name, d.at)
	}
}

// --- assertions ------------------------------------------------------------

// --- value recording -------------------------------------------------------

// skipAssertionArg classifies one call argument: a lambda, a block or a type name explains nothing; a
// literal explains something only for a predicate.
func skipAssertionArg(arg ast.Node, includeLiterals bool) bool {
	switch arg.(type) {
	case *ast.Lambda, *ast.Block, *ast.FieldAccessor, *ast.TypeIdent:
		return true
	case *ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.CodepointLit:
		return !includeLiterals
	default:
		return false
	}
}

// How an operand's VALUE reads is inspect.go's: gen.inspectCode. A `values:`
// row is rendered structurally and dispatches nowhere, so scalars and
// composites are rendered in that one file.

// renderNode is how an expression reads in a report. format.RenderNode, not a
// second renderer, so `assert  x==2 ` normalizes the way the formatter does.
func renderNode(n ast.Node) string { return format.RenderNode(n) }

// --- the test binary's entry point -----------------------------------------
