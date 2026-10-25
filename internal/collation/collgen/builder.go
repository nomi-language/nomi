package collgen

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/nomi-language/nomi/internal/collation"
)

// Loader answers the rules of a locale's collation type, for `[import]`.
type Loader func(locale, typ string) (rules string, ok bool)

// Build compiles rules over the root FractionalUCA gives into a tailoring.
func Build(fr *Fractional, rules string, load Loader) (*collation.Tailoring, error) {
	root := fr.Root()
	b := &builder{
		fr:        fr,
		root:      root,
		elems:     newRootElements(fr.Markers),
		base:      collation.New(root, nil, nil),
		tail:      &collation.Tailoring{Table: collation.NewTable(nil)},
		byRune:    map[rune][]collation.Mapping{},
		primaries: map[uint32]int{},
		load:      load,
		importing: map[string]bool{},
		set:       collation.DefaultSettings,
	}
	b.cur = collation.New(root, b.tail, nil)
	if err := parseRules(rules, b); err != nil {
		return nil, err
	}
	if err := b.makeTailoredCEs(); err != nil {
		return nil, err
	}
	b.finalize()
	b.tail.Settings = b.set
	b.tail.Reorder = b.reorder
	return b.tail, nil
}

type node struct {
	// weight is a primary node's primary, or a secondary or tertiary
	// node's 16-bit weight; a tailored node's is assigned at the end.
	weight   uint32
	strength int
	prev     int
	next     int
	tailored bool
	before2  bool
	before3  bool
	// ce is a tailored node's CE, once assigned.
	ce collation.CE
}

type builder struct {
	fr    *Fractional
	root  *collation.Root
	elems *rootElements
	base  *collation.Collator // the root alone
	cur   *collation.Collator // the root and the tailoring so far
	tail  *collation.Tailoring
	// byRune is the tailoring's mappings by first code point.
	byRune    map[rune][]collation.Mapping
	nodes     []node
	primaries map[uint32]int // root primary -> its node
	// ces is the CEs of the current position: the last reset or relation,
	// its last CE a temporary CE standing for a node.
	ces       []collation.CE
	set       collation.Settings
	reorder   []string
	load      Loader
	importing map[string]bool
}

const maxExpansion = 31

// A temporary CE stands for a node: tempLead, the node's index, its
// strength in the secondary field, and case bits in the low 16 bits.
func tempCE(index, strength int) collation.CE {
	return collation.CE(uint64(0x01)<<56 | uint64(index)<<32 | uint64(strength)<<16)
}

func tempIndex(ce collation.CE) int { return int(uint64(ce)>>32) & 0xffffff }

func ceStrength(ce collation.CE) int {
	if collation.IsTemp(ce) {
		return int(ce.Secondary())
	}
	return ce.Strength()
}

func nfd(s string) string { return norm.NFD.String(s) }

func (b *builder) cesOf(s string) []collation.CE { return b.cur.CEs(nil, nfd(s)) }

// reset is `&s` or `&[before n]s`, or a reset to a special position.
func (b *builder) reset(strength int, s, position string) error {
	if position != "" {
		ce, err := b.specialReset(position)
		if err != nil {
			return err
		}
		b.ces = []collation.CE{ce}
	} else {
		b.ces = b.cesOf(s)
		if len(b.ces) > maxExpansion {
			return errors.New("reset position maps to too many collation elements")
		}
	}
	if strength == collation.Identical {
		return nil
	}
	index, err := b.findOrInsertNodeForCEs(strength)
	if err != nil {
		return err
	}
	n := b.nodes[index]
	for n.strength > strength {
		index = n.prev
		n = b.nodes[index]
	}
	switch {
	case n.strength == strength && n.tailored:
		index = n.prev
	case strength == collation.Primary:
		p := n.weight
		if p == 0 {
			return errors.New("reset primary-before ignorable not possible")
		}
		if p <= b.elems.firstPrimary() {
			return errors.New("reset primary-before first non-ignorable not supported")
		}
		if p == b.fr.Positions["first trailing"].Primary() {
			return errors.New("reset primary-before [first trailing] not supported")
		}
		index = b.findOrInsertNodeForPrimary(b.elems.primaryBefore(p))
		for b.nodes[index].next >= 0 {
			index = b.nodes[index].next
		}
	default:
		index = b.findCommonNode(index, collation.Secondary)
		if strength >= collation.Tertiary {
			index = b.findCommonNode(index, collation.Tertiary)
		}
		n = b.nodes[index]
		if n.strength == strength {
			w16 := n.weight
			if w16 == 0 {
				return errors.New("reset tertiary-before completely ignorable not possible")
			}
			w16 = b.weight16Before(index, strength)
			prevIndex := n.prev
			var prevW16 uint32
			for i := prevIndex; ; i = b.nodes[i].prev {
				m := b.nodes[i]
				if m.strength < strength {
					prevW16 = collation.CommonWeight16
					break
				}
				if m.strength == strength && !m.tailored {
					prevW16 = m.weight
					break
				}
			}
			if prevW16 == w16 {
				index = prevIndex
			} else {
				index = b.insertNodeBetween(prevIndex, index, node{weight: w16, strength: strength})
			}
		} else {
			index = b.findOrInsertWeakNode(index, collation.BeforeWeight16, strength)
		}
		strength = ceStrength(b.ces[len(b.ces)-1])
	}
	b.ces[len(b.ces)-1] = tempCE(index, strength)
	return nil
}

