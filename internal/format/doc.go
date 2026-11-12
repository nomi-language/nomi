// Package format implements the Nomi code formatter.
//
// The package is named "format" rather than "fmt" to avoid shadowing the
// Go standard library. This mirrors Go's own go/format package.
//
// The formatter works by walking a Nomi AST, emitting a tree of Doc values
// that represents the intended layout, and then rendering that tree via a
// Wadler/Leijen pretty-printer (see layout.go). The emitter and layout
// algorithm are decoupled: the emitter decides what pieces appear; the
// layout engine decides where to break lines to fit a width budget.
//
// What `nomi fmt` does for a user is in docs/tooling.md, "Formatting". The
// sections below say why some of its rules are built the way they are.
//
// # Tests that guard the formatter
//
// TestFormatKeepsMeaning and FuzzFormatKeepsMeaning (vmhost,
// format_meaning_test.go) hold every format to the program's meaning and
// to a second format changing nothing. TestKnownFormatGaps pins each known
// break in knownFormatGaps (vmhost/format_gaps_test.go).
// TestTrackedNomiFilesAreFormatted fails for every tracked .nomi file
// `nomi fmt` would change, outside its unformattedAllowed list, and
// TestDocExamplesAreFormatted (vmhost) does the same for the examples in
// the tour, docs/ and std's doc comments.
//
// # Brace-bounded field constructs
//
// Anonymous struct types, struct-shaped variant payloads and struct literals
// share one surface syntax, so they share one width rule. The break decision
// is made at the braces, not at the enclosing list, so a long parameter
// annotation breaks its own anonymous struct rather than the parameter list,
// and `Map<String, List<{...}>>` keeps the outer containers inline. The
// exception is an anonymous struct in a function type's parameter list
// (`({...}) -> R`): the whole signature including `-> R` is fit-checked as
// one unit, so a too-long signature breaks at the parameter list and the
// struct sits flat at the inner indent.
//
// Fits are tail-aware: each group's flat-or-broken decision counts the rest
// of the line after it, up to the first place it may break and the `{` of a
// construct kept multi-line, so `A{a} + B{` measures the same whether
// `B{...}` was broken for width or kept broken from the source. Trailing
// comments are invisible to fits (Hidden).
//
// A type keeps a multi-line shape when its fields span lines, as a `fn`
// body does (see "Fixed layouts"), so typing a brace, pressing enter and
// saving does not collapse the new layout. Literals use Prettier's rule for
// objects instead (see "A line break after a literal's opening bracket").
//
// The kept shape is local (LocalBroken), so a call can hug it
// (`f(Point{`). The exception is an argument or parameter list already
// written one element per line, its first element below its opening
// bracket: it stays that way when it holds an element that breaks whatever
// the width. Otherwise a list broken for width would hug its literal on the
// next format, and the layout would not be stable. The same holds for an
// or-pattern written one alternative per line, the short form of a
// one-statement block (`if`, `else`), and a case arm's body below its `->`.
//
// # Fixed layouts
//
// Some layouts do not depend on width or on how the source was written:
//
//   - A body is written below its header and closed by `end`
//     (emitDocsBlockForceBroken): `fn add(x: Int, y: Int) -> Int`, then
//     `x + y` on its own line, then `end`. An empty body is ` end` on the
//     header's line: `fn noop() end`, `impl Marker for P end`. This holds
//     for functions, declarations' members, `begin` blocks and every body
//     a header opens; an `if` keeps its inline form when it fits (see
//     "One-line if and binding else").
//   - A blank line above a body's first statement goes, as none could
//     follow a `{`; a `//!` prompt's body keeps its own.
//   - A block of several statements puts each on its own line:
//     `a = 1; b = 2` becomes two lines.
//   - A tail `return value` becomes `value` (stripTailReturns).
//   - A spread of a range in a literal is written in parentheses,
//     `[0, ..(1..4)]`, so the two `..` do not run together.
//   - A parameter pattern whose head names its type loses a redundant
//     annotation: `fn raw(Duration(x): Duration)` becomes
//     `fn raw(Duration(x))` (stripSelfTypingAnnotations). A head with
//     nothing after it keeps its annotation, since `fn f(Label)` is another
//     parameter: one written as its type alone, which the parser gives
//     [ast.Param].TypeOnly (name `_`, the head as its annotation) and the
//     formatter writes back as the type alone.
//   - A `#!` line is kept as written, without trailing whitespace. A blank
//     line after it stays one blank line, and no blank line stays none.
//
// # One-line if and binding else
//
// The inline forms are `if c then a else b` and `Pattern = e else value`.
// A one-line guard or binding `else` is limited only by the 100-column line,
// its indentation and the rest of the line, so a run of guards reads as a
// run of one-line statements. A plain two-branch `if`/`else` also has a
// 50-column limit (singleLineValueIfMaxWidth), like rustfmt's
// single_line_if_else_max_width. A form holding a forced break (a `case`,
// a block of several statements, a stacked pipe) is never one line, and
// neither is an `else if` chain. An inline `if`'s `else` branch extends as
// far right as it can, so an inline `if` that a binary operator or a pipe
// stage follows goes in parentheses (emitIfParens), and an `if` stage that
// another stage follows is written in block form.
//
// # Case arms
//
// A `case` always breaks, one arm per line, and its arms share one layout
// (emitCaseArms). When every arm fits as `<pattern> -> <body>` and no body
// is a block or a triple-quoted literal, the arms sit on consecutive lines.
// When any arm needs more than one line (a block body, a triple-quoted
// body, a body holding a forced break such as a stacked pipeline, a body too
// wide, a jump carrying its own comment), every arm moves its body below its
// `->`, with a blank line between every two arms. A comment between the last
// arm and the `end` then sits at the bodies' indent, since the parser reads
// the lines below an `->` up to the `end` as that arm's body. A binding
// `else` with arms uses the same layout. This is intentional: the arms of
// one `case` always read as one table, so a reader never meets a short arm
// laid out differently from its neighbours. Do not make arms break one at a
// time.
//
// # A line break after a literal's opening bracket
//
// A literal broken after its opening bracket stays broken. This is
// Prettier's rule for objects: the author joins the literal again by
// deleting one line break. The parser records the opening bracket's line on
// each literal, and the formatter compares it with its first element's
// line, so the rule needs no flag. The broken form puts the first element
// below the bracket, so the next format finds the same break. Every bracket
// and brace literal follows it, a list with a spread among them. A pattern
// with `..` and every other bracketed form (call arguments, parameter
// lists, type argument lists, pipes) are laid out by width.
//
// A lambda body below its `->` follows the same rule: the parser gives a body
// closed by `end` an EndLine, and a one-line body none, so a body the
// formatter moves below the parameters stays there on later runs, even
// after it shrinks to fit. A body that does not fit goes below the
// parameters, closed by `end`, as rustfmt writes a closure in a block,
// wherever the lambda stands.
//
// # Filled literal lists
//
// Only lists of number literals (alone, after a unary `-`, or in
// parentheses the formatter drops) are filled (filledList, fillTexts).
// Strings, codepoints and booleans are not, since such a list is usually
// read one element at a time, as messages or the rows of a grid. Packing is
// greedy (packGreedy), in display columns, with no column alignment, and is
// decided when the list renders from the indent its elements start at, so
// it depends only on the syntax tree, the list's position and whether the
// source breaks after the opening bracket. Derive and impl conformance
// lists use the same greedy packing, against the fixed conformanceWrapWidth
// rather than the indent they render at.
//
// # Comparison and boolean operators
//
// A comparison, `and` or `or` gets a group that breaks before the operator,
// the right operand one indent in. The renderer breaks the outermost group
// first, so a long comparison breaks before its operator before it breaks
// inside a call on either side. A chain of one `and` or `or` is one group,
// so a broken chain puts each operator on its own line. No group is made
// when either operand already spans lines, when the left operand is a
// pipeline (the operator follows its last stage), or when the right operand
// is a bracketed literal, which breaks inside its brackets.
//
// # Compound depth
//
// Each list, vector, set, map or struct literal or pattern adds a level
// (compoundDepth), except an unnamed list literal with a spread
// (`[head, ..tail]`): cons-shaped and conventionally short, it adds none and
// passes none through, so `Zipper{focus: f, path: [.Up{v:}, ..path]}` stays
// on one line when it fits. A call, an enum pattern's payload, a tuple, an `as` or
// `or` pattern and parentheses add none but pass their contents' depth
// through. Only the literal or pattern that reaches maxCompoundDepth (three)
// levels breaks for it; its elements are laid out on their own merits, so a
// nested literal one level down can stay on one line inside it.
//
// # Comments
//
// The parser attaches a comment inside a bracketed, comma-separated list to
// one of its items as it reads the list, so every list form keeps comments
// the same way. A list holding a comment is written one item per line, with
// no trailing comma where its syntax takes none (a lambda's parameters, type
// arguments and parameters, a tuple or variant pattern, a list ending in a
// spread, a call with one argument). Parentheses around an expression
// holding a comment stay, with the comment inside. A lambda header whose
// only comments each end the line of a parameter other than the last keeps
// its layout.
//
// Most other comments are trivia on their construct: a statement's leading
// and trailing comments, the comments before a closing `}` or `end`, a struct
// field's leading comments. A comment with no slot of its own (after a
// `with` statement's `=`, after a spread's `..`, inside a pattern's
// parentheses) goes to the nearest node: inside the innermost node
// containing it, after the last node ending before it, else before the
// first node starting after it ([ast.TriviaCarrier]'s DanglingBefore and
// DanglingAfter). A comment before a node is written on the line above when
// the node starts a line, else at the end of the line (CommentBefore). A
// comment that cannot end its line, because the line already ends in a
// comment, an opening bracket or `|`, goes on its own line above the next
// line of code, where it reads back as the next item's comment.
package format

