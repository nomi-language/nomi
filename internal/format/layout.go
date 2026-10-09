package format

import (
	"bytes"
	"strings"
)

// Render produces the final string for a Doc, given a max line width.
// The algorithm: for each Group, try rendering it flat (Line -> alt,
// HardLine forces broken). If the flat result fits in the remaining
// width on the current line, commit to flat. Otherwise render broken:
// each Line becomes a real newline + current indent.
//
// Trailing spaces on every line are stripped, except those of Verbatim
// text. Adjacent HardLines (used to preserve blank lines between statements)
// otherwise leave indent-spaces on the blank line, producing ugly trailing
// whitespace in the output.
func Render(d Doc, width int) string {
	var out renderOut
	render(&out, d, 0, 0, noFlatGroup, nil, width, false)
	return out.String()
}

// noFlatGroup is the sentinel for "no enclosing flat Group has set a
// local-base indent." LocalBroken nodes encountered with this sentinel
// fall back to the current ambient `indent` (which already reflects
// whatever Nest contributions are legitimate from broken ancestors).
const noFlatGroup = -1

// renderOut is the text a render writes. Spaces before a line break, or at
// the end, are dropped as the break is written, back to the end of the last
// Verbatim text: those are part of a string's value.
type renderOut struct {
	buf  bytes.Buffer
	kept int
	// suffix holds the LineSuffix comments waiting for the end of the
	// line, and lineComment says the line already ends in a comment.
	suffix      []string
	lineComment bool
	// ownLine holds the comments that could not end their line, waiting
	// for the next line with content, and ownLineIndent the indent of the
	// line they could not end.
	ownLine       []string
	ownLineIndent int
	// inString counts the InString documents being rendered.
	inString int
}

func (o *renderOut) WriteString(s string) { o.buf.WriteString(s) }

func (o *renderOut) WriteByte(c byte) error {
	if c == '\n' {
		o.trim()
		o.lineComment = false
	}
	return o.buf.WriteByte(c)
}

// text writes code or comment text, noting a comment.
func (o *renderOut) text(s string) {
	if strings.TrimLeft(s, " ") != "" {
		o.placeOwnLine()
	}
	if strings.HasPrefix(strings.TrimLeft(s, " "), "//") {
		o.lineComment = true
	}
	o.buf.WriteString(s)
}

// lineBreak writes a line break and indent spaces, first writing the
// pending LineSuffix comments at the end of the line being ended.
func (o *renderOut) lineBreak(indent int) {
	if o.inString == 0 {
		o.flushSuffix()
	}
	o.WriteByte('\n')
	for i := 0; i < indent; i++ {
		o.WriteByte(' ')
	}
}

// flushSuffix writes the pending LineSuffix comments at the end of the line
// being ended. A comment cannot end a line that already ends in one, nor a
// line that ends in an opening bracket, whose comment reads back as the
// first item's leading comment: it waits for the next line with content
// and goes on a line of its own above it (placeOwnLine). Nor can it end a
// line that ends in a case arm's `->` (insertAboveLine).
func (o *renderOut) flushSuffix() {
	for _, c := range o.suffix {
		o.trim()
		if bytes.HasSuffix(o.buf.Bytes(), []byte("->")) {
			o.insertAboveLine(c)
			continue
		}
		if o.lineComment || o.endsInOpener() {
			if len(o.ownLine) == 0 {
				o.ownLineIndent = o.lineIndent()
			}
			o.ownLine = append(o.ownLine, c)
			continue
		}
		o.buf.WriteByte(' ')
		o.buf.WriteString(c)
		o.lineComment = true
	}
	o.suffix = nil
}

// insertAboveLine writes comment on a line of its own above the line being
// written, at its indent. A case arm's `->` takes no comment after it, on
// its line or the next, so one that would end the arm's first line goes
// above the arm, where it reads back as the arm's leading comment.
func (o *renderOut) insertAboveLine(comment string) {
	b := o.buf.Bytes()
	start := bytes.LastIndexByte(b, '\n') + 1
	line := strings.Repeat(" ", o.lineIndent()) + comment + "\n"
	rest := string(b[start:])
	o.buf.Truncate(start)
	o.buf.WriteString(line)
	o.buf.WriteString(rest)
	if o.kept > start {
		o.kept += len(line)
	}
}

