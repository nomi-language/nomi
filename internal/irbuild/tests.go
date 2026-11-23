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
// Recording is lowered only for operators lexically inside an assertion's
// subject, and a callee's body is a separate declaration that is never
// lexically inside one, so a callee's body holds no recording at all. No
// flag is consulted at run time.
//
// The one place lexical containment is the WRONG test is a lambda body, which is
// lexically inside the subject but is a separate ACTIVATION and is therefore
// excluded. That seam is the only exception — every other construct that
// is lexically inside a subject is also in the assertion's own activation.
//
// Suspension is a build-time fact about where the builder is, not a flag the
// program reads. testdata/tests_lambda_boundary.nomi is the pin.

// --- collecting the cases --------------------------------------------------

// testSetup is a `group`'s `boot` line and `setup`, as a case in the
// group sees them.
//
// boot is the group's `boot` call; body is the setup, whose value the case's
// pattern binds. A frame exists where `Boot != nil || Setup != nil`, which is
// the exact condition frontend's collectTestCasesInBlock pushes one under.
//
// boot rides along even when a group is refused, because a refusal must not
// hide what is inside it and the boot line's argument is inside it.
type testSetup struct {
	// group is the `group` declaration the frame came from. A frame is
	// shared by every case in the group.
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
	// self is the self type of a prompt inside an impl block's items (the
	// receiver's name), or "".
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
	// row is the row of its group's `subtests` block the case runs under,
	// or nil: frontend.TestCase's Row.
	row *testRow
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
// The recursion into an impl block's items is mirrored too, with
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
			out = append(out, collectTestDecl(t, n, nil, nil, "", ref, nil)...)
		}
	}
	// One refusal per refused CONSTRUCT rather than per case beneath it, and
	// the first case carries it so the report is in declaration order.
	reported := map[ast.Node]bool{}
	for i := range out {
		if out[i].refusedBy == "" {
			continue
		}
		if !reported[out[i].refusalAt] {
			reported[out[i].refusalAt] = true
			out[i].reportRefusal = true
		}
	}
	return out
}

// testRefusal is the refusal a case inherits from the construct it sits in.
type testRefusal struct {
	construct string
	detail    string
	at        ast.Node
}

// testRow is one row of a group's `subtests` block: the row's pattern
// binds value ahead of the case's boot line, setup and body.
type testRow struct {
	rows  *ast.GroupSubtests
	value ast.Node
	index int
}

// collectTestDecl collects one `test` or `group` declaration and its
// descendants. ref is inherited: a group this builder turns down refuses every
// case beneath it, and the reason and position are the group's. clock is the
// inherited `clock` variant, "" at the top level.
func collectTestDecl(t *ast.TestDecl, owner ast.Node, prefix []string, setups []testSetup, clock string, ref testRefusal, row *testRow) []testCaseDecl {
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
			row:           row,
			refusedBy:     ref.construct,
			refusalDetail: ref.detail,
			refusalAt:     ref.at,
		}}
	}
	if t.Clock != nil {
		// `clock Clock.Virtual` lowers: the case's group records the clock
		// (irtestgroup.go), and the VM runner runs the body inside
		// `rt/vclock.RunCase`'s virtual-time bubble.
		// `clock Clock.System` lowers to nothing, because the real clock is
		// already what a case with no clause gets.
		//
		// THE CLAUSE EXPRESSION IS NOT LOWERED, AND THAT IS COMPLETE RATHER
		// THAN PARTIAL. Nothing evaluates it: the clock has to be known
		// before a case runs while `boot` and `setup` run inside it, so
		// the front end reads the variant syntactically too. There is no `probe`
		// entry for it any more for the same reason — an expression nobody
		// evaluates has no operands to refuse.
		//
		// An unreadable spelling refuses rather than guessing a clock or
		// lowering a failure. The front end marks that case InvalidClock and
		// the runner reports it as failed with its own observable text, and
		// mirroring the string here would be a second copy of observable text
		// (rt/trap.go's rule); a refusal is never a wrong answer. Defensive in
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
			}
		}
	}
	// A `boot` constructs the active App value its descendants see. This walk
	// does not stage it: the VM starts the boot
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
	if t.Subtests != nil {
		// frontend's rule: each row runs the group's items, its cases named
		// with the row's label (ast.GroupRowLabels, the one copy).
		rows := ast.GroupRows(t.Subtests)
		var out []testCaseDecl
		for i, label := range ast.GroupRowLabels(t.Subtests) {
			rowNames := appendTestName(prefix, ast.GroupRowSegment(t.Name, label))
			r := &testRow{rows: t.Subtests, value: rows[i], index: i}
			for _, stmt := range t.Body.Stmts {
				if child, ok := stmt.(*ast.TestDecl); ok {
					out = append(out, collectTestDecl(child, owner, rowNames, local, clock, ref, r)...)
				}
			}
		}
		return out
	}
	var out []testCaseDecl
	for _, stmt := range t.Body.Stmts {
		child, ok := stmt.(*ast.TestDecl)
		if !ok {
			// The parser admits only clock, boot, setup and test in a group
			// body ("a group body may contain only clock, boot, setup, subtests and test
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
		out = append(out, collectTestDecl(child, owner, names, local, clock, ref, nil)...)
	}
	return out
}

