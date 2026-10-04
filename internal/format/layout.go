package format

import "strings"

// Render produces the final string for a Doc, given a max line width.
// The algorithm: for each Group, try rendering it flat (Line -> alt,
// HardLine forces broken). If the flat result fits in the remaining
// width on the current line, commit to flat. Otherwise render broken:
// each Line becomes a real newline + current indent.
//
// After the raw render, trailing spaces on every line are stripped.
// Adjacent HardLines (used to preserve blank lines between statements)
// otherwise leave indent-spaces on the blank line, producing ugly
// trailing whitespace in the output.
func Render(d Doc, width int) string {
	var sb strings.Builder
	render(&sb, d, 0, 0, noFlatGroup, nil, width, false)
	return stripTrailingLineSpaces(sb.String())
}

// noFlatGroup is the sentinel for "no enclosing flat Group has set a
// local-base indent." LocalBroken nodes encountered with this sentinel
// fall back to the current ambient `indent` (which already reflects
// whatever Nest contributions are legitimate from broken ancestors).
const noFlatGroup = -1

// stripTrailingLineSpaces removes spaces that appear immediately before a
// newline or at the very end of the string. Does not touch tabs or other
// whitespace — the formatter only emits spaces for indentation.
func stripTrailingLineSpaces(s string) string {
	if !strings.ContainsRune(s, ' ') {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '\n' {
			continue
		}
		end := i
		for end > start && s[end-1] == ' ' {
			end--
		}
		sb.WriteString(s[start:end])
		sb.WriteByte('\n')
		start = i + 1
	}
	// Final chunk (no trailing newline).
	end := len(s)
	for end > start && s[end-1] == ' ' {
		end--
	}
	sb.WriteString(s[start:end])
	return sb.String()
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
func render(sb *strings.Builder, d Doc, col, indent, localBase int, tail Doc, width int, broken bool) int {
	if d == nil {
		return col
	}
	switch v := d.(type) {
	case docNil:
		return col
	case docText:
		sb.WriteString(v.s)
		return col + len(v.s)
	case docLine:
		if broken {
			sb.WriteByte('\n')
			for i := 0; i < indent; i++ {
				sb.WriteByte(' ')
			}
			return indent
		}
		sb.WriteString(v.alt)
		return col + len(v.alt)
	case docHard:
		sb.WriteByte('\n')
		for i := 0; i < indent; i++ {
			sb.WriteByte(' ')
		}
		return indent
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
	case docHidden:
		return false
	case docWithIndent:
		return endsAtLineBreak(v.f(0, unboundedWidth))
	}
	return false
}
