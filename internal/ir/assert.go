package ir

import "strconv"

// The `assert` class: judge a subject, and record what the report will print.
//
// This file is point 3 of ir.go's package header: an assertion's rendered
// source text is a field on the node, `Assert.Text` and `Record.Text`.
//
// # Why a linear IR has to carry the text at all
//
// A failing assertion's report prints the SOURCE of the subject and of each
// recorded operand:
//
//	line 62: assertion failed
//	  assert found
//	  values:
//	    xs |> Iter.count()
//	      = 3
//
// The text comes from ONE function, `format.RenderNode`; `internal/irbuild`'s
// `renderNode` is a one-line alias for it. WHICH NODE is rendered is the
// producer's choice, and two cases need care:
//
//   - `try` in pipe position. The pipe renders the whole `*ast.Binary` and the
//     prefix form the `*ast.TryOp`, so the builder is handed the node to
//     render SEPARATELY from the node carrying the position (irbuild's tryOp
//     takes both `t` and `whole`).
//   - `assert pat = e`. irbuild's `patternAssert` builds a SYNTHETIC
//     `*ast.PatternDestructure`, because rendering the original node would
//     print the `assert` keyword twice.
//
// A tree-shaped IR could defer the choice; a linear one cannot, and this is
// the one place linearizing costs something. The cost
// is a FIELD, not a shape: the producer already materializes the text at
// lowering time, so the IR carries a result that was computed either way.
//
// # What is on the node and what is not
//
// | on the node                        | the consumer's                      |
// |------------------------------------|-------------------------------------|
// | the subject, as an operand         | `JudgeBool` against a tag compare   |
// | the KEYWORD that wrote it          | `Refute: true` against `Check: true`|
// | the rendered source text           | the Go string literal's quoting     |
// | the position                       | WHERE THE FAILURE GOES              |
//
// The last row is the fault/delivery division of ir.go's package header, and
// it is the reason there is no `trap bool` here. `assert`, `refute` and `testing.check` are ONE judgement with THREE
// deliveries — a `return` out of a test function, a `return` of
// `Err(f.NomiFailure())` out of an ordinary `fn`, and an ordinary `Result`
// VALUE for `check`. irbuild's `assertionExit` lives in the `jump` class for
// that reason.
//
// Whether the subject is a Bool, a `Maybe`/`Result` shape or an `Assertable`
// is not a field either. As with patterns (match.go), it is a consumer choice
// made from an operand's type rather than from a flag. irbuild reads
// `subject.k`. A kind on the node would restate a fact the producer and the
// consumer already hold. The VM reads a Bool or a `Maybe`/`Result` record off the
// value; an `Assertable` subject's verdict comes from a user impl the producer
// selected statically, so the producer calls it and the node carries that
// ANSWER as an operand (`WithAnswer`), not a flag.

// AssertKeyword is the spelling that wrote this assertion.
//
// THREE CONSTANTS AND NOT TWO BOOLS. `rt.AssertionSite` carries `Refute` and
// `Check` as separate fields and derives `Keyword()` from them, which admits
// the state `Refute && Check` that no source can write. One enum has every
// state reachable from some source text, and the printed spelling is a
// consumer's rendering of it.
type AssertKeyword uint8

const (
	// KeywordAssert is `assert e`: the assertion holds when the subject does.
	KeywordAssert AssertKeyword = iota + 1
	// KeywordRefute is `refute e`: the polarity is inverted, and the consumer
	// inverts the same comparison rather than writing a second one.
	KeywordRefute
	// KeywordCheck is `testing.check(e)`: the same judgement delivered as a
	// `Result` value instead of an exit. It is the keyword whose Assert WRITES
	// a destination; see NewAssert.
	KeywordCheck
)

func (k AssertKeyword) String() string {
	switch k {
	case KeywordAssert:
		return "assert"
	case KeywordRefute:
		return "refute"
	case KeywordCheck:
		return "check"
	}
	return "keyword?"
}