// placeOwnLine writes the comments waiting for a line of their own above
// the line about to get content, at that line's indent or the indent of the
// line they could not end, whichever is deeper: a comment that could not
// end the last line of a body stays in the body.
func (o *renderOut) placeOwnLine() {
	if len(o.ownLine) == 0 || !o.atLineStart() {
		return
	}
	indent := o.lineIndent()
	at := max(indent, o.ownLineIndent)
	o.trim()
	for _, c := range o.ownLine {
		o.buf.WriteString(strings.Repeat(" ", at))
		o.buf.WriteString(c)
		o.buf.WriteByte('\n')
	}
	o.buf.WriteString(strings.Repeat(" ", indent))
	o.ownLine = nil
	o.lineComment = false
}

// lineIndent is the indentation of the line being written.
func (o *renderOut) lineIndent() int {
	b := o.buf.Bytes()
	start := bytes.LastIndexByte(b, '\n') + 1
	n := 0
	for start+n < len(b) && b[start+n] == ' ' {
		n++
	}
	return n
}

// atLineStart reports whether the line being written holds nothing but
// indentation.
func (o *renderOut) atLineStart() bool {
	b := o.buf.Bytes()
	for i := len(b) - 1; i >= 0; i-- {
		switch b[i] {
		case ' ':
			continue
		case '\n':
			return true
		}
		return false
	}
	return true
}

// endsInOpener reports whether the text written so far ends in `{`, `(` or
// `[`.
func (o *renderOut) endsInOpener() bool {
	b := o.buf.Bytes()
	if len(b) == 0 {
		return false
	}
	switch b[len(b)-1] {
	case '{', '(', '[':
		return true
	}
	return false
}

// verbatim writes s and keeps its trailing spaces.
func (o *renderOut) verbatim(s string) {
	o.buf.WriteString(s)
	o.kept = o.buf.Len()
}

func (o *renderOut) trim() {
	b := o.buf.Bytes()
	end := len(b)
	for end > o.kept && b[end-1] == ' ' {
		end--
	}
	o.buf.Truncate(end)
}

func (o *renderOut) String() string {
	o.flushSuffix()
	if len(o.ownLine) > 0 {
		o.trim()
		for _, c := range o.ownLine {
			o.buf.WriteByte('\n')
			o.buf.WriteString(strings.Repeat(" ", o.ownLineIndent))
			o.buf.WriteString(c)
		}
		o.ownLine = nil
	}
	o.trim()
	return o.buf.String()
}

