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
package format

// Doc is an element of the formatter's intermediate representation.
// A layout engine walks a Doc tree and renders it to a string,
// choosing between flat and broken renderings of each Group to fit
// within a target line width.
type Doc interface{ doc() }

type docNil struct{}
type docText struct{ s string }
type docLine struct{ alt string } // when flat, becomes alt (usually " " or "")
type docHard struct{}             // forced newline — never flattened
type docConcat struct{ a, b Doc }
type docNest struct {
	n int
	d Doc
}
type docGroup struct{ d Doc }
type docIfBroken struct{ flat, broken Doc }
type docLocalBroken struct{ d Doc } // see LocalBroken
type docHidden struct{ d Doc }      // see Hidden
type docWithIndent struct {         // see WithIndent
	f func(indent, width int) Doc
}

func (docNil) doc()         {}
func (docText) doc()        {}
func (docLine) doc()        {}
func (docHard) doc()        {}
func (docConcat) doc()      {}
func (docNest) doc()        {}
func (docGroup) doc()       {}
func (docIfBroken) doc()    {}
func (docLocalBroken) doc() {}
func (docHidden) doc()      {}
func (docWithIndent) doc()  {}

// Nil is the empty document.
func Nil() Doc { return docNil{} }

// Text is a literal string that renders as-is. Should not contain a newline.
func Text(s string) Doc { return docText{s} }

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