// Assert judges one subject at one assertion site.
type Assert struct {
	pos  Pos
	dst  Temp
	subj Temp
	kw   AssertKeyword
	text string
	// mismatch marks the failure edge of `assert pattern = value`: it always
	// fails, with the reason "pattern did not match" and subj, the value that
	// did not match, shown as the report's `actual`.
	mismatch bool
	binding  *AssertBinding
	// answer is an `Assertable` subject's own verdict: the
	// `Maybe<AssertionDetails>` its `Assertable.failure` returned, which the
	// producer calls before the judgement. NoTemp for every other subject.
	answer Temp
}

// AssertBinding is the report's "defined as:" block for a subject that is a
// bare name an ordinary binding introduced: the name, the binding's
// initializer as `format.RenderNode` renders it, and the subject's value.
//
// A FIELD AND NOT A CONSUMER LOOKUP, because the initializer's text is a
// fact of the source the graph has otherwise forgotten. The producer knows it
// when it lowers the binding, and records it then.
type AssertBinding struct {
	Name string
	Expr string
	Val  Temp
	// Stages is the `pipeline values:` block of a binding whose initializer
	// was a pipe: one cumulative prefix per stage, leftmost value first and
	// the whole pipe last.
	Stages []AssertStage
}

// AssertStage is one `pipeline values:` row: a pipe prefix's rendered source
// and the value it produced.
type AssertStage struct {
	Text string
	Val  Temp
}

// NewAssert judges subj, which the report will print as text.
//
// dst is the temporary the judgement WRITES, and it is NoTemp for `assert` and
// `refute`. That is not an omission: their value is the subject itself (irbuild
// returns the subject's own expression), which is what makes `truth = assert holds?()` an ordinary
// binding — so no instruction produces anything. `check` answers a fresh
// `Result` and names it. The field therefore varies over the population, which
// is the test a field has to pass here: a field that never varies carries no
// information.
//
// text is the subject as `format.RenderNode` renders it. It is REQUIRED: a
// report with an empty `Expr` prints a bare keyword, and a producer that has
// no node to render has not finished lowering the assertion.
func NewAssert(pos Pos, dst, subj Temp, kw AssertKeyword, text string) *Assert {
	requirePos(pos, "ir.NewAssert")
	if subj == NoTemp {
		panic("ir.NewAssert: an assertion with no subject judges nothing")
	}
	if kw < KeywordAssert || kw > KeywordCheck {
		panic("ir.NewAssert: unknown assert keyword " + strconv.Itoa(int(kw)))
	}
	if text == "" {
		panic("ir.NewAssert: an assertion's report prints its source text and this one has none")
	}
	if (dst != NoTemp) != (kw == KeywordCheck) {
		panic("ir.NewAssert: only " + KeywordCheck.String() + " writes a destination; " +
			kw.String() + " evaluates to its subject")
	}
	return &Assert{pos: pos, dst: dst, subj: subj, kw: kw, text: text}
}

// NewAssertMismatch is the failure edge of `assert pattern = value`, reached
// only when the pattern did not match. actual is the value that did not match
// and text is the pattern and value rendered without the keyword.
func NewAssertMismatch(pos Pos, actual Temp, text string) *Assert {
	a := NewAssert(pos, NoTemp, actual, KeywordAssert, text)
	a.mismatch = true
	return a
}

// WithBinding attaches the "defined as:" block and answers a.
func (a *Assert) WithBinding(b AssertBinding) *Assert {
	if b.Name == "" || b.Expr == "" || b.Val == NoTemp {
		panic("ir.Assert.WithBinding: a defined-as block needs a name, an expression and a value")
	}
	for _, st := range b.Stages {
		if st.Text == "" || st.Val == NoTemp {
			panic("ir.Assert.WithBinding: a pipeline stage needs a text and a value")
		}
	}
	a.binding = &b
	return a
}