// attachedTestCases collects the `//!` prompt tests a file-scope declaration
// carries, its own and its items', in frontend.CollectTestCases' order and
// under its names (attachedTestCasesIn). Every case's owner is the file-scope
// declaration, which is where it is built from.
//
// A case inside an impl block's items has the block's receiver as its self
// type, through which
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
		if v, isBlock := n.(*ast.ImplBlock); isBlock {
			inner := appendTestName(prefix, attachedTestOwner(v))
			recv := analysis.TypeExprBaseName(v.Receiver)
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

// attachedTestDecls lowers the `//!` prompt cases a declaration carries.
func (g *gen) attachedTestDecls(cases []testCaseDecl) {
	for _, c := range cases {
		if c.attached {
			g.testCase(c)
		}
	}
}

// testDecl lowers the cases one file-scope `test` or `group` declaration
// contributes, in declaration order.
func (g *gen) testDecl(cases []testCaseDecl) {
	for _, c := range cases {
		if !c.attached {
			g.testCase(c)
		}
	}
}

// testCase lowers one case, or reports the refusal it carries.
func (g *gen) testCase(c testCaseDecl) {
	g.at(c.line)
	if g.errorDeclBody(c.owner) {
		return
	}
	if c.refusedBy != "" {
		g.rejectTestBody(c)
		return
	}

	name := strings.Join(c.names, " / ")

	prevInTest, prevResult := g.inTest, g.result
	g.inTest, g.result = true, kindInvalid
	// irTestBody declares a grouped case's body in the module's test table and queues an
	// ungrouped one for irRetryWalkOnlyTestBodies. The decline a case it did
	// not build left in g.dc is cleared here, so it does not leak into the
	// next attempt. See irtestbody.go.
	g.irTestBody(c, name)
	g.dc.take()
	g.irBodyObserve(irBodyTestBody, true)
	g.inTest, g.result = prevInTest, prevResult
}

// rejectTestBody reports the refusal a case carries.
//
// The refusal itself is reported by ONE case per refused construct
// (reportRefusal), at that construct's own position: a `clock` group of six
// cases is one `test clock` blamed on the group, not six blamed on its bodies.
//
// The key is a LITERAL at each g.reject, and the switch is what makes it one
// rather than passing c.refusedBy straight through. That is not style: the
// coverage report derives the builder's vocabulary by parsing this package and
// reading the first argument of every reject site (inventory_guard_test.go), so
// a variable there is a key the report cannot name.
//
// `test boot` has no arm here: collectTestDecl never sets it, because the
// construct lowers.
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
// question rather than a `test` question: `group "a" { test "x" }` twice is one
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

// skipAssertionArg classifies one call argument: a lambda, a capture, a block or a type name explains nothing; a
// literal explains something only for a predicate.
//
// A `"""` literal is recorded all the same. Its row never prints, since
// rt.RecordOperand drops a literal operand's row, but the Record carries
// where the literal is written: a failed `io.replay` numbers its diff's
// transcript lines from it (internal/vm's replayDiff).
func skipAssertionArg(arg ast.Node, includeLiterals bool) bool {
	switch a := arg.(type) {
	case *ast.Lambda, *ast.Block, *ast.FieldAccessor, *ast.Capture, *ast.TypeIdent:
		return true
	case *ast.StringLit:
		return !includeLiterals && !(a.Triple && !a.Raw)
	case *ast.IntLit, *ast.FloatLit, *ast.CodepointLit:
		return !includeLiterals
	default:
		return false
	}
}

// renderNode is how an expression reads in a report. format.RenderNode, not a
// second renderer, so `assert  x==2 ` normalizes the way the formatter does.
func renderNode(n ast.Node) string { return format.RenderNode(n) }
