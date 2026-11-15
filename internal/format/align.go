package format

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/nomi-language/nomi/internal/ast"
)

// Vertical alignment (package doc, "Alignment"). The emitter marks the
// places a construct aligns at, and alignLines pads them once the layout has
// chosen every line break, so alignment never changes where a line breaks.
//
// A mark is text the renderer writes but does not measure: markOpen, the
// rule's kind byte, the block and item numbers, the slot, markClose. The two
// bytes never occur in UTF-8; a comment that is not UTF-8 may hold them, and
// the renderer escapes them there (escapeMarks).
const (
	markOpen  = '\xff'
	markClose = '\xfe'
)

// alignKind is the rule a block aligns by.
type alignKind byte

const (
	alignArms     alignKind = 'a' // a case's arms, at `->`
	alignFields   alignKind = 'f' // a struct declaration's fields, their types
	alignValues   alignKind = 'v' // fields, named arguments and parameters, after `name:`
	alignMap      alignKind = 'm' // a map literal's entries, at `=>`
	alignRows     alignKind = 'r' // rows of tuples or calls, each column
	alignOnce     alignKind = 'o' // `once` declarations, at `=`
	alignComments alignKind = 'c' // trailing comments
)

// alignSmall and alignRatio are gofmt's rule for where alignment breaks
// (go/printer's exprList). Two consecutive items whose left parts, measured
// from the run's indent, are both at most alignSmall wide stay in one
// section. Otherwise an item starts a new section when its left part is
// alignRatio times wider or narrower than the geometric mean of the
// section's left parts so far. Each section aligns on its own, so one
// outlier leaves the lines around it aligned. Rows have no limit.
const (
	alignSmall = 40
	alignRatio = 2.5
)

// alignSections splits a run's left-part sizes into sections by alignSmall
// and alignRatio, as [start, end) ranges.
func alignSections(sizes []int) [][2]int {
	var out [][2]int
	start := 0
	log2sum, count := 0.0, 0
	for i, size := range sizes {
		if i > 0 {
			prev := sizes[i-1]
			split := false
			if count > 0 && (prev > alignSmall || size > alignSmall) {
				geomean := math.Exp2(log2sum / float64(count))
				ratio := float64(max(size, 1)) / geomean
				split = alignRatio*ratio <= 1 || alignRatio <= ratio
			}
			if split {
				out = append(out, [2]int{start, i})
				start = i
				log2sum, count = 0, 0
			}
		}
		log2sum += math.Log2(float64(max(size, 1)))
		count++
	}
	return append(out, [2]int{start, len(sizes)})
}

// commentMark is the mark the renderer writes before a comment that ends a
// line of code.
const commentMark = "\xffc\xfe"

var alignCounter atomic.Uint64

// alignBlock is one construct whose items align with each other. The zero
// alignBlock aligns nothing.
type alignBlock struct {
	kind alignKind
	id   uint64
}

func newAlignBlock(kind alignKind) alignBlock {
	return alignBlock{kind: kind, id: alignCounter.Add(1)}
}

// onceBlock is the block every `once` declaration belongs to: consecutive
// ones at one indent align, whichever declarations hold them.
var onceBlock = alignBlock{kind: alignOnce}

// alignItem is one item of a block: an arm, a field, a row.
type alignItem struct {
	b  alignBlock
	id uint64
}

func (b alignBlock) item() alignItem {
	if b.kind == 0 {
		return alignItem{}
	}
	return alignItem{b: b, id: alignCounter.Add(1)}
}

func (it alignItem) mark(slot string) Doc {
	if it.b.kind == 0 {
		return Nil()
	}
	return docMark{s: it.markText(slot)}
}

func (it alignItem) markText(slot string) string {
	return "\xff" + string(rune(it.b.kind)) + strconv.FormatUint(it.b.id, 10) + "." + strconv.FormatUint(it.id, 10) + "." + slot + "\xfe"
}

// wrap marks d as the item, from its start to its end. An item aligns only
// when both are on one line.
func (it alignItem) wrap(d Doc) Doc {
	if it.b.kind == 0 {
		return d
	}
	return Concat(it.mark("s"), d, it.mark("e"))
}

