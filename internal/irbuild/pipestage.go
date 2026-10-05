package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// Pipeline-stage recording: the values a pipeline passed through, kept so a
// failed assertion can explain the pipeline without re-running it.
//
// # There are TWO collectors, and this is the second one
//
// gen.recordOperand fills the `values:` rows. The stage list is a SECOND
// slice, switched on for exactly two shapes and off everywhere else:
//
//   - `x = a |> b()` — a binding whose value is a pipe. The stages are
//     collected for the duration of the initializer and attached to the
//     binding, so an `assert x` on the bare name prints `defined as:` AND a
//     `pipeline values:` block.
//   - `testing.check(a |> b())` — a piped check argument. The stages are
//     collected for the duration of the argument and, if the check FAILS, one
//     more `values:` row is appended whose Expr is the WHOLE subject, whose
//     Value is the subject's, and whose Pipeline carries the stages.
//
// A report that drops the `pipeline values:` block is invisible to a
// comparison of runs where every assertion PASSES. See
// testdata/pipe_stages.nomi, whose assertions fail on purpose so the report
// text itself is compared.
//
// # What is recorded, in what order
//
// One stage per CUMULATIVE PREFIX of the chain, leftmost value first and the
// whole pipe last. So `a |> b() |> c()` records `a`, then `a |> b()`, then
// `a |> b() |> c()`, and the expression text of a stage is the prefix's own
// source rather than the stage's.
//
// The report does the compaction, not this: rt.WriteAssertionFailure strips a
// preceding stage's text off an observed VALUE's pipeline rows and leaves a
// BINDING's alone, so the same stage list prints `|> Iter.any?(...)` in one
// place and the whole two-line prefix in the other. Feeding either one
// pre-compacted would produce a green differential and wrong text against the
// other.
//
// # What is refused, and why the door is this narrow
//
// A stage list that is wrong is worse than one that is absent, so the shapes
// admitted here are the ones whose stage sequence is a function of the SPINE
// alone. Two things break that and both are refused rather than approximated:
//
//   - A nested pipe. A pipe inside the chain would interleave its own stages
//     into this list — `x |> f(y |> g())` would record four — and whether one
//     inside a lambda body records is a question of which scope collects. A
//     parenthesised left operand is the same hazard wearing a disguise:
//     `(a |> b()) |> c()` would record `a` and `a |> b()` from the inner pipe
//     and then `(a |> b())` again from the outer.
//   - A stage that is not a call, a `then`, an `if` or a `case`. `|> try`
//     and `|> dbg` are what is left, and both are refused as pipe stages in
//     their own right.
//
//     `|> if` and `|> case` are admitted: a stage is recorded for EVERY stage
//     shape, so the stage sequence of a keyword stage is a function of the
//     spine exactly as a call chain's is. A failing bare-name `assert` over
//     `12 |> if Int.equal?(12) { False } else { True }` prints THREE rows —
//     `12`, `12 |> Int.equal?(12)`, and the whole chain rendered back in the
//     KEYWORD spelling — and the bare `Some(3) |> case { … }` prints two.
//     See testdata/pipe_stage_keyword_report.nomi, whose assertions fail on
//     purpose so the report text itself is compared.
//
// Both tests are syntactic and neither needs a kind, which is deliberate: a
// refusal that depends on a type would be masked in exactly the files this is
// for.
//
// # Recordable is not the same question as WORTH RECORDING
//
// Everything above is about whether a stage list would be RIGHT. Whether
// anyone will ever read it is a second question. Recording every recordable
// pipe-valued initializer would make `seen = counts |> Map.get(cell)` inside a
// fold render the whole map once per element and discard the text. That gate
// is pipestagereach.go's; a piped `testing.check` records
// unconditionally, because its rows are read whenever that check fails.

// pipeStagePrefixes is the chain's cumulative prefixes in RECORD order: the
// leftmost operand, then each pipe node from the innermost out. Nil when n is
// not a pipe.
//
// The prefixes are the chain's own nodes rather than copies, which is what lets
// the caller lower each one by lowering the next pipe node with the previous
// answer already in hand.
func pipeStagePrefixes(n ast.Node) []ast.Node {
	pipe, ok := n.(*ast.Binary)
	if !ok || pipe.Op != "|>" {
		return nil
	}
	var chain []*ast.Binary
	for {
		chain = append(chain, pipe)
		left, nested := pipe.Left.(*ast.Binary)
		if !nested || left.Op != "|>" {
			break
		}
		pipe = left
	}
	out := make([]ast.Node, 0, len(chain)+1)
	out = append(out, chain[len(chain)-1].Left)
	for i := len(chain) - 1; i >= 0; i-- {
		out = append(out, chain[i])
	}
	return out
}

// pipeStagesRecordable reports whether the stage list of this chain is a
// function of its spine, which is the condition the file header states.
func pipeStagesRecordable(prefixes []ast.Node) bool {
	if len(prefixes) < 2 {
		return false
	}
	if containsPipe(prefixes[0]) {
		return false
	}
	for _, node := range prefixes[1:] {
		stage := node.(*ast.Binary).Right
		for {
			grouped, wrapped := stage.(*ast.GroupedExpr)
			if !wrapped {
				break
			}
			stage = grouped.Expr
		}
		switch stage.(type) {
		case *ast.Call, *ast.Then, *ast.If, *ast.Case:
		default:
			return false
		}
		if containsPipe(stage) {
			return false
		}
	}
	return true
}

// containsPipe reports whether a pipe appears anywhere in n, INCLUDING inside a
// lambda body, whose stages are not part of this chain's list. Over-strict on
// purpose: the cost is a refusal and the alternative is a stage list that is
// wrong.
func containsPipe(n ast.Node) bool {
	if isNilNode(n) {
		return false
	}
	if b, isBinary := n.(*ast.Binary); isBinary && b.Op == "|>" {
		return true
	}
	for _, c := range childNodes(n) {
		if containsPipe(c) {
			return true
		}
	}
	return false
}