func (b *builder) specialReset(pos string) (collation.CE, error) {
	isFirst := strings.HasPrefix(pos, "first ")
	strength := collation.Primary
	isBoundary := false
	var ce collation.CE
	switch pos {
	case "first tertiary ignorable", "last tertiary ignorable":
		return 0, nil
	case "first secondary ignorable":
		index := b.findOrInsertNodeForRootCE(0, collation.Tertiary)
		if next := b.nodes[index].next; next >= 0 {
			if n := b.nodes[next]; n.tailored && n.strength == collation.Tertiary {
				return tempCE(next, collation.Tertiary), nil
			}
		}
		return b.fr.Positions["first secondary ignorable"], nil
	case "last secondary ignorable":
		ce, strength = b.fr.Positions["last secondary ignorable"], collation.Tertiary
	case "first primary ignorable":
		index := b.findOrInsertNodeForRootCE(0, collation.Secondary)
		for i := b.nodes[index].next; i >= 0; i = b.nodes[i].next {
			n := b.nodes[i]
			if n.strength < collation.Secondary {
				break
			}
			if n.strength == collation.Secondary {
				if n.tailored {
					if n.before3 {
						i = b.nodes[b.nodes[i].next].next
					}
					return tempCE(i, collation.Secondary), nil
				}
				break
			}
		}
		ce, strength = b.fr.Positions["first primary ignorable"], collation.Secondary
	case "last primary ignorable":
		ce, strength = b.fr.Positions["last primary ignorable"], collation.Secondary
	case "first variable":
		ce, isBoundary = collation.PrimaryCE(b.elems.firstPrimary()), true
	case "last variable":
		ce = b.lastCEWithPrimaryAtMost(b.root.VariableTop)
	case "first regular":
		ce, isBoundary = b.firstCEWithPrimaryAtLeast(b.root.VariableTop+1), true
	case "last regular":
		// ICU resets to the Han script's first primary, so what a tailoring
		// puts after [last regular] is in the Han reordering group.
		ce = b.firstCEWithPrimaryAtLeast(b.hanFirstPrimary())
	case "first implicit":
		ce = b.root.Implicit(0x4e00)
	case "last implicit":
		return 0, errors.New("reset to [last implicit] not supported")
	case "first trailing":
		ce, isBoundary = collation.PrimaryCE(b.fr.Positions["first trailing"].Primary()), true
	case "last trailing":
		return 0, errors.New("LDML forbids tailoring to U+FFFF")
	default:
		return 0, fmt.Errorf("unknown position [%s]", pos)
	}
	index := b.findOrInsertNodeForRootCE(ce, strength)
	n := b.nodes[index]
	if isFirst {
		if !n.before2 && !n.before3 && isBoundary {
			if next := n.next; next >= 0 {
				ce = tempCE(next, strength)
				index, n = next, b.nodes[next]
			} else {
				p := b.elems.primaryAfter(ce.Primary())
				ce = collation.PrimaryCE(p)
				index = b.findOrInsertNodeForRootCE(ce, collation.Primary)
				n = b.nodes[index]
			}
		}
		if n.before2 || n.before3 {
			if n.before2 {
				index = b.nodes[b.nodes[index].next].next
				n = b.nodes[index]
			}
			if n.before3 {
				index = b.nodes[b.nodes[index].next].next
			}
			ce = tempCE(index, strength)
		}
		return ce, nil
	}
	for {
		next := n.next
		if next < 0 || b.nodes[next].strength < strength {
			break
		}
		index, n = next, b.nodes[next]
	}
	if n.tailored {
		ce = tempCE(index, strength)
	}
	return ce, nil
}