// col marks the place column i of the item starts: what follows is padded
// to the block's widest left part.
func (it alignItem) col(i int) Doc { return it.mark(strconv.Itoa(i)) }

type markedItem struct {
	kind         alignKind
	block, id    uint64
	start, end   int
	cols         map[int]int
	ncols        int
	inconsistent bool
}

// alignLines pads the marked places in s, rendered at width, and removes
// the marks.
func alignLines(s string, width int) string {
	if strings.IndexByte(s, markOpen) < 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	items := map[uint64]*markedItem{}
	for li, line := range lines {
		for i := 0; i < len(line); i++ {
			if line[i] != markOpen {
				continue
			}
			j := strings.IndexByte(line[i:], markClose)
			if j < 0 {
				break
			}
			body := line[i+1 : i+j]
			i += j
			if len(body) < 2 || alignKind(body[0]) == alignComments {
				continue
			}
			f := strings.Split(body[1:], ".")
			if len(f) != 3 {
				continue
			}
			block, _ := strconv.ParseUint(f[0], 10, 64)
			id, _ := strconv.ParseUint(f[1], 10, 64)
			it := items[id]
			if it == nil {
				it = &markedItem{kind: alignKind(body[0]), block: block, id: id, start: -1, end: -1, cols: map[int]int{}}
				items[id] = it
			}
			switch f[2] {
			case "s":
				it.start = li
			case "e":
				it.end = li
			default:
				c, _ := strconv.Atoi(f[2])
				if _, dup := it.cols[c]; dup {
					it.inconsistent = true
				}
				it.cols[c] = li
				if c+1 > it.ncols {
					it.ncols = c + 1
				}
			}
		}
	}
	type blockKey struct {
		kind  alignKind
		block uint64
	}
	blocks := map[blockKey][]*markedItem{}
	var keys []blockKey
	for _, it := range items {
		k := blockKey{it.kind, it.block}
		if blocks[k] == nil {
			keys = append(keys, k)
		}
		blocks[k] = append(blocks[k], it)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].kind != keys[j].kind {
			return keys[i].kind < keys[j].kind
		}
		return keys[i].block < keys[j].block
	})
	for _, k := range keys {
		its := blocks[k]
		sort.Slice(its, func(i, j int) bool { return its[i].start < its[j].start })
		var run []*markedItem
		flush := func() {
			if len(run) >= 2 {
				alignRun(lines, run, width)
			}
			run = nil
		}
		for _, it := range its {
			if !it.oneLine() {
				flush()
				continue
			}
			if len(run) > 0 {
				prev := run[len(run)-1]
				if it.start != prev.start+1 || lineIndent(lines[it.start]) != lineIndent(lines[prev.start]) || it.ncols != prev.ncols {
					flush()
				}
			}
			run = append(run, it)
		}
		flush()
	}
	alignCommentLines(lines, width)
	for i, line := range lines {
		lines[i] = stripMarks(line)
	}
	return strings.Join(lines, "\n")
}

// oneLine reports whether the item can align: it starts and ends on one
// line, which holds each of its columns, and it has a column.
func (it *markedItem) oneLine() bool {
	if it.inconsistent || it.start < 0 || it.start != it.end || it.ncols == 0 {
		return false
	}
	for c := 0; c < it.ncols; c++ {
		l, ok := it.cols[c]
		if !ok || l != it.start {
			return false
		}
	}
	return true
}

// alignRun aligns one run of a block: rows as a whole, any other kind in
// the sections alignSections splits it into by its first column's left
// parts.
func alignRun(lines []string, run []*markedItem, width int) {
	if run[0].kind == alignRows {
		alignSection(lines, run, width)
		return
	}
	indent := lineIndent(lines[run[0].start])
	sizes := make([]int, len(run))
	for i, it := range run {
		line := lines[it.start]
		off := strings.Index(line, it.colMark(0))
		if off < 0 {
			return
		}
		sizes[i] = textWidth(stripMarks(line[:off])) - indent
	}
	for _, sec := range alignSections(sizes) {
		if sec[1]-sec[0] >= 2 {
			alignSection(lines, run[sec[0]:sec[1]], width)
		}
	}
}