// Doc is an element of the formatter's intermediate representation.
// A layout engine walks a Doc tree and renders it to a string,
// choosing between flat and broken renderings of each Group to fit
// within a target line width.
type Doc interface{ doc() }

type docNil struct{}
type docText struct {
	s string
	// keep marks text whose trailing spaces are the program's, a line of
	// a multi-line string; see Verbatim.
	keep bool
}
type docLine struct{ alt string } // when flat, becomes alt (usually " " or "")
type docHard struct{}             // forced newline — never flattened
type docConcat struct{ a, b Doc }
type docNest struct {
	n int
	d Doc
}
type docGroup struct{ d Doc }
type docIfBroken struct{ flat, broken Doc }
type docLocalBroken struct { // see LocalBroken and LocalBrokenBlock
	d Doc
	// firstLine says ancestor fits checks measure d up to its first
	// HardLine.
	firstLine bool
}
type docHidden struct{ d Doc } // see Hidden
type docWithIndent struct {    // see WithIndent
	f func(indent, width int) Doc
}
type docWithColumn struct { // see WithColumn
	f func(col, indent, width int) Doc
}
type docAtLineIndent struct { // see AtLineIndent
	f func(startsLine bool) Doc
}
type docLineSuffix struct { // see LineSuffix and CommentBefore
	s      string
	before bool
}
type docInString struct{ d Doc } // see InString