func (b *builder) hanFirstPrimary() uint32 {
	for _, g := range b.root.Groups {
		for _, c := range g.Codes {
			if c == "Hani" {
				return g.Start
			}
		}
	}
	return collation.FirstImplicitPrimary
}

func (b *builder) firstCEWithPrimaryAtLeast(p uint32) collation.CE {
	ps := b.elems.primaries
	i := sort.Search(len(ps), func(i int) bool { return ps[i] >= p })
	return collation.PrimaryCE(ps[i])
}

func (b *builder) lastCEWithPrimaryAtMost(p uint32) collation.CE {
	ps := b.elems.primaries
	i := sort.Search(len(ps), func(i int) bool { return ps[i] > p }) - 1
	combos := b.elems.combos(ps[i])
	st := combos[len(combos)-1]
	return collation.MakeCE(ps[i], uint16(st>>16), uint16(st))
}

func (b *builder) relation(strength int, prefix, s, ext string) error {
	nfdPrefix, nfdS := nfd(prefix), nfd(s)
	if strength != collation.Identical {
		index, err := b.findOrInsertNodeForCEs(strength)
		if err != nil {
			return err
		}
		ce := b.ces[len(b.ces)-1]
		if strength == collation.Primary && !collation.IsTemp(ce) && ce.Primary() == 0 {
			return errors.New("tailoring primary after ignorables not supported")
		}
		if strength == collation.Quaternary && ce == 0 {
			return errors.New("tailoring quaternary after tertiary ignorables not supported")
		}
		index = b.insertTailoredNodeAfter(index, strength)
		ts := ceStrength(ce)
		if strength < ts {
			ts = strength
		}
		b.ces[len(b.ces)-1] = tempCE(index, ts)
	}
	b.setCaseBits(nfdS)
	ces := append([]collation.CE(nil), b.ces...)
	if ext != "" {
		ces = b.cur.CEs(ces, nfd(ext))
	}
	if len(ces) > maxExpansion {
		return errors.New("a mapping to too many collation elements")
	}
	b.addMapping(nfdPrefix, nfdS, ces)
	return nil
}

// addMapping adds a mapping to the tailoring. The tailoring's mappings of a
// code point replace the root's whole, so the first one of a code point
// brings the root's along.
func (b *builder) addMapping(prefix, s string, ces []collation.CE) {
	first, _ := utf8.DecodeRuneInString(s)
	ms, ok := b.byRune[first]
	if !ok {
		ms = b.root.Table.MappingsOf(first)
		hasSingle := false
		for _, m := range ms {
			if m.Prefix == "" && m.Str == string(first) {
				hasSingle = true
			}
		}
		if !hasSingle {
			ms = append(ms, collation.Mapping{Str: string(first), CEs: []collation.CE{b.root.Implicit(first)}})
		}
	}
	out := ms[:0:0]
	for _, m := range ms {
		if m.Prefix != prefix || m.Str != s {
			out = append(out, m)
		}
	}
	out = append(out, collation.Mapping{Prefix: prefix, Str: s, CEs: ces})
	b.byRune[first] = out
	b.tail.Table.Set(first, out)
}

// setCaseBits sets the case bits of the current CEs from the root CEs of
// s, as ICU's CollationBuilder::setCaseBits does: each tailored primary CE
// takes the case of the root's primary CE in its place, the last one the
// case of the rest (mixed when they differ).
func (b *builder) setCaseBits(s string) {
	n := 0
	for _, ce := range b.ces {
		if ceStrength(ce) == collation.Primary {
			n++
		}
	}
	var cases uint64
	if n > 0 {
		var lastCase uint64
		nb := 0
		for _, ce := range b.base.CEs(nil, s) {
			if ce.Primary() == 0 {
				continue
			}
			nb++
			c := uint64(ce.Case())
			if nb < n {
				cases |= c << ((nb - 1) * 2)
			} else if nb == n {
				lastCase = c
			} else if c != lastCase {
				lastCase = 1
				break
			}
		}
		if nb >= n {
			cases |= lastCase << ((n - 1) * 2)
		}
	}
	for i, ce := range b.ces {
		ce &^= collation.CaseMask
		switch ceStrength(ce) {
		case collation.Primary:
			ce |= collation.CE(cases&3) << 14
			cases >>= 2
		case collation.Tertiary:
			ce |= 0x8000
		}
		b.ces[i] = ce
	}
}