// WithAnswer marks the subject as an `Assertable` and attaches the
// `Maybe<AssertionDetails>` its `Assertable.failure` answered, and answers a.
//
// AN OPERAND AND NOT A KIND FLAG. Which judgement applies is still read off
// the subject (see the file header). What the judgement cannot read off the
// subject is the verdict a user impl computes, because selecting that impl is
// the producer's static dispatch. So the producer calls it and hands the
// answer over.
func (a *Assert) WithAnswer(answer Temp) *Assert {
	if answer == NoTemp {
		panic("ir.Assert.WithAnswer: an Assertable subject's answer is a value")
	}
	if a.mismatch {
		panic("ir.Assert.WithAnswer: a pattern assertion's failure edge judges nothing")
	}
	a.answer = answer
	return a
}

// Answer is an `Assertable` subject's `failure` answer, or NoTemp.
func (a *Assert) Answer() Temp { return a.answer }

// Mismatch reports whether this is a pattern assertion's failure edge.
func (a *Assert) Mismatch() bool { return a.mismatch }

// Binding is the "defined as:" block, or nil.
func (a *Assert) Binding() *AssertBinding { return a.binding }

// Subject is the judged temporary.
func (a *Assert) Subject() Temp { return a.subj }

// Keyword is the spelling that wrote this assertion.
func (a *Assert) Keyword() AssertKeyword { return a.kw }

// Text is the subject's rendered source, as the report prints it.
func (a *Assert) Text() string { return a.text }

// Refuted reports whether this assertion's polarity is inverted.
//
// A PREDICATE OVER A SECOND FIELD. The consumer needs the polarity and not
// that `check` shares it with `assert`. Deriving it from the keyword keeps one field where two would admit a state
// no source can write.
func (a *Assert) Refuted() bool { return a.kw == KeywordRefute }

func (a *Assert) Pos() Pos  { return a.pos }
func (a *Assert) Dst() Temp { return a.dst }
func (a *Assert) AppendUses(dst []Temp) []Temp {
	dst = append(dst, a.subj)
	if a.answer != NoTemp {
		dst = append(dst, a.answer)
	}
	if a.binding != nil {
		dst = append(dst, a.binding.Val)
		for _, st := range a.binding.Stages {
			dst = append(dst, st.Val)
		}
	}
	return dst
}
func (a *Assert) String() string {
	kw := a.kw.String()
	if a.mismatch {
		kw = "assert-mismatch"
	}
	s := kw + " " + a.subj.String() + " " + strconv.Quote(a.text)
	if a.answer != NoTemp {
		s += " answer " + a.answer.String()
	}
	if a.binding != nil {
		s += " defined " + a.binding.Name + " = " + strconv.Quote(a.binding.Expr) + " " + a.binding.Val.String()
		for _, st := range a.binding.Stages {
			s += " stage " + strconv.Quote(st.Text) + " " + st.Val.String()
		}
	}
	if a.dst != NoTemp {
		return a.dst.String() + " = " + s
	}
	return s
}
func (a *Assert) irNode()  {}
func (a *Assert) irInstr() {}

// RecordKind is which of the report's two row lists this record fills.
//
// Two kinds and not two nodes, for the reason `ir.Match` is one node over two
// positions: an operand row and a pipeline-stage row are the same operation — bind a rendered source
// text to an observed value — in two POSITIONS. rt says so by building the
// same pair of fields twice: `rt.RecordOperand(&tr, e, v, s)` beside
// `rt.AssertionPipelineStage{Expr: e, Value: v}`.
//
// What differs is the DELIVERY — which slice the row lands in, and whether the
// redundancy rule may drop it — which is the consumer's half of the
// fault/delivery division.
type RecordKind uint8