// docBrokenTail is the part of a broken Group that follows a nested
// document on its line, as render passes it to that document's fits checks:
// an IfBroken in it takes its broken branch, which is what renders. A nested
// Group in it decides for itself, so its IfBroken stay flat. It appears only
// in a tail and is never rendered.
type docBrokenTail struct{ d Doc }

func (docNil) doc()          {}
func (docText) doc()         {}
func (docLine) doc()         {}
func (docHard) doc()         {}
func (docConcat) doc()       {}
func (docNest) doc()         {}
func (docGroup) doc()        {}
func (docIfBroken) doc()     {}
func (docLocalBroken) doc()  {}
func (docHidden) doc()       {}
func (docWithIndent) doc()   {}
func (docWithColumn) doc()   {}
func (docAtLineIndent) doc() {}
func (docLineSuffix) doc()   {}
func (docInString) doc()     {}
func (docBrokenTail) doc()   {}

// Nil is the empty document.
func Nil() Doc { return docNil{} }

// Text is a literal string that renders as-is. Should not contain a newline.
func Text(s string) Doc { return docText{s: s} }

// Verbatim is Text whose trailing spaces survive the render: a line of a
// multi-line string's body, where they are part of the value. Render strips
// trailing spaces from every other line.
func Verbatim(s string) Doc { return docText{s: s, keep: true} }

// Line is a line break that collapses to a single space when the enclosing
// group is rendered flat.
func Line() Doc { return docLine{alt: " "} }

// LineOrEmpty is a line break that collapses to the empty string when the
// enclosing group is rendered flat. Used for "no space between args on flat,
// newline+indent on broken" — e.g. after `(` in a call.
func LineOrEmpty() Doc { return docLine{alt: ""} }

// HardLine is a forced line break. Cannot be flattened. When a HardLine
// appears inside a group, the group must be rendered broken.
func HardLine() Doc { return docHard{} }

// Concat concatenates documents left-to-right. An empty argument list
// returns Nil; a single argument is returned as-is.
func Concat(ds ...Doc) Doc {
	if len(ds) == 0 {
		return Nil()
	}
	out := ds[0]
	for _, d := range ds[1:] {
		out = docConcat{a: out, b: d}
	}
	return out
}

func Join(sep Doc, ds ...Doc) Doc {
	if len(ds) == 0 {
		return Nil()
	}
	out := ds[0]
	for _, d := range ds[1:] {
		out = Concat(out, sep, d)
	}
	return out
}

// Nest adds n spaces of indent for every newline inside d.
func Nest(n int, d Doc) Doc { return docNest{n: n, d: d} }