func (b *builder) findOrInsertNodeForCEs(strength int) (int, error) {
	var ce collation.CE
	for ; ; b.ces = b.ces[:len(b.ces)-1] {
		if len(b.ces) == 0 {
			b.ces = []collation.CE{0}
			ce = 0
			break
		}
		ce = b.ces[len(b.ces)-1]
		if ceStrength(ce) <= strength {
			break
		}
	}
	if collation.IsTemp(ce) {
		return tempIndex(ce), nil
	}
	if p := ce.Primary(); p >= collation.UnassignedPrimary && p < b.fr.Positions["first trailing"].Primary() {
		return 0, errors.New("tailoring relative to an unassigned code point not supported")
	}
	return b.findOrInsertNodeForRootCE(ce, strength), nil
}

func (b *builder) findOrInsertNodeForRootCE(ce collation.CE, strength int) int {
	index := b.findOrInsertNodeForPrimary(ce.Primary())
	if strength >= collation.Secondary {
		low := uint32(ce)
		index = b.findOrInsertWeakNode(index, low>>16, collation.Secondary)
		if strength >= collation.Tertiary {
			index = b.findOrInsertWeakNode(index, low&collation.OnlyTertiaryMask, collation.Tertiary)
		}
	}
	return index
}

func (b *builder) findOrInsertNodeForPrimary(p uint32) int {
	if i, ok := b.primaries[p]; ok {
		return i
	}
	i := len(b.nodes)
	b.nodes = append(b.nodes, node{weight: p, strength: collation.Primary, prev: -1, next: -1})
	b.primaries[p] = i
	return i
}

func (b *builder) findOrInsertWeakNode(index int, w16 uint32, level int) int {
	if w16 == collation.CommonWeight16 {
		return b.findCommonNode(index, level)
	}
	n := b.nodes[index]
	if w16 != 0 && w16 < collation.CommonWeight16 {
		has := n.before2
		if level == collation.Tertiary {
			has = n.before3
		}
		if !has {
			// The parent has an implied common weight at this level: make
			// it explicit after the new below-common node.
			common := node{weight: collation.CommonWeight16, strength: level}
			if level == collation.Secondary {
				common.before3 = n.before3
				b.nodes[index].before3 = false
				b.nodes[index].before2 = true
			} else {
				b.nodes[index].before3 = true
			}
			next := n.next
			index = b.insertNodeBetween(index, next, node{weight: w16, strength: level})
			b.insertNodeBetween(index, next, common)
			return index
		}
	}
	// Insert the root weight before the next stronger node, or before the
	// next root node of the same strength with a larger weight.
	next := n.next
	for next >= 0 {
		m := b.nodes[next]
		if m.strength <= level {
			if m.strength < level {
				break
			}
			if !m.tailored {
				if m.weight == w16 {
					return next
				}
				if m.weight > w16 {
					break
				}
			}
		}
		index = next
		next = m.next
	}
	return b.insertNodeBetween(index, next, node{weight: w16, strength: level})
}

func (b *builder) insertTailoredNodeAfter(index, strength int) int {
	if strength >= collation.Secondary {
		index = b.findCommonNode(index, collation.Secondary)
		if strength >= collation.Tertiary {
			index = b.findCommonNode(index, collation.Tertiary)
		}
	}
	next := b.nodes[index].next
	for next >= 0 && b.nodes[next].strength > strength {
		index = next
		next = b.nodes[next].next
	}
	return b.insertNodeBetween(index, next, node{strength: strength, tailored: true})
}

func (b *builder) insertNodeBetween(index, next int, n node) int {
	i := len(b.nodes)
	n.prev, n.next = index, next
	b.nodes = append(b.nodes, n)
	b.nodes[index].next = i
	if next >= 0 {
		b.nodes[next].prev = i
	}
	return i
}

func (b *builder) findCommonNode(index, strength int) int {
	n := b.nodes[index]
	if n.strength >= strength {
		return index
	}
	if (strength == collation.Secondary && !n.before2) || (strength == collation.Tertiary && !n.before3) {
		return index
	}
	index = n.next
	n = b.nodes[index]
	for {
		index = n.next
		n = b.nodes[index]
		if !(n.tailored || n.strength > strength || n.weight < collation.CommonWeight16) {
			return index
		}
	}
}

