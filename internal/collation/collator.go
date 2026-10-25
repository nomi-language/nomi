package collation

import (
	"bytes"
	"encoding/binary"
	"sync"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Collator compares strings under a root, an optional tailoring and
// settings. It is safe for concurrent use.
type Collator struct {
	root    *Root
	tail    *Tailoring
	reorder *reorderTable
	set     Settings
	// lv is the levels the settings compare, in order.
	lv []int
}

// New is the collator of root and tail (nil for the root order) with the
// tailoring's settings, or the root's, changed by change when it is not nil.
func New(root *Root, tail *Tailoring, change func(*Settings)) *Collator {
	c := &Collator{root: root, tail: tail, set: DefaultSettings}
	var codes []string
	if tail != nil {
		c.set = tail.Settings
		codes = tail.Reorder
	}
	if change != nil {
		change(&c.set)
	}
	c.reorder = root.newReorderTable(codes)
	c.lv = c.levels()
	return c
}

// Settings are the settings the collator compares with.
func (c *Collator) Settings() Settings { return c.set }

// lookup is the entry a code point starts: the tailoring's, which replaces
// the root's whole, else the root's.
func (c *Collator) lookup(r rune) *entry {
	if c.tail != nil {
		if e := c.tail.Table.entries[r]; e != nil {
			return e
		}
	}
	return c.root.Table.entries[r]
}

// work is the buffers one comparison uses.
type work struct {
	runes   []rune
	skipped []bool
	ces     []CE
	other   []CE
	// iter normalizes a string, and cand holds the bytes of a contraction
	// being matched.
	iter norm.Iter
	cand []byte
}

var workPool = sync.Pool{New: func() any { return &work{} }}

// nfd appends s in NFD to buf as code points.
func nfd(w *work, buf []rune, s string) []rune {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if !ascii && norm.NFD.QuickSpanString(s) != len(s) {
		// The iterator lives in the pooled work, so normalizing does
		// not allocate as norm.NFD.String does.
		w.iter.InitString(norm.NFD, s)
		for !w.iter.Done() {
			for _, r := range string(w.iter.Next()) {
				buf = append(buf, r)
			}
		}
		return buf
	}
	for _, r := range s {
		buf = append(buf, r)
	}
	return buf
}

func ccc(r rune) uint8 {
	if r < 0x300 {
		return 0
	}
	var b [utf8.UTFMax]byte
	n := utf8.EncodeRune(b[:], r)
	return norm.NFD.Properties(b[:n]).CCC()
}

// CEs appends the collation elements of s to buf.
func (c *Collator) CEs(buf []CE, s string) []CE {
	w := workPool.Get().(*work)
	defer workPool.Put(w)
	return c.appendCEs(w, buf, s)
}

func (c *Collator) appendCEs(w *work, buf []CE, s string) []CE {
	w.runes = nfd(w, w.runes[:0], s)
	runes := w.runes
	w.skipped = w.skipped[:0]
	for i := 0; i < len(runes); {
		if len(w.skipped) > 0 && w.skipped[i] {
			i++
			continue
		}
		r := runes[i]
		if c.set.Numeric {
			if _, ok := digitValue(r); ok {
				buf, i = c.appendNumeric(buf, runes, i, w.skipped)
				continue
			}
		}
		e := c.lookup(r)
		if e == nil {
			buf = append(buf, c.root.implicit(r))
			i++
			continue
		}
		if len(e.prefixed) > 0 {
			e = matchPrefix(e, runes[:i])
		}
		ces := e.ces
		next := i + 1
		if e.contractions != nil {
			ces, next = c.matchContraction(w, e, runes, i)
		}
		if ces == nil {
			buf = append(buf, c.root.implicit(r))
		} else {
			buf = append(buf, ces...)
		}
		i = next
	}
	return buf
}

// matchPrefix is the entry of the longest prefix context of e that the
// text before the code point ends with, else e.
func matchPrefix(e *entry, before []rune) *entry {
	for _, p := range e.prefixed {
		if len(p.prefix) > len(before) {
			continue
		}
		tail := before[len(before)-len(p.prefix):]
		match := true
		for k := range p.prefix {
			if tail[k] != p.prefix[k] {
				match = false
				break
			}
		}
		if match {
			return p.e
		}
	}
	return e
}

// matchContraction finds the longest contraction of e at runes[i], first
// over adjacent code points and then over unblocked non-starters after
// them (UTS #10 S2.1), which it marks skipped. It answers the CEs and the
// index after the adjacent part.
func (c *Collator) matchContraction(w *work, e *entry, runes []rune, i int) ([]CE, int) {
	skipped := func(k int) bool { return len(w.skipped) > 0 && w.skipped[k] }
	ces := e.ces
	// The candidate is UTF-8 in a buffer the comparison keeps; a map
	// lookup by string(buf) does not copy it.
	buf := utf8.AppendRune(w.cand[:0], runes[i])
	best := len(buf)
	end := i + 1
	for j := i + 1; j < len(runes); j++ {
		if skipped(j) {
			continue
		}
		buf = utf8.AppendRune(buf, runes[j])
		full, ok := e.contractions[string(buf)]
		if !ok && !e.starts[string(buf)] {
			break
		}
		if ok {
			ces, best, end = full, len(buf), j+1
		}
	}
	buf = buf[:best]

	// Discontiguous contractions: a non-starter after the match that no
	// code point between blocks (one of combining class 0 or of a class at
	// least its own) may extend it.
	if end < len(runes) && ccc(runes[end]) != 0 {
		var blocking uint8
		for k := end; k < len(runes); k++ {
			if skipped(k) {
				continue
			}
			cc := ccc(runes[k])
			if cc == 0 {
				break
			}
			if blocking >= cc {
				continue
			}
			n := len(buf)
			buf = utf8.AppendRune(buf, runes[k])
			if full, ok := e.contractions[string(buf)]; ok {
				ces = full
				if len(w.skipped) == 0 {
					for range runes {
						w.skipped = append(w.skipped, false)
					}
				}
				w.skipped[k] = true
				continue
			}
			buf = buf[:n]
			blocking = cc
		}
	}
	w.cand = buf
	return ces, end
}

// appendNumeric appends the CEs of the run of decimal digits at runes[i]:
// primaries that order the run by its value (UTS #35 part 5 "numeric"),
// numericLead first, then the count of significant digits, then the
// digits two to a byte.
func (c *Collator) appendNumeric(buf []CE, runes []rune, i int, skipped []bool) ([]CE, int) {
	var digitBuf [64]byte
	digits := digitBuf[:0]
	for i < len(runes) && (len(skipped) == 0 || !skipped[i]) {
		v, ok := digitValue(runes[i])
		if !ok {
			break
		}
		digits = append(digits, byte(v))
		i++
	}
	for len(digits) > 1 && digits[0] == 0 {
		digits = digits[1:]
	}
	for len(digits) > 0 {
		chunk := digits
		if len(chunk) > 254 {
			chunk = digits[:254]
		}
		digits = digits[len(chunk):]
		var keyBuf [160]byte
		bs := append(keyBuf[:0], byte(len(chunk)+1))
		for k := 0; k < len(chunk); k += 2 {
			pair := chunk[k] * 10
			if k+1 < len(chunk) {
				pair += chunk[k+1]
			}
			bs = append(bs, pair+2)
		}
		for k := 0; k < len(bs); k += 3 {
			p := uint32(numericLead) << 24
			for m := 0; m < 3 && k+m < len(bs); m++ {
				p |= uint32(bs[k+m]) << (16 - 8*m)
			}
			buf = append(buf, PrimaryCE(p))
		}
	}
	return buf, i
}

// level reads one level's weights from CEs, as the settings say which CEs
// a level skips.
type level struct {
	c        *Collator
	ces      []CE
	i        int
	variable bool // the last CE with a primary was variable
}

func (c *Collator) isVariable(p uint32) bool {
	// U+FFFE's primary, the merge separator, sorts below every other but is
	// not variable.
	return c.set.Shifted && p > mergeSeparatorPrimary && p <= c.root.VariableTop
}

// mergeSeparatorPrimary is U+FFFE's primary, CLDR's lowest.
const mergeSeparatorPrimary = 0x02000000

// next is the next weight at lvl and true, or false at the end. lvl is
// Primary, Secondary, caseLevel, Tertiary or Quaternary.
func (l *level) next(lvl int) (uint32, bool) {
	c := l.c
	for l.i < len(l.ces) {
		ce := l.ces[l.i]
		l.i++
		p := ce.Primary()
		if c.set.Shifted {
			if p != 0 {
				l.variable = c.isVariable(p)
			}
			if l.variable && lvl != Quaternary {
				continue
			}
			if l.variable && lvl == Quaternary {
				if p == 0 {
					continue
				}
				return c.reorder.apply(p), true
			}
		}
		switch lvl {
		case Primary:
			if p != 0 {
				return c.reorder.apply(p), true
			}
		case Secondary:
			if s := ce.Secondary(); s != 0 {
				return uint32(s), true
			}
		case caseLevel:
			if p != 0 {
				cs := uint32(ce.Case())
				if c.set.CaseFirst == CaseFirstUpper {
					cs = 2 - cs
				}
				return cs + 1, true
			}
		case Tertiary:
			low := ce.Lower16()
			if low&OnlyTertiaryMask == 0 {
				continue
			}
			t := uint32(low & OnlyTertiaryMask)
			if c.set.CaseFirst != CaseFirstOff && !c.set.CaseLevel {
				t = uint32(low & (OnlyTertiaryMask | CaseMask))
				if c.set.CaseFirst == CaseFirstUpper {
					if uint32(ce) > 0xffff {
						t ^= CaseMask
					} else {
						t += 0x4000
					}
				}
			}
			return t, true
		case Quaternary:
			low := uint32(ce.Lower16())
			if low&OnlyTertiaryMask == 0 {
				continue
			}
			return 0xffffff3f | low&QuaternaryMask, true
		}
	}
	return 0, false
}

// caseLevel is the case level, which comes between the secondary and the
// tertiary levels.
const caseLevel = 100

// levels is the levels the settings compare, in order, up to the
// quaternary.
func (c *Collator) levels() []int {
	ls := []int{Primary}
	if c.set.Strength >= Secondary {
		ls = append(ls, Secondary)
	}
	if c.set.CaseLevel {
		ls = append(ls, caseLevel)
	}
	if c.set.Strength >= Tertiary {
		ls = append(ls, Tertiary)
	}
	if c.set.Strength >= Quaternary {
		ls = append(ls, Quaternary)
	}
	return ls
}

// Compare orders a and b: -1, 0 or 1.
func (c *Collator) Compare(a, b string) int {
	if a == b {
		return 0
	}
	w := workPool.Get().(*work)
	defer workPool.Put(w)
	w.ces = c.appendCEs(w, w.ces[:0], a)
	w.other = c.appendCEs(w, w.other[:0], b)
	for _, lvl := range c.lv {
		if r := c.compareLevel(lvl, w.ces, w.other); r != 0 {
			return r
		}
	}
	if c.set.Strength == Identical {
		w.runes = nfd(w, w.runes[:0], a)
		ra := string(w.runes)
		w.runes = nfd(w, w.runes[:0], b)
		return compareRunes(ra, string(w.runes))
	}
	return 0
}

func compareRunes(a, b string) int {
	// UTF-8 orders as the code points do.
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func (c *Collator) compareLevel(lvl int, a, b []CE) int {
	if lvl == Secondary && c.set.Backwards {
		wa := c.weights(lvl, a)
		wb := c.weights(lvl, b)
		for i, j := len(wa)-1, len(wb)-1; ; i, j = i-1, j-1 {
			switch {
			case i < 0 && j < 0:
				return 0
			case i < 0:
				return -1
			case j < 0:
				return 1
			case wa[i] != wb[j]:
				return cmp32(wa[i], wb[j])
			}
		}
	}
	la, lb := level{c: c, ces: a}, level{c: c, ces: b}
	for {
		x, okx := la.next(lvl)
		y, oky := lb.next(lvl)
		switch {
		case !okx && !oky:
			return 0
		case !okx:
			return -1
		case !oky:
			return 1
		case x != y:
			return cmp32(x, y)
		}
	}
}

func (c *Collator) weights(lvl int, ces []CE) []uint32 {
	var out []uint32
	l := level{c: c, ces: ces}
	for {
		x, ok := l.next(lvl)
		if !ok {
			return out
		}
		out = append(out, x)
	}
}

func cmp32(x, y uint32) int {
	if x < y {
		return -1
	}
	return 1
}

// SortKey is s's sort key: two strings' keys compare, byte by byte, as
// Compare compares the strings. Each level's weights are written at a
// fixed width (four bytes for primaries and quaternaries, two for
// secondaries and tertiaries, one for case), every one starting with a
// byte above zero, and a zero byte separates the levels.
func (c *Collator) SortKey(s string) []byte {
	w := workPool.Get().(*work)
	defer workPool.Put(w)
	w.ces = c.appendCEs(w, w.ces[:0], s)
	var key []byte
	for n, lvl := range c.lv {
		if n > 0 {
			key = append(key, 0)
		}
		ws := c.weights(lvl, w.ces)
		if lvl == Secondary && c.set.Backwards {
			for i, j := 0, len(ws)-1; i < j; i, j = i+1, j-1 {
				ws[i], ws[j] = ws[j], ws[i]
			}
		}
		for _, x := range ws {
			switch lvl {
			case Primary, Quaternary:
				key = binary.BigEndian.AppendUint32(key, x)
			case Secondary, Tertiary:
				key = binary.BigEndian.AppendUint16(key, uint16(x))
			default:
				key = append(key, byte(x))
			}
		}
	}
	if c.set.Strength == Identical {
		key = append(key, 0)
		w.runes = nfd(w, w.runes[:0], s)
		key = append(key, string(w.runes)...)
	}
	return key
}

// CompareKeys compares two sort keys.
func CompareKeys(a, b []byte) int { return bytes.Compare(a, b) }