// alignSection pads the lines of one section, column by column, unless
// padding would take a line past width.
func alignSection(lines []string, run []*markedItem, width int) {
	padded := make([]string, len(run))
	for i, it := range run {
		padded[i] = lines[it.start]
	}
	for c := 0; c < run[0].ncols; c++ {
		cols := make([]int, len(run))
		offs := make([]int, len(run))
		for i, line := range padded {
			off := strings.Index(line, run[i].colMark(c))
			if off < 0 {
				return
			}
			offs[i] = off
			cols[i] = textWidth(stripMarks(line[:off]))
		}
		hi := cols[0]
		for _, c := range cols {
			hi = max(hi, c)
		}
		for i, line := range padded {
			if pad := hi - cols[i]; pad > 0 {
				padded[i] = line[:offs[i]] + strings.Repeat(" ", pad) + line[offs[i]:]
			}
		}
	}
	for i, line := range padded {
		if line != lines[run[i].start] && codeWidth(line) > width {
			return
		}
	}
	for i, it := range run {
		lines[it.start] = padded[i]
	}
}

// colMark is the text of the item's mark for column c.
func (it *markedItem) colMark(c int) string {
	return alignItem{b: alignBlock{kind: it.kind, id: it.block}, id: it.id}.markText(strconv.Itoa(c))
}

// alignCommentLines aligns the comments that end consecutive lines of code at
// one indent.
func alignCommentLines(lines []string, width int) {
	var run []int
	flush := func() {
		if len(run) >= 2 {
			alignCommentRun(lines, run, width)
		}
		run = nil
	}
	for i, line := range lines {
		if !strings.Contains(line, commentMark) {
			flush()
			continue
		}
		if len(run) > 0 && (run[len(run)-1] != i-1 || lineIndent(lines[run[len(run)-1]]) != lineIndent(line)) {
			flush()
		}
		run = append(run, i)
	}
	flush()
}

func alignCommentRun(lines []string, run []int, width int) {
	indent := lineIndent(lines[run[0]])
	lefts := make([]string, len(run))
	rights := make([]string, len(run))
	cols := make([]int, len(run))
	sizes := make([]int, len(run))
	for i, l := range run {
		at := strings.Index(lines[l], commentMark)
		lefts[i] = strings.TrimRight(dropAlignMarks(lines[l][:at]), " ")
		rights[i] = strings.TrimLeft(dropAlignMarks(lines[l][at+len(commentMark):]), " ")
		cols[i] = textWidth(stripMarks(lefts[i]))
		sizes[i] = cols[i] - indent
	}
	for _, sec := range alignSections(sizes) {
		if sec[1]-sec[0] < 2 {
			continue
		}
		hi := 0
		for i := sec[0]; i < sec[1]; i++ {
			hi = max(hi, cols[i])
		}
		padded := make([]string, sec[1]-sec[0])
		fits := true
		for i := sec[0]; i < sec[1]; i++ {
			padded[i-sec[0]] = lefts[i] + strings.Repeat(" ", hi-cols[i]+1) + rights[i]
			if hi != cols[i] && textWidth(stripMarks(padded[i-sec[0]])) > width {
				fits = false
				break
			}
		}
		if !fits {
			continue
		}
		for i := sec[0]; i < sec[1]; i++ {
			lines[run[i]] = padded[i-sec[0]]
		}
	}
}

// codeWidth is the width of line without its marks and its trailing
// comment, which the layout does not measure either.
func codeWidth(line string) int {
	if at := strings.Index(line, commentMark); at >= 0 {
		line = strings.TrimRight(line[:at], " ")
	}
	return textWidth(stripMarks(line))
}