const (
	// RecordOperand is a `values:` row: one operand observed while the subject
	// was evaluated. Owners: recordOperand, and the whole-subject row a piped
	// `testing.check` appends.
	RecordOperand RecordKind = iota + 1
	// RecordStage is a `pipeline values:` row: one cumulative prefix of a pipe
	// and the value it produced. Owner: pipeStages.
	//
	// The redundancy rule does not apply to it, which is why SuppressRedundant
	// is a predicate rather than a parameter of this kind's constructor.
	RecordStage
)

func (k RecordKind) String() string {
	switch k {
	case RecordOperand:
		return "operand"
	case RecordStage:
		return "stage"
	}
	return "record?"
}

// Record binds one rendered source text to one observed value, for the report.
type Record struct {
	pos      Pos
	val      Temp
	kind     RecordKind
	text     string
	suppress bool
}

// NewRecordOperand records val, which reads as text in the source, as a
// `values:` row.
//
// suppressRedundantLiteral asks for the rule "an operand that reads exactly
// like its value explains nothing" — so `assert 1 == 2` does not print
// `1 = 1`. It is a REQUEST and not an answer: the rule is applied by the
// consumer, in `rt.RecordOperand`.
//
// It is false for a PREDICATE's literal arguments — `assert even?(5)` prints
// `5 = 5` because that is the whole content of the row — and for the
// whole-subject row a piped `check` appends. irbuild reaches that answer from
// the callee's declared result.
func NewRecordOperand(pos Pos, val Temp, text string, suppressRedundantLiteral bool) *Record {
	return newRecord(pos, val, RecordOperand, text, suppressRedundantLiteral, "ir.NewRecordOperand")
}

// NewRecordStage records val, the value of the cumulative pipe prefix that
// reads as text, as a `pipeline values:` row.
//
// No suppression parameter: a stage row is kept whatever it renders as. A pipe's first stage is USUALLY redundant by that rule —
// `"Ada Lovelace"` producing `"Ada Lovelace"` — and printing it is the point,
// because a chain that dropped its head would not read as a chain.
func NewRecordStage(pos Pos, val Temp, text string) *Record {
	return newRecord(pos, val, RecordStage, text, false, "ir.NewRecordStage")
}

func newRecord(pos Pos, val Temp, kind RecordKind, text string, suppress bool, who string) *Record {
	requirePos(pos, who)
	if val == NoTemp {
		panic(who + ": a report row shows a value and this one has none")
	}
	if text == "" {
		panic(who + ": a report row shows the source it was written as and this one has none")
	}
	return &Record{pos: pos, val: val, kind: kind, text: text, suppress: suppress}
}

// Kind is which row list this record fills.
func (r *Record) Kind() RecordKind { return r.kind }

// Val is the observed temporary.
func (r *Record) Val() Temp { return r.val }

// Text is how the observed expression was written.
func (r *Record) Text() string { return r.text }

// SuppressRedundant reports whether a consumer may drop this row when its text
// and its rendered value read identically.
//
// This is the one stored predicate in this file, and it earns its place the
// way `Proj.Faults` does: a property one consumer reads that cannot be derived from
// the shape. It is not derivable from the KIND — operand rows carry both
// answers — and it is not derivable from the text, because whether the text
// equals the value is a run-time fact.
//
// Always false for RecordStage. That is not a vacuous field: it varies over
// the population it is about, and the kind that never asks for it is exactly
// the kind whose constructor does not take it.
func (r *Record) SuppressRedundant() bool { return r.suppress }

func (r *Record) Pos() Pos                     { return r.pos }
func (r *Record) Dst() Temp                    { return NoTemp }
func (r *Record) AppendUses(dst []Temp) []Temp { return append(dst, r.val) }
func (r *Record) String() string {
	s := "record " + r.kind.String() + " " + strconv.Quote(r.text) + " " + r.val.String()
	if r.suppress {
		return s + " suppress-redundant"
	}
	return s
}
func (r *Record) irNode()  {}
func (r *Record) irInstr() {}
