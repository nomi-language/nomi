package irbuild

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// kindInvalidClasses is the closed vocabulary a non-suppression comparison may
// claim, and each entry is a reason a site that finds kindInvalid is NOT
// declining to name a blocker. A class is checkable; a prose paragraph per
// site is not.
//
//   - reports      this arm names its own refusal, directly through reject or by
//     recording a `why` its caller rejects under. Nothing is lost.
//   - propagates   a kind CONSTRUCTOR or forwarder passes kindlessness on. The
//     site that JUDGES the operand is the caller, and that is where
//     the decline is reported or counted. Counting here too would
//     tally one gap twice, which is the bias cascade.go exists to
//     prevent.
//   - cascade      deliberately suppresses the ECHO of a refusal already
//     recorded elsewhere. See cascade.go.
//   - marker       kindInvalid as a typeDef's `inner` is how a MARKER type — a
//     distinct carrying no value — is represented. Domain meaning,
//     not a refused operand. A distinct whose inner really was
//     refused has lowerable=false and is caught by rejectUnlowered.
//   - sentinel     kindInvalid in a `want` position means "no expected type".
//     Nothing was refused; nothing is being judged.
//   - lookup       `!= kindInvalid` asks whether a NAME resolves to a kind at
//     all, to choose a lowering path. A miss falls through to a site
//     that reports.
//   - mechanical   lowering plumbing — hold, operand, forceExpr, coerce.
//     No judgement is pending at the site.
//   - no-position  outside the builder walk, in registry construction over
//     stdlib declarations or Go reflect types. There is no Nomi
//     position to record, so there is nothing a suppression could
//     name.
//   - fenced       the comparison asserts that a kindless operand CANNOT arrive,
//     and its body PANICS rather than declining. Nothing is
//     suppressed because no user input reaches it: the shape that
//     admits the operand was restricted upstream, so a kindless one
//     is a producer bug in this package. Distinct from `mechanical`:
//     "no judgement is pending at the site" is false where the site's
//     judgement is "this cannot happen".
//
// `fenced` is the one class with a CHECKED obligation, because it is the one
// that could otherwise explain a real decline away: a marker claiming it is
// accepted only when the guarded body actually panics. Swap the panic for a
// `return bad()` and the class stops applying, which is the whole difference
// between asserting impossibility and declining quietly.
var kindInvalidClasses = map[string]bool{
	"reports":     true,
	"propagates":  true,
	"cascade":     true,
	"marker":      true,
	"sentinel":    true,
	"lookup":      true,
	"mechanical":  true,
	"no-position": true,
	"fenced":      true,
}

// The suppression counter is only as complete as the set of guards routed
// through it, and that set cannot be maintained by memory: a new
// `if x.k == kindInvalid { return bad() }` added anywhere in this package
// silently makes `masked` an undercount, which is the exact failure the counter
// exists to expose.
//
// So the guarantee is this: **every comparison in this package is either
// routed through g.suppress()/g.suppressed()/g.suppressedKind() or carries a
// `kindInvalid: <class>` marker naming why it is not a suppression.** Add a
// comparison and this test fails until you do one or the other. A count cannot
// be bumped to make it pass.
//
// The recognizer resolves the guarded BODY through go/ast, so an explanatory
// comment between the guard and the call cannot hide a call the way it would
// from a fixed line window.
func TestSuppression_EveryKindInvalidComparisonIsRoutedOrExplained(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("cwd: %v", err)
	}
	entries, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	sort.Strings(entries)

	routed, byClass := 0, map[string]int{}
	var unexplained, contradictory, unfenced []string

	for _, path := range entries {
		base := filepath.Base(path)
		// _test.go files are not the builder; suppression.go defines the
		// primitives and would count itself.
		if strings.HasSuffix(base, "_test.go") || base == "suppression.go" {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", base, err)
		}
		lines := strings.Split(string(src), "\n")
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
		if err != nil {
			t.Fatalf("parsing %s: %v", base, err)
		}
		for _, cmp := range kindInvalidComparisons(file) {
			line := fset.Position(cmp.expr.Pos()).Line
			at := base + ":" + itoa(line)
			marked := hasKindInvalidMarker(lines, line, fset, cmp)
			if cmp.body != nil && bodyRoutes(cmp.body) {
				routed++
				if marked != "" {
					// A marker on a routed guard would let somebody silence a
					// real suppression by explaining it away, which is the one
					// failure mode this vocabulary must not enable.
					contradictory = append(contradictory, at+" is routed AND marked `"+marked+"`")
				}
				continue
			}
			switch {
			case marked == "":
				unexplained = append(unexplained, at+"  "+strings.TrimSpace(lines[line-1]))
			case !kindInvalidClasses[marked]:
				unexplained = append(unexplained, at+"  unknown class `"+marked+"`")
			case marked == "fenced" && !bodyPanics(cmp.body):
				unfenced = append(unfenced, at+"  "+strings.TrimSpace(lines[line-1]))
			default:
				byClass[marked]++
			}
		}
	}

	if len(unexplained) > 0 {
		t.Errorf("%d kindInvalid comparison(s) are neither routed nor explained:\n  %s\n\n"+
			"Decide which this is:\n"+
			"  - it DECLINES to name a blocker because the operand had no kind -> route it through\n"+
			"    g.suppress(at) / g.suppressed(at) / g.suppressedKind(at), passing the node the\n"+
			"    declined check would have been reported at\n"+
			"  - it does not -> put `// kindInvalid: <class> — <reason>.` on the line above it,\n"+
			"    with <class> one of: %s\n"+
			"See internal/irbuild/suppression.go for what the counter means.",
			len(unexplained), strings.Join(unexplained, "\n  "), strings.Join(sortedKeys(kindInvalidClasses), ", "))
	}
	if len(contradictory) > 0 {
		t.Errorf("%d routed guard(s) also carry a not-a-suppression marker:\n  %s\n"+
			"A marker must never explain away a guard that really does decline.",
			len(contradictory), strings.Join(contradictory, "\n  "))
	}
	if len(unfenced) > 0 {
		t.Errorf("%d comparison(s) claim `fenced` and do not panic:\n  %s\n"+
			"`fenced` says a kindless operand cannot arrive. A body that declines instead of "+
			"panicking is handling the operand it claims cannot exist, which is either a "+
			"suppression to route or a different class.",
			len(unfenced), strings.Join(unfenced, "\n  "))
	}

	// A census that found nothing would pass vacuously forever.
	if routed == 0 {
		t.Fatal("found no routed guards at all — the recognizer is broken, not the code")
	}
	explained := 0
	for _, n := range byClass {
		explained += n
	}
	if explained == 0 {
		t.Fatal("found no explained comparisons at all — the marker recognizer is broken")
	}
	var census []string
	for _, class := range sortedKeys(kindInvalidClasses) {
		if byClass[class] > 0 {
			census = append(census, fmt.Sprintf("%s %d", class, byClass[class]))
		}
	}
	t.Logf("kindInvalid comparisons: %d routed, %d explained (%s)",
		routed, explained, strings.Join(census, ", "))
}