// lineIndent is the number of spaces line starts with.
func lineIndent(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

// stripMarks is line without its marks, its escaped bytes (escapeMarks)
// restored.
func stripMarks(line string) string {
	if strings.IndexByte(line, markOpen) < 0 {
		return line
	}
	var b strings.Builder
	for i := 0; i < len(line); i++ {
		if line[i] == markOpen {
			if j := strings.IndexByte(line[i:], markClose); j >= 0 {
				switch line[i+1 : i+j] {
				case "!":
					b.WriteByte(markOpen)
				case "?":
					b.WriteByte(markClose)
				}
				i += j
				continue
			}
		}
		b.WriteByte(line[i])
	}
	return b.String()
}

// dropAlignMarks is line without the marks alignment reads, keeping the
// escaped bytes as they are.
func dropAlignMarks(line string) string {
	var b strings.Builder
	for i := 0; i < len(line); i++ {
		if line[i] == markOpen {
			if j := strings.IndexByte(line[i:], markClose); j >= 0 {
				if body := line[i+1 : i+j]; body != "!" && body != "?" {
					i += j
					continue
				}
			}
		}
		b.WriteByte(line[i])
	}
	return b.String()
}

// escapeMarks is s with each mark byte written as a mark of its own, which
// stripMarks turns back into the byte: a comment may hold bytes that are
// not UTF-8, and those must not read as marks.
func escapeMarks(s string) string {
	if strings.IndexByte(s, markOpen) < 0 && strings.IndexByte(s, markClose) < 0 {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case markOpen:
			b.WriteString("\xff!\xfe")
		case markClose:
			b.WriteString("\xff?\xfe")
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// rowsBlock is a block for items when they are rows: two or more tuples of
// one length, or calls of one callee with one argument count, each with
// two or more elements, none holding a comment or ending in a lambda. Each
// column after the first aligns. Otherwise it is the zero alignBlock.
func rowsBlock(items []ast.Node) alignBlock {
	if len(items) < 2 {
		return alignBlock{}
	}
	switch first := items[0].(type) {
	case *ast.TupleLit:
		for _, n := range items {
			t, ok := n.(*ast.TupleLit)
			if !ok || len(t.Items) != len(first.Items) || len(t.Items) < 2 || nodeHasDangling(t) {
				return alignBlock{}
			}
			if _, has := nodesComments(t.Items); has {
				return alignBlock{}
			}
		}
	case *ast.Call:
		head := Render(callHead(first), unboundedWidth)
		for _, n := range items {
			c, ok := n.(*ast.Call)
			if !ok || len(c.Args) != len(first.Args) || len(c.Args) < 2 || nodeHasDangling(c) {
				return alignBlock{}
			}
			if _, has := nodesComments(c.Args); has {
				return alignBlock{}
			}
			if _, lam := c.Args[len(c.Args)-1].(*ast.Lambda); lam || Render(callHead(c), unboundedWidth) != head {
				return alignBlock{}
			}
		}
	default:
		return alignBlock{}
	}
	return newAlignBlock(alignRows)
}

// callHead is what a call writes before its arguments.
func callHead(c *ast.Call) Doc {
	callee := emit(c.Func)
	if len(c.TypeArgs) > 0 {
		callee = Concat(callee, emitTypeArgs(c.TypeArgs))
	}
	return callee
}

// emitRow writes n, an item rowsBlock accepted, as item it, each element
// after the first a column.
func emitRow(n ast.Node, it alignItem) Doc {
	switch v := n.(type) {
	case *ast.TupleLit:
		elems := make([]Doc, len(v.Items))
		for i, e := range v.Items {
			elems[i] = Concat(rowCol(it, i), emit(e))
		}
		return it.wrap(emitTupleItems(elems, v))
	case *ast.Call:
		elems := make([]Doc, len(v.Args))
		for i, a := range v.Args {
			elems[i] = Concat(rowCol(it, i), emitCallArg(a))
		}
		return it.wrap(Concat(callHead(v), emitArgDocs(v, elems)))
	}
	return emit(n)
}

// rowCol is the column mark before a row's element i: none before the
// first.
func rowCol(it alignItem, i int) Doc {
	if i == 0 {
		return Nil()
	}
	return it.col(i - 1)
}