func (b *builder) weight16Before(index int, level int) uint32 {
	n := b.nodes[index]
	t := uint32(collation.CommonWeight16)
	if n.strength == collation.Tertiary {
		t = n.weight
	}
	for n.strength > collation.Secondary {
		index = n.prev
		n = b.nodes[index]
	}
	if n.tailored {
		return collation.BeforeWeight16
	}
	s := uint32(collation.CommonWeight16)
	if n.strength == collation.Secondary {
		s = n.weight
	}
	for n.strength > collation.Primary {
		index = n.prev
		n = b.nodes[index]
	}
	if n.tailored {
		return collation.BeforeWeight16
	}
	p := n.weight
	if level == collation.Secondary {
		return b.elems.secondaryBefore(p, s)
	}
	return b.elems.tertiaryBefore(p, s, t)
}

func (b *builder) countTailoredNodes(i, strength int) int {
	count := 0
	for i >= 0 {
		n := b.nodes[i]
		if n.strength < strength {
			break
		}
		if n.strength == strength {
			if !n.tailored {
				break
			}
			count++
		}
		i = n.next
	}
	return count
}

// weightRun hands out n weights strictly between two limits, one after
// another.
type weightRun struct {
	next, limit uint64
	tertiary    bool
}

func newWeightRun(lower, upper uint32, n int, tertiary bool) (*weightRun, bool) {
	lo, hi := uint64(lower), uint64(upper)
	if tertiary {
		lo, hi = linearTertiary(lower), linearTertiary(upper)
	}
	if hi <= lo || hi-lo <= uint64(n) {
		return nil, false
	}
	return &weightRun{next: lo + 1, limit: hi, tertiary: tertiary}, true
}

func (w *weightRun) take() uint32 {
	v := w.next
	w.next++
	if w.tertiary {
		return uint32(v>>6)<<8 | uint32(v&0x3f)
	}
	return uint32(v)
}

// linearTertiary is a tertiary weight's two 6-bit bytes as one number.
func linearTertiary(t uint32) uint64 { return uint64(t>>8)<<6 | uint64(t&0x3f) }

// makeTailoredCEs gives each tailored node its weights, as ICU's
// CollationBuilder::makeTailoredCEs does.
func (b *builder) makeTailoredCEs() error {
	var roots []uint32
	for p := range b.primaries {
		roots = append(roots, p)
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i] < roots[j] })
	for _, rp := range roots {
		i := b.primaries[rp]
		n := b.nodes[i]
		p := n.weight
		s := uint32(0)
		if p != 0 {
			s = collation.CommonWeight16
		}
		t := s
		q := uint32(0)
		pTailored, sTailored, tTailored := false, false, false
		var prims, secs, ters *weightRun
		for next := n.next; next >= 0; {
			i = next
			n = b.nodes[i]
			next = n.next
			switch n.strength {
			case collation.Quaternary:
				if q == 3 {
					return errors.New("quaternary tailoring gap too small")
				}
				q++
			case collation.Tertiary:
				if n.tailored {
					if !tTailored {
						count := b.countTailoredNodes(next, collation.Tertiary) + 1
						var limit uint32
						switch {
						case t == 0:
							t = tertiaryBoundary - 0x100
							limit = uint32(b.fr.Positions["first secondary ignorable"].Tertiary())
						case !pTailored && !sTailored:
							limit = b.elems.tertiaryAfter(p, s, t)
						case t == collation.BeforeWeight16:
							limit = collation.CommonWeight16
						default:
							limit = tertiaryBoundary
						}
						var ok bool
						if ters, ok = newWeightRun(t, limit, count, true); !ok {
							return errors.New("tertiary tailoring gap too small")
						}
						tTailored = true
					}
					t = ters.take()
				} else {
					t = n.weight
					tTailored = false
				}
				q = 0
			case collation.Secondary:
				if n.tailored {
					if !sTailored {
						count := b.countTailoredNodes(next, collation.Secondary) + 1
						var limit uint32
						switch {
						case s == 0:
							s = secondaryBoundary - 0x100
							limit = uint32(b.fr.Positions["first primary ignorable"].Secondary())
						case !pTailored:
							limit = b.elems.secondaryAfter(p, s)
						case s == collation.BeforeWeight16:
							limit = collation.CommonWeight16
						default:
							limit = secondaryBoundary
						}
						if s == collation.CommonWeight16 {
							s = lastCommonSecondary
						}
						var ok bool
						if secs, ok = newWeightRun(s, limit, count, false); !ok {
							return errors.New("secondary tailoring gap too small")
						}
						sTailored = true
					}
					s = secs.take()
				} else {
					s = n.weight
					sTailored = false
				}
				t = 0
				if s != 0 {
					t = collation.CommonWeight16
				}
				tTailored = false
				q = 0
			case collation.Primary:
				if !pTailored {
					count := b.countTailoredNodes(next, collation.Primary) + 1
					limit := b.elems.primaryAfter(p)
					var ok bool
					if prims, ok = newWeightRun(p, limit, count, false); !ok {
						return errors.New("primary tailoring gap too small")
					}
					pTailored = true
				}
				p = prims.take()
				s = collation.CommonWeight16
				sTailored = false
				t = collation.CommonWeight16
				tTailored = false
				q = 0
			}
			if n.tailored {
				b.nodes[i].ce = collation.MakeCE(p, uint16(s), uint16(t|q<<6))
			}
		}
	}
	return nil
}