// kindInvalidCmp is one comparison against kindInvalid, with the body it guards
// when it guards one.
type kindInvalidCmp struct {
	expr *ast.BinaryExpr
	// body is the statements taken when the comparison's `if`/`case` is
	// entered. Nil for a comparison that guards nothing — a returned boolean
	// expression, say — which can never be a suppression on its own.
	body []ast.Stmt
	// stmtLine is the line the enclosing if/case opens on, so a marker above a
	// multi-line condition still counts.
	stmtLine token.Pos
}

// kindInvalidComparisons finds every `x == kindInvalid` / `x != kindInvalid` in
// a file and resolves the body each one guards.
//
// go/ast rather than a line window, because a window cannot see a suppression
// call under a multi-line comment.
func kindInvalidComparisons(file *ast.File) []kindInvalidCmp {
	// Innermost enclosing guard per comparison, found by walking down: an
	// if/case records its condition's comparisons before descending further.
	var out []kindInvalidCmp
	seen := map[*ast.BinaryExpr]bool{}
	record := func(cond ast.Node, body []ast.Stmt, open token.Pos) {
		ast.Inspect(cond, func(n ast.Node) bool {
			be, ok := n.(*ast.BinaryExpr)
			if !ok || seen[be] || !isKindInvalidComparison(be) {
				return true
			}
			seen[be] = true
			out = append(out, kindInvalidCmp{expr: be, body: body, stmtLine: open})
			return true
		})
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch t := n.(type) {
		case *ast.IfStmt:
			if t.Cond != nil {
				record(t.Cond, t.Body.List, t.Pos())
			}
		case *ast.CaseClause:
			for _, e := range t.List {
				record(e, t.Body, t.Pos())
			}
		}
		return true
	})
	// Anything left over guards no body.
	ast.Inspect(file, func(n ast.Node) bool {
		be, ok := n.(*ast.BinaryExpr)
		if !ok || seen[be] || !isKindInvalidComparison(be) {
			return true
		}
		seen[be] = true
		out = append(out, kindInvalidCmp{expr: be, stmtLine: be.Pos()})
		return true
	})
	return out
}

func isKindInvalidComparison(be *ast.BinaryExpr) bool {
	if be.Op != token.EQL && be.Op != token.NEQ {
		return false
	}
	return isKindInvalidIdent(be.X) || isKindInvalidIdent(be.Y)
}

func isKindInvalidIdent(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "kindInvalid"
}

// bodyRoutes reports whether these statements reach one of the three counted
// calls. Nested statements count: a routed guard may bind names or emit a probe
// walk before declining, and caseInto's does both.
func bodyRoutes(body []ast.Stmt) bool {
	routed := false
	for _, s := range body {
		ast.Inspect(s, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "suppress", "suppressed", "suppressedKind":
				routed = true
			}
			return true
		})
	}
	return routed
}

// bodyPanics reports whether these statements panic, which is `fenced`'s
// obligation. A nil body — a comparison guarding nothing at all — panics
// nowhere, so it can never claim the class.
//
// `panic` and nothing else: `requirePos`-style helpers that panic on the
// caller's behalf are deliberately not recognized. A site claiming a kindless
// operand is impossible should say so where a reader of that line can see it.
func bodyPanics(body []ast.Stmt) bool {
	panics := false
	for _, s := range body {
		ast.Inspect(s, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, isID := call.Fun.(*ast.Ident); isID && id.Name == "panic" {
				panics = true
			}
			return true
		})
	}
	return panics
}

// hasKindInvalidMarker returns the class claimed on the line above the
// comparison — or above the if/case it guards, for a condition spread over
// several lines — and "" when there is none.
func hasKindInvalidMarker(lines []string, cmpLine int, fset *token.FileSet, cmp kindInvalidCmp) string {
	const prefix = "// kindInvalid:"
	candidates := []int{cmpLine}
	if open := fset.Position(cmp.stmtLine).Line; open != cmpLine {
		candidates = append(candidates, open)
	}
	for _, at := range candidates {
		if at < 2 || at-2 >= len(lines) {
			continue
		}
		above := strings.TrimSpace(lines[at-2])
		if !strings.HasPrefix(above, prefix) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(above, prefix))
		if i := strings.IndexAny(rest, " \t"); i > 0 {
			return rest[:i]
		}
		return rest
	}
	return ""
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
