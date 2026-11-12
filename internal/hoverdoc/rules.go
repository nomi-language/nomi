package hoverdoc

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Hovers that state a rule the text does not show, from what the checker
// recorded: an `if` with no `else` is a Maybe or a statement (spec §8
// *`if`/`else`*), and a binding's `else` runs when the pattern does not
// match and leaves or gives a fallback (spec §10 *Bindings with `else`*).
// Each writes the construct as this file spells it, the condition, value
// and pattern quoted from the source when they fit on a line.

// quoteLimit is the longest source text a rule hover quotes; longer or
// multi-line text is named instead ("the condition").
const quoteLimit = 40

// sourceOf is the text of n as written, or "" when it has no extent, runs
// over several lines or is longer than quoteLimit.
func sourceOf(content string, n ast.Node) string {
	sp := spanOf(n)
	if sp.IsZero() {
		sp = leaveSpan(n)
	}
	if sp.IsZero() || sp.StartLine != sp.EndLine {
		return ""
	}
	line := lineText(content, sp.StartLine)
	if sp.StartCol < 1 || sp.EndCol-1 > len(line) || sp.EndCol <= sp.StartCol {
		return ""
	}
	text := strings.TrimSpace(line[sp.StartCol-1 : sp.EndCol-1])
	if len(text) > quoteLimit {
		return ""
	}
	return text
}

// leaveSpan is the extent of a `return`, `break` or `continue`, which the
// parser records no span for: its keyword through its value.
func leaveSpan(n ast.Node) ast.Span {
	var line, col int
	var word string
	var value ast.Node
	switch l := n.(type) {
	case *ast.Return:
		line, col, word, value = l.Line, l.Col, "return", l.Value
	case *ast.Break:
		line, col, word, value = l.Line, l.Col, "break", l.Value
	case *ast.Continue:
		line, col, word, value = l.Line, l.Col, "continue", l.Value
	default:
		return ast.Span{}
	}
	if line == 0 {
		return ast.Span{}
	}
	sp := ast.Span{StartLine: line, StartCol: col, EndLine: line, EndCol: col + len(word)}
	if value != nil {
		vsp := spanOf(value)
		if vsp.IsZero() {
			return ast.Span{}
		}
		sp.EndLine, sp.EndCol = vsp.EndLine, vsp.EndCol
	}
	return sp
}

// aType is a type as code with its article: "a `List<Int>`", "an `Int`".
func aType(t string) string {
	if t != "" && strings.ContainsRune("AEIOU", rune(t[0])) {
		return "an `" + t + "`"
	}
	return "a `" + t + "`"
}

// lineText is the 1-based line n of content, without its line break.
func lineText(content string, n int) string {
	for i := 1; i < n; i++ {
		j := strings.IndexByte(content, '\n')
		if j < 0 {
			return ""
		}
		content = content[j+1:]
	}
	if j := strings.IndexByte(content, '\n'); j >= 0 {
		content = content[:j]
	}
	return strings.TrimSuffix(content, "\r")
}

// code is text as inline code, or the fallback words when there is none.
func code(text, otherwise string) string {
	if text == "" {
		return otherwise
	}
	return "`" + text + "`"
}

// blockResult is the node whose value a block has: its last statement,
// unwrapped from an expression statement.
func blockResult(b *ast.Block) ast.Node {
	if b == nil || len(b.Stmts) == 0 {
		return nil
	}
	last := b.Stmts[len(b.Stmts)-1]
	if es, ok := last.(*ast.ExprStmt); ok {
		return es.Expr
	}
	return last
}

// leaves reports whether the checker gave n the type of an expression
// that never produces a value (`return`, `break`, `continue`, a call that
// never returns), and names how it leaves.
func leaves(fa *analysis.FileAnalysis, n ast.Node) (how string, ok bool) {
	switch n.(type) {
	case *ast.Return:
		return "`return`", true
	case *ast.Break:
		return "`break`", true
	case *ast.Continue:
		return "`continue`", true
	}
	if n == nil {
		return "", false
	}
	if ty := fa.ExprTypes[n]; ty != nil && analysis.ResolveTypeVar(ty) == analysis.TypeInfallible {
		return "an expression that never produces a value", true
	}
	return "", false
}

