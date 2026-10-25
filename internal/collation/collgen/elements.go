package collgen

import (
	"sort"

	"github.com/nomi-language/nomi/internal/collation"
)

// rootElements answers what weights the root uses around a weight, as
// ICU's CollationRootElements does: the primaries in order, and for each
// primary the secondary and tertiary combinations its CEs have (common
// implied), and the secondary and tertiary CEs that have no primary.
type rootElements struct {
	primaries []uint32
	// secTer is each primary's sec<<16|ter combinations, sorted, common
	// included.
	secTer map[uint32][]uint32
	// secondaryCEs are the sec<<16|ter of the CEs with no primary and a
	// secondary, and tertiaryCEs the ter of those with only a tertiary.
	secondaryCEs []uint32
	tertiaryCEs  []uint32
}

// The boundaries FractionalUCA fixes: tailored secondaries of a primary CE
// stay below its first ignorable secondary byte, and tailored tertiaries
// below its first ignorable tertiary byte; the common secondaries run to
// the last common secondary byte.
const (
	secondaryBoundary   = 0x8000
	tertiaryBoundary    = 0x3c00
	lastCommonSecondary = 0x4500
)

func newRootElements(ms []collation.Mapping) *rootElements {
	e := &rootElements{secTer: map[uint32][]uint32{}}
	prims := map[uint32]bool{collation.FirstImplicitPrimary: true}
	sts := map[uint32]map[uint32]bool{}
	secs, ters := map[uint32]bool{}, map[uint32]bool{}
	for _, m := range ms {
		for _, ce := range m.CEs {
			p, st := ce.Primary(), uint32(ce.Secondary())<<16|uint32(ce.Tertiary())
			switch {
			case p != 0:
				if p > mergeSeparator {
					prims[p] = true
				}
				if sts[p] == nil {
					sts[p] = map[uint32]bool{collation.CommonSecTer: true}
				}
				sts[p][st] = true
			case ce.Secondary() != 0:
				secs[st] = true
			case ce.Tertiary() != 0:
				ters[uint32(ce.Tertiary())] = true
			}
		}
	}
	for p := range prims {
		e.primaries = append(e.primaries, p)
	}
	sort.Slice(e.primaries, func(i, j int) bool { return e.primaries[i] < e.primaries[j] })
	for p, set := range sts {
		e.secTer[p] = sortedSet(set)
	}
	e.secondaryCEs = sortedSet(secs)
	e.tertiaryCEs = sortedSet(ters)
	return e
}

const mergeSeparator = 0x02000000

func sortedSet(m map[uint32]bool) []uint32 {
	out := make([]uint32, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// firstPrimary is the lowest primary a tailoring may sort before.
func (e *rootElements) firstPrimary() uint32 { return e.primaries[0] }

func (e *rootElements) combos(p uint32) []uint32 {
	if c, ok := e.secTer[p]; ok {
		return c
	}
	return []uint32{collation.CommonSecTer}
}

// primaryBefore is the root primary before p.
func (e *rootElements) primaryBefore(p uint32) uint32 {
	i := sort.Search(len(e.primaries), func(i int) bool { return e.primaries[i] >= p })
	prev := uint32(0)
	if i > 0 {
		prev = e.primaries[i-1]
	}
	if p > collation.FirstImplicitPrimary {
		// An implicit primary: the slot before it, which no other code
		// point's primary lies above.
		step := uint32(1 << 8)
		switch {
		case p >= collation.UnassignedPrimary:
			step = 1
		case p >= 0xe1000000:
			step = 1 << 6
		}
		if c := p - step; c > prev {
			return c
		}
	}
	return prev
}

// primaryAfter is the root primary after p.
func (e *rootElements) primaryAfter(p uint32) uint32 {
	if p >= collation.FirstImplicitPrimary {
		i := sort.Search(len(e.primaries), func(i int) bool { return e.primaries[i] > p })
		next := collation.PrimaryAfterImplicit(p)
		if i < len(e.primaries) && e.primaries[i] < next {
			return e.primaries[i]
		}
		return next
	}
	i := sort.Search(len(e.primaries), func(i int) bool { return e.primaries[i] > p })
	return e.primaries[i]
}

// secondaryAfter is the next secondary than s that the root uses with
// primary p, or the limit tailored secondaries stay below.
func (e *rootElements) secondaryAfter(p uint32, s uint32) uint32 {
	list, limit := e.secondaryCEs, uint32(0x10000)
	if p != 0 {
		list, limit = e.combos(p), secondaryBoundary
	}
	for _, st := range list {
		if st>>16 > s {
			return st >> 16
		}
	}
	return limit
}

// tertiaryAfter is the next tertiary than t that the root uses with p and
// s, or the limit.
func (e *rootElements) tertiaryAfter(p, s, t uint32) uint32 {
	var list []uint32
	limit := uint32(tertiaryBoundary)
	switch {
	case p != 0:
		list = e.combos(p)
	case s != 0:
		list = e.secondaryCEs
	default:
		for _, x := range e.tertiaryCEs {
			if x > t {
				return x
			}
		}
		return 0x4000
	}
	st := s<<16 | t
	for _, x := range list {
		if x > st {
			if x>>16 == s {
				return x & 0xffff
			}
			return limit
		}
	}
	return limit
}

// secondaryBefore is the root secondary before s with primary p.
func (e *rootElements) secondaryBefore(p, s uint32) uint32 {
	var list []uint32
	prev := uint32(0)
	if p == 0 {
		list = e.secondaryCEs
	} else {
		list = e.combos(p)
		prev = collation.BeforeWeight16
	}
	for _, st := range list {
		if st>>16 >= s {
			break
		}
		prev = st >> 16
	}
	return prev
}

// tertiaryBefore is the root tertiary before t with p and s.
func (e *rootElements) tertiaryBefore(p, s, t uint32) uint32 {
	var list []uint32
	prev := uint32(collation.BeforeWeight16)
	switch {
	case p != 0:
		list = e.combos(p)
	case s != 0:
		list = e.secondaryCEs
	default:
		prev = 0
		for _, x := range e.tertiaryCEs {
			if x >= t {
				break
			}
			prev = x
		}
		return prev
	}
	st := s<<16 | t
	for _, x := range list {
		if x >= st {
			break
		}
		if x>>16 == s {
			prev = x & 0xffff
		}
	}
	return prev
}