// finalize replaces each temporary CE by its node's CE and the temporary
// CE's case bits.
func (b *builder) finalize() {
	for r, ms := range b.byRune {
		changed := false
		for k, m := range ms {
			for j, ce := range m.CEs {
				if collation.IsTemp(ce) {
					if !changed {
						ms = append([]collation.Mapping(nil), ms...)
						changed = true
					}
					ces := append([]collation.CE(nil), ms[k].CEs...)
					ces[j] = b.nodes[tempIndex(ce)].ce | ce&collation.CaseMask
					ms[k].CEs = ces
					m = ms[k]
				}
			}
		}
		if changed {
			b.byRune[r] = ms
			b.tail.Table.Set(r, ms)
		}
	}
}

func (b *builder) setting(name string, args []string, set []rune) error {
	arg := ""
	if len(args) > 0 {
		arg = args[0]
	}
	switch name {
	case "strength":
		switch arg {
		case "1", "2", "3", "4":
			n, _ := strconv.Atoi(arg)
			b.set.Strength = n - 1
		case "I":
			b.set.Strength = collation.Identical
		default:
			return fmt.Errorf("bad strength %q", arg)
		}
	case "alternate":
		switch arg {
		case "shifted":
			b.set.Shifted = true
		case "non-ignorable":
			b.set.Shifted = false
		default:
			return fmt.Errorf("bad alternate %q", arg)
		}
	case "caseFirst":
		switch arg {
		case "upper":
			b.set.CaseFirst = collation.CaseFirstUpper
		case "lower":
			b.set.CaseFirst = collation.CaseFirstLower
		case "off":
			b.set.CaseFirst = collation.CaseFirstOff
		default:
			return fmt.Errorf("bad caseFirst %q", arg)
		}
	case "caseLevel":
		b.set.CaseLevel = arg == "on"
	case "backwards":
		if arg != "2" {
			return fmt.Errorf("bad backwards %q", arg)
		}
		b.set.Backwards = true
	case "numericOrdering":
		b.set.Numeric = arg == "on"
	case "reorder":
		b.reorder = args
	case "suppressContractions":
		for _, r := range set {
			single, ok := b.root.Table.Single(r)
			if !ok {
				single = []collation.CE{b.root.Implicit(r)}
			}
			ms := []collation.Mapping{{Str: string(r), CEs: single}}
			b.byRune[r] = ms
			b.tail.Table.Set(r, ms)
		}
	case "normalization", "optimize", "hiraganaQ":
		// Strings are always normalized; optimizing is ICU's.
	default:
		return fmt.Errorf("unsupported setting [%s]", name)
	}
	return nil
}

// importRules reads another collation's rules here, as ICU does: tag is a
// locale with an optional -u-co- type (`de-u-co-phonebk`, `hr`).
func (b *builder) importRules(tag string) error {
	locale, typ := tag, "standard"
	if i := strings.Index(tag, "-u-co-"); i >= 0 {
		locale, typ = tag[:i], tag[i+len("-u-co-"):]
	}
	typ = collation.TypeOfKeyword(typ)
	locale = strings.ReplaceAll(locale, "-", "_")
	if locale == "und" {
		locale = "root"
	}
	key := locale + "/" + typ
	if b.importing[key] {
		return fmt.Errorf("imports %s from itself", key)
	}
	if b.load == nil {
		return errors.New("no rules to import from")
	}
	rules, ok := b.load(locale, typ)
	if !ok {
		return fmt.Errorf("no %s collation for %s", typ, locale)
	}
	b.importing[key] = true
	defer delete(b.importing, key)
	return parseRules(rules, b)
}