// ifRule is the hover of the `if` of an `if` with no final `else`: the
// `if` and its type, and what it gives. ok is false for an `if` with an
// `else`, which keeps its typed hover.
func (k *keywordSite) ifRule() (context string, ok bool) {
	var at *ast.If
	for _, n := range k.path {
		if i, isIf := n.(*ast.If); isIf && i.Line == k.at.Line && i.Col == k.at.Col {
			at = i
		}
	}
	if at == nil {
		return "", false
	}
	head := elseIfHead(k.nodes, at)
	links := []*ast.If{head}
	for {
		next, isIf := links[len(links)-1].Else.(*ast.If)
		if !isIf || next == nil {
			break
		}
		links = append(links, next)
	}
	if links[len(links)-1].Else != nil {
		return "", false
	}
	pattern, value := "", ""
	if at.CondPattern != nil {
		pattern, value = sourceOf(k.content, at.CondPattern), sourceOf(k.content, at.Cond)
	}
	cond := code(sourceOf(k.content, at.Cond), "the condition") + " holds"
	notCond := code(sourceOf(k.content, at.Cond), "the condition") + " doesn't hold"
	if at.CondPattern != nil {
		cond = code(value, "the value") + " matches " + code(pattern, "the pattern")
		notCond = code(value, "the value") + " doesn't match " + code(pattern, "the pattern")
	}
	branch := sourceOf(k.content, blockResult(at.Then))
	if branch == "" {
		branch = "…"
	}
	if ty := k.fa.ElselessIfs[head]; ty != nil {
		k.form = "if … then " + branch + ": " + displayValueType(ty)
		if len(links) > 1 {
			k.text = "An `if` chain without a final `else` is a `Maybe`: `Some` of the value of the branch that runs, `None` when no condition holds."
			return "maybe", true
		}
		some := "`Some` of the branch's value"
		if branch != "…" {
			some = "`Some(" + branch + ")`"
		}
		k.text = "An `if` without `else` is a `Maybe`: " + some + " when " + cond + ", `None` when it doesn't."
		return "maybe", true
	}
	k.form = "if … then " + branch
	how, left := leaves(k.fa, blockResult(at.Then))
	if left {
		k.text = "An `if` without `else` whose branch leaves (" + how + ") is a statement with no value: the code after it runs only when " + notCond + "."
		return "statement", true
	}
	k.text = "An `if` without `else` whose branch is `Unit` is a statement: it runs the branch when " + cond + " and has no value."
	return "statement", true
}

// elseIfHead is the `if` whose `else if` chain at belongs to, at itself
// when it heads one.
func elseIfHead(nodes []ast.Node, at *ast.If) *ast.If {
	head := at
	for _, n := range nodes {
		ast.Inspect(n, func(c ast.Node) bool {
			i, ok := c.(*ast.If)
			if !ok || i == nil {
				return true
			}
			for l := i; l != nil; {
				next, isIf := l.Else.(*ast.If)
				if !isIf || next == nil {
					break
				}
				if next == at && head == at {
					head = i
				}
				l = next
			}
			return true
		})
	}
	// A link of a link: follow up to the chain's first `if`.
	if head != at {
		return elseIfHead(nodes, head)
	}
	return head
}

// bindingElseRule is the hover of a binding's `else`: when it runs, and
// whether it leaves or gives a fallback for the pattern's payload.
func (k *keywordSite) bindingElseRule() {
	var pb *ast.PatternBinding
	for _, n := range k.nodes {
		ast.Inspect(n, func(c ast.Node) bool {
			if b, ok := c.(*ast.PatternBinding); ok && b.Else != nil && b.Else.Line == k.at.Line && b.Else.Col == k.at.Col {
				pb = b
			}
			return pb == nil
		})
	}
	if pb == nil {
		return
	}
	pattern, value := sourceOf(k.content, pb.Pattern), sourceOf(k.content, pb.Value)
	if pattern == "" {
		k.form = "Pattern"
	} else {
		k.form = pattern
	}
	k.form += " = " + func() string {
		if value == "" {
			return "value"
		}
		return value
	}() + " else …"
	runs := "Runs when " + code(value, "the value") + " doesn't match " + code(pattern, "the pattern")
	// What each path gives: the block's value, or each arm's.
	var results []ast.Node
	if pb.Else.Block != nil {
		results = append(results, blockResult(pb.Else.Block))
	}
	for _, arm := range pb.Else.Arms {
		results = append(results, arm.Body)
	}
	// The checker checks each path against the payload's type when the
	// pattern has one a fallback could stand in for (one variant whose
	// own pattern always matches), and against nothing otherwise.
	var fallback ast.Node
	var payload analysis.Type
	how := ""
	for _, r := range results {
		if r == nil {
			continue
		}
		if want := k.fa.ExpectedTypes[r]; want != nil && payload == nil {
			payload = want
		}
		if leaving, left := leaves(k.fa, r); !left {
			if fallback == nil {
				fallback = r
			}
		} else if how == "" {
			how = leaving
		}
	}
	arms := ""
	if len(pb.Else.Arms) > 0 {
		arms = "; its arms match the value that didn't"
	}
	switch {
	case fallback != nil && payload != nil:
		k.text = runs + arms + ". Where it doesn't leave the function, its value, " + aType(displayValueType(payload)) + ", stands in for the pattern's payload, and the pattern's names bind from it."
	case fallback != nil:
		k.text = runs + arms + ". Where it doesn't leave the function, its value stands in for the pattern's payload, and the pattern's names bind from it."
	case payload != nil:
		k.text = runs + arms + ". Here it leaves the function (" + how + "); " + aType(displayValueType(payload)) + " in its place would stand in for the pattern's payload instead."
	case noSinglePayload(pb.Pattern):
		k.text = runs + arms + ". It must leave the function (`return`, `break`, `continue`, or a call that never returns), so the pattern's names are bound after it."
	default:
		k.text = runs + arms + ". Here it leaves the function (" + how + "), so the pattern's names are bound after it."
	}
}

// noSinglePayload reports whether a binding's pattern is one of the
// shapes spec §10 *Bindings with `else`* names as having no payload a
// fallback could stand in for: a bracket, map or tuple pattern. Its
// `else` must leave.
func noSinglePayload(p ast.Node) bool {
	switch ast.WithoutAs(p).(type) {
	case *ast.ListPattern, *ast.MapPattern, *ast.TuplePattern:
		return true
	}
	return false
}