// render walks the doc tree, tracking:
//
//	col       = current column (0 = start of line)
//	indent    = current nest indent (sum of all enclosing Nests)
//	localBase = the indent at the most recent Group entry, used by
//	            LocalBroken to bypass any Nest contributions from
//	            flat-decided ancestor Groups (which only exist for the
//	            broken-layout that didn't happen). For LocalBroken inside
//	            a broken Group this still works because the Group's own
//	            break also passes localBase = indent at entry, then the
//	            Nest inside the broken Group adjusts indent normally.
//	tail      = the Doc that follows d on the current line. Used by
//	            docGroup's fits decision so a Group whose flat content
//	            fits in the budget but whose trailing siblings push the
//	            line past width still breaks. nil at the document root.
//	width     = line width budget
//	broken    = true if the enclosing group has committed to broken mode
//
// Returns the new col after rendering d.
func render(sb *renderOut, d Doc, col, indent, localBase int, tail Doc, width int, broken bool) int {
	if d == nil {
		return col
	}
	switch v := d.(type) {
	case docNil:
		return col
	case docText:
		if v.keep {
			sb.verbatim(v.s)
		} else {
			sb.text(v.s)
		}
		return col + len(v.s)
	case docLine:
		if broken {
			sb.lineBreak(indent)
			return indent
		}
		sb.WriteString(v.alt)
		return col + len(v.alt)
	case docHard:
		sb.lineBreak(indent)
		return indent
	case docLineSuffix:
		if v.before && sb.inString == 0 && sb.atLineStart() {
			sb.text(v.s)
			sb.lineBreak(col)
			return col
		}
		sb.suffix = append(sb.suffix, v.s)
		return col
	case docInString:
		sb.inString++
		col = render(sb, v.d, col, indent, localBase, tail, width, broken)
		sb.inString--
		return col
	case docConcat:
		// Render a with tail = Concat(b, original tail). Render b with the
		// original tail. This is what threads "rest of line" down to inner
		// Groups so their fits decisions account for trailing siblings.
		col = render(sb, v.a, col, indent, localBase, docConcat{a: v.b, b: tail}, width, broken)
		return render(sb, v.b, col, indent, localBase, tail, width, broken)
	case docNest:
		return render(sb, v.d, col, indent+v.n, localBase, tail, width, broken)
	case docGroup:
		// A Group decides flat vs broken independently based on whether it
		// fits at the current column — it does NOT inherit the enclosing
		// group's broken state. This is standard Wadler semantics: broken
		// state applies to Lines within the same Group, not to nested Groups.
		//
		// Tail is included in the fits check so a Group whose own content
		// fits but whose trailing siblings would overflow still breaks.
		// Without tail, `({long anon}) -> Result<...>` would render the
		// param-list flat (anon-content fits at col) and then spill the
		// trailing `-> Result<...>` past width.
		//
		// Set localBase per branch:
		//   flat:   localBase = indent (Group entry indent). LocalBroken
		//           inside should bypass any Nest contributions made by
		//           this flat Group (those Nests only mattered for the
		//           broken layout that didn't happen).
		//   broken: localBase = noFlatGroup. The Group's Nest contributions
		//           are real now, so LocalBroken inside should respect
		//           them — the noFlatGroup sentinel makes LocalBroken fall
		//           back to the current ambient indent.
		if fits(v.d, tail, width-col) {
			return render(sb, v.d, col, indent, indent, tail, width, false)
		}
		return render(sb, v.d, col, indent, noFlatGroup, tail, width, true)
	case docIfBroken:
		// Select branch based on the enclosing Group's mode (carried by
		// `broken`). A top-level docIfBroken (no enclosing Group) is
		// effectively flat.
		if broken {
			return render(sb, v.broken, col, indent, localBase, tail, width, broken)
		}
		return render(sb, v.flat, col, indent, localBase, tail, width, broken)
	case docLocalBroken:
		// Render d in broken mode regardless of the enclosing group's
		// state. If a flat-decided ancestor Group set localBase, use that
		// (bypassing the Nest contributions inside that flat Group, which
		// only existed for the broken layout that didn't happen). Otherwise
		// use the current ambient indent (which already reflects whatever
		// Nest contributions are legitimate from broken ancestors). The
		// hide-from-ancestor-fits behaviour is enforced in fits.
		//
		// Descendants get localBase=noFlatGroup: once we've committed to
		// broken mode here, any *nested* LocalBroken should use the
		// then-current ambient indent (which reflects this LocalBroken's
		// own Nest contributions), not the original flat-Group base.
		// Keeping the original base would pin a nested LocalBroken's
		// children to the outer flat-Group's entry indent and visually
		// "un-nest" deeply chained broken compounds.
		base := localBase
		if base == noFlatGroup {
			base = indent
		}
		return render(sb, v.d, col, base, noFlatGroup, tail, width, true)
	case docHidden:
		// Render d normally; the hide-from-ancestor-fits behaviour is
		// enforced in fits.
		return render(sb, v.d, col, indent, localBase, tail, width, broken)
	case docWithIndent:
		return render(sb, v.f(indent, width), col, indent, localBase, tail, width, broken)
	}
	panic("unknown doc variant")
}