// Group marks d as a layout decision point: the renderer tries to render
// d flat first; if it doesn't fit in the remaining width, d is rendered
// broken. Groups nest — an outer group may be broken while an inner one
// stays flat.
func Group(d Doc) Doc { return docGroup{d} }

// IfBroken renders to `flat` when the enclosing Group is rendered flat,
// and to `broken` when the enclosing Group is rendered broken. Useful for
// content that should only appear in one mode — e.g. trailing commas that
// appear only when a call arg list breaks across multiple lines.
//
// Contract: neither branch may contain a HardLine (that would force the
// surrounding Group broken during `fits`, even when the flat-side branch
// would be the one selected).
func IfBroken(flat, broken Doc) Doc { return docIfBroken{flat: flat, broken: broken} }

// LocalBroken renders d in broken mode regardless of the enclosing
// Group's flat-vs-broken state, AND hides d's HardLines from any ancestor
// Group's `fits` check. Used to preserve a user-written multi-line layout
// (e.g. a multi-line struct literal inside a fn-param list) without
// forcing the surrounding Group to break too.
//
// Without this primitive, a `HardLine()` inside an inner Doc bubbles up
// through `fits` and forces every ancestor Group to broken — so a multi-
// line anon-struct annotation would also force the outer param list to
// break, even when the only user intent was "keep the struct multi-line."
//
// LocalBroken is the only Doc that decouples local layout from ancestor
// fits decisions. Use sparingly — by design it lies to ancestors about
// width, so a too-wide LocalBroken header (the part on the `{` line) can
// still spill past the budget.
func LocalBroken(d Doc) Doc { return docLocalBroken{d: d} }

// LocalBrokenBlock is LocalBroken for a block that opens on the line it
// starts on, `case f() {` or `if c {`: ancestor fits checks measure d's
// first line, up to its first HardLine, and hide the rest. A pipeline
// whose `|> case f() {` line is too wide then breaks, while the arms below
// do not make it break.
func LocalBrokenBlock(d Doc) Doc { return docLocalBroken{d: d, firstLine: true} }

// Hidden renders d normally but is invisible to ancestor `fits` /
// `flatWidth` checks (returns true / 0). Used for content that should not
// influence structural break decisions — e.g. a trailing trivia comment
// (`x = 1 // explanatory note`) shouldn't force the formatter to break
// `x = 1` to make room for the comment. The comment can spill past the
// budget; the structural code stays clean.
//
// Unlike LocalBroken, Hidden does NOT change the broken-mode rendering
// state — it simply renders d at the current position with the current
// state.
func Hidden(d Doc) Doc { return docHidden{d: d} }

// WithIndent builds a document at render time from the indent and line width
// in effect where it renders. It is for a layout decision that spans several
// lines, each measured on its own from that indent, which a Group cannot make:
// a Group measures its whole content as one line. When an ancestor Group asks
// whether this document fits flat, it is built at indent 0.
func WithIndent(f func(indent, width int) Doc) Doc { return docWithIndent{f: f} }

// WithColumn builds a document from the column it starts at, the current
// indent and the line width, when it renders. An ancestor fits check measures the document f
// builds at column 0 with an unbounded width, so f must answer one whose
// flat text is the same at every column.
func WithColumn(f func(col, indent, width int) Doc) Doc { return docWithColumn{f: f} }

// AtLineIndent builds a document from whether it starts its line (only
// indentation precedes it) and renders it with the nest indent set to the
// indentation of that line, whatever Nests enclose it. A multi-line
// string's body takes its indent from the line its opening delimiter is on
// this way. An ancestor fits check measures f(true); f must answer
// documents whose flat text is the same either way.
func AtLineIndent(f func(startsLine bool) Doc) Doc { return docAtLineIndent{f: f} }

// LineSuffix is a line comment written at the end of the line it renders on,
// just before the next line break, after whatever else the line holds. It is
// how a comment the tree keeps no trivia for (ast.TriviaCarrier's
// DanglingBefore and DanglingAfter) comes back: whatever follows it on the
// line is code it must not swallow. When the line already ends in a comment,
// it goes on a line of its own below. It is invisible to fits, as a trailing
// comment is (Hidden).
func LineSuffix(comment string) Doc { return docLineSuffix{s: comment} }

// CommentBefore is a line comment that belongs before the document after it.
// Where that document starts a line, the comment takes the line above it;
// elsewhere it is a LineSuffix.
func CommentBefore(comment string) Doc { return docLineSuffix{s: comment, before: true} }

// InString marks d as a multi-line string's body and closing delimiter. Its
// line breaks are the string's own, so a pending LineSuffix waits until the
// first line break after it.
func InString(d Doc) Doc { return docInString{d: d} }