// fits reports whether d can be rendered flat within `remaining` columns,
// considering that `tail` (the rest of the line in the parent context)
// follows it. A docHard anywhere in d forces broken (returns false).
// HardLine inside tail is treated as end-of-line: the rest of tail is on
// a future line and doesn't count against our budget.
//
// `tail` is the key bit that lets nested Groups make accurate decisions:
// without it, a Group whose flat content fits at its column but whose
// trailing siblings push the line past width would still go flat (and
// then spill). With tail, the Group sees the full picture and breaks.
func fits(d Doc, tail Doc, remaining int) bool {
	if remaining < 0 {
		return false
	}
	rem := remaining
	if !fitsWalk(d, &rem, false) {
		return false
	}
	if tail == nil {
		return true
	}
	return fitsWalk(tail, &rem, true)
}

// fitsWalk decrements *rem by the flat width of d, returning false if
// anything overflows. When `inTail` is true, encountering any line break
// (HardLine OR a docLine that could break) is treated as end-of-line:
// the rest of d is on a future line and doesn't count. When inTail is
// false (walking the Group's own flat content), HardLine returns false
// (forces broken) and docLine contributes its alt width.
func fitsWalk(d Doc, rem *int, inTail bool) bool {
	if d == nil {
		// Nil Doc behaves like docNil — important because render constructs
		// `docConcat{a: v.b, b: tail}` for tail propagation and tail can
		// itself be nil at the document root.
		return true
	}
	if *rem < 0 {
		return false
	}
	switch v := d.(type) {
	case docNil:
		return true
	case docText:
		*rem -= len(v.s)
		return *rem >= 0
	case docLine:
		if inTail {
			return true
		}
		*rem -= len(v.alt)
		return *rem >= 0
	case docHard:
		return inTail
	case docConcat:
		if !fitsWalk(v.a, rem, inTail) {
			return false
		}
		// If `a` ended at a line break (HardLine OR docLine inTail), the
		// rest of the Concat is on a future line — short-circuit.
		if inTail && endsAtLineBreak(v.a) {
			return true
		}
		return fitsWalk(v.b, rem, inTail)
	case docNest:
		return fitsWalk(v.d, rem, inTail)
	case docGroup:
		// Nested Group: assume flat. fitsWalk recurses into its content.
		return fitsWalk(v.d, rem, inTail)
	case docIfBroken:
		// When probing flat fit, the enclosing group is (tentatively) flat,
		// so the flat branch is what would render.
		return fitsWalk(v.flat, rem, inTail)
	case docLocalBroken:
		// Hide from ancestor fits: claim to fit, contribute 0 width. Local
		// breaking is the whole point — ancestors should not be forced
		// broken on our behalf.
		return true
	case docHidden:
		// Hide from ancestor fits: claim to fit, contribute 0 width.
		return true
	case docLineSuffix:
		// A comment, like a Hidden trailing one, does not count.
		return true
	case docInString:
		return fitsWalk(v.d, rem, inTail)
	case docWithIndent:
		return fitsWalk(v.f(0, unboundedWidth), rem, inTail)
	}
	return false
}

// unboundedWidth is the width a WithIndent document is built at when an
// ancestor's fits check asks for its flat width.
const unboundedWidth = 1 << 30

// endsAtLineBreak reports whether d's flat traversal ends at (i.e.
// contains) a HardLine or docLine. Used by tail-walk to short-circuit
// Concat traversal once a line break has been seen — anything after it
// is on a future line. LocalBroken contributes no line break to the
// flat view (per its hide-from-ancestor semantics).
func endsAtLineBreak(d Doc) bool {
	if d == nil {
		return false
	}
	switch v := d.(type) {
	case docNil, docText:
		return false
	case docLine, docHard:
		return true
	case docConcat:
		return endsAtLineBreak(v.a) || endsAtLineBreak(v.b)
	case docNest:
		return endsAtLineBreak(v.d)
	case docGroup:
		return endsAtLineBreak(v.d)
	case docIfBroken:
		return endsAtLineBreak(v.flat)
	case docLocalBroken:
		return false
	case docHidden, docLineSuffix:
		return false
	case docInString:
		return endsAtLineBreak(v.d)
	case docWithIndent:
		return endsAtLineBreak(v.f(0, unboundedWidth))
	}
	return false
}
