package collation

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// The binary forms of a root and a tailoring, which internal/cldrgen writes
// into the locale data and Decode reads back. Every number is a varint.
//
//	root:      "NCR" format  variableTop  groups  unified  mappings
//	tailoring: "NCT" format  settings  reorder  mappings
//
// groups is a count and, per script group in root order, its first primary
// and its reorder codes; unified is the Unified_Ideograph ranges as pairs;
// mappings is a count and the mappings in CE order, each a header (its CE
// count, whether it has a prefix, and its code point count), its string's
// first code point as a difference from the last one's, its other code
// points, its prefix, and its CEs, each CE's primary as a difference from
// the last CE's and its low 32 bits only when they change. A mapping to the
// last CE with the next primary, as most of a tailoring's long lists are,
// writes a CE count of zero and no CE.
const formatVersion = 1

// maxHeaderCEs is the most CEs a mapping's header can count.
const maxHeaderCEs = 63

type writer struct{ b []byte }

func (w *writer) uint(v uint64)   { w.b = binary.AppendUvarint(w.b, v) }
func (w *writer) int(v int64)     { w.b = binary.AppendVarint(w.b, v) }
func (w *writer) str(s string)    { w.uint(uint64(len(s))); w.b = append(w.b, s...) }
func (w *writer) bool(v bool)     { w.uint(map[bool]uint64{false: 0, true: 1}[v]) }
func (w *writer) strs(s []string) { w.str(strings.Join(s, " ")) }

type reader struct {
	b   []byte
	err error
}

var errTruncated = errors.New("collation data is truncated")

func (r *reader) uint() uint64 {
	v, n := binary.Uvarint(r.b)
	if n <= 0 {
		r.fail()
		return 0
	}
	r.b = r.b[n:]
	return v
}

func (r *reader) int() int64 {
	v, n := binary.Varint(r.b)
	if n <= 0 {
		r.fail()
		return 0
	}
	r.b = r.b[n:]
	return v
}

func (r *reader) fail() {
	if r.err == nil {
		r.err = errTruncated
	}
	r.b = nil
}

func (r *reader) str() string {
	n := r.uint()
	if uint64(len(r.b)) < n {
		r.fail()
		return ""
	}
	s := string(r.b[:n])
	r.b = r.b[n:]
	return s
}

func (r *reader) bool() bool { return r.uint() != 0 }

func (r *reader) strs() []string {
	s := r.str()
	if s == "" {
		return nil
	}
	return strings.Split(s, " ")
}

func (w *writer) mappings(ms []Mapping) {
	ms = append([]Mapping(nil), ms...)
	// In CE order each CE differs little from the one before it, which
	// keeps the differences short.
	sort.SliceStable(ms, func(i, j int) bool {
		a, b := ms[i].CEs, ms[j].CEs
		if len(a) > 0 && len(b) > 0 && a[0] != b[0] {
			return a[0] < b[0]
		}
		if ms[i].Str != ms[j].Str {
			return ms[i].Str < ms[j].Str
		}
		return ms[i].Prefix < ms[j].Prefix
	})
	w.uint(uint64(len(ms)))
	var lastRune rune
	var lastP uint32
	var lastLow uint32 = CommonSecTer
	for _, m := range ms {
		rs := []rune(m.Str)
		ps := []rune(m.Prefix)
		// The header packs the CE count (0: one CE, the last one's primary
		// plus one), whether there is a prefix, and the code points less
		// one.
		n := uint64(len(m.CEs))
		if n > maxHeaderCEs {
			panic("collation: a mapping to too many CEs")
		}
		next := len(m.CEs) == 1 && m.CEs[0].Primary() == lastP+1 && lastP != 0 && uint32(m.CEs[0]) == lastLow
		if next {
			n = 0
		}
		if len(ps) > 0 {
			n |= 1 << 6
		}
		w.uint(n | uint64(len(rs)-1)<<7)
		w.int(int64(rs[0] - lastRune))
		lastRune = rs[0]
		for _, r := range rs[1:] {
			w.uint(uint64(r))
		}
		if len(ps) > 0 {
			w.uint(uint64(len(ps)))
			for _, r := range ps {
				w.uint(uint64(r))
			}
		}
		if next {
			lastP++
			continue
		}
		for _, ce := range m.CEs {
			d := int64(ce.Primary()) - int64(lastP)
			zz := uint64(d<<1) ^ uint64(d>>63)
			low := uint32(ce)
			changed := low != lastLow
			v := zz << 1
			if changed {
				v |= 1
			}
			w.uint(v)
			if changed {
				w.uint(uint64(low))
			}
			lastP, lastLow = ce.Primary(), low
		}
	}
}

func (r *reader) mappings() []Mapping {
	n := r.uint()
	if n > uint64(len(r.b)) {
		r.fail()
		return nil
	}
	ms := make([]Mapping, 0, n)
	var lastRune rune
	var lastP uint32
	var lastLow uint32 = CommonSecTer
	var pool []CE
	for i := uint64(0); i < n && r.err == nil; i++ {
		var sb strings.Builder
		h := r.uint()
		nc, hasPrefix, k := h&maxHeaderCEs, h&(1<<6) != 0, h>>7+1
		lastRune += rune(r.int())
		sb.WriteRune(lastRune)
		for j := uint64(1); j < k && r.err == nil; j++ {
			sb.WriteRune(rune(r.uint()))
		}
		var pb strings.Builder
		if hasPrefix {
			pk := r.uint()
			for j := uint64(0); j < pk && r.err == nil; j++ {
				pb.WriteRune(rune(r.uint()))
			}
		}
		next := nc == 0
		if next {
			nc = 1
		}
		if len(pool) < int(nc) {
			pool = make([]CE, max(1024, int(nc)))
		}
		ces := pool[:nc:nc]
		pool = pool[nc:]
		if next {
			lastP++
			ces[0] = CE(uint64(lastP)<<32 | uint64(lastLow))
			ms = append(ms, Mapping{Prefix: pb.String(), Str: sb.String(), CEs: ces})
			continue
		}
		for j := range ces {
			v := r.uint()
			zz := v >> 1
			d := int64(zz>>1) ^ -int64(zz&1)
			lastP = uint32(int64(lastP) + d)
			if v&1 != 0 {
				lastLow = uint32(r.uint())
			}
			ces[j] = CE(uint64(lastP)<<32 | uint64(lastLow))
		}
		ms = append(ms, Mapping{Prefix: pb.String(), Str: sb.String(), CEs: ces})
	}
	return ms
}

// Group is a script reordering group of the root collation: the primaries
// from Start to the next group's Start, and the reorder codes that name it
// (`space`, `punct`, `symbol`, `currency`, `digit`, or the ISO 15924 codes
// of its scripts, `Latn`, `Hani Hans Hant`).
type Group struct {
	Start uint32
	Codes []string
}

// EncodeRoot writes a root collation.
func EncodeRoot(r *Root) []byte {
	w := &writer{b: []byte("NCR")}
	w.uint(formatVersion)
	w.uint(uint64(r.VariableTop))
	w.uint(uint64(r.GroupsEnd))
	w.uint(uint64(len(r.Groups)))
	for _, g := range r.Groups {
		w.uint(uint64(g.Start))
		w.strs(g.Codes)
	}
	w.uint(uint64(len(r.Unified)))
	for _, u := range r.Unified {
		w.uint(uint64(u[0]))
		w.uint(uint64(u[1] - u[0]))
	}
	w.mappings(r.Table.Mappings())
	return w.b
}

// DecodeRoot reads a root collation EncodeRoot wrote.
func DecodeRoot(b []byte) (*Root, error) {
	if len(b) < 3 || string(b[:3]) != "NCR" {
		return nil, fmt.Errorf("not a root collation")
	}
	r := &reader{b: b[3:]}
	if v := r.uint(); v != formatVersion {
		return nil, fmt.Errorf("root collation format %d, not %d", v, formatVersion)
	}
	root := &Root{VariableTop: uint32(r.uint()), GroupsEnd: uint32(r.uint())}
	ng := r.uint()
	for i := uint64(0); i < ng && r.err == nil; i++ {
		root.Groups = append(root.Groups, Group{Start: uint32(r.uint()), Codes: r.strs()})
	}
	nu := r.uint()
	for i := uint64(0); i < nu && r.err == nil; i++ {
		lo := rune(r.uint())
		root.Unified = append(root.Unified, [2]rune{lo, lo + rune(r.uint())})
	}
	ms := r.mappings()
	if r.err != nil {
		return nil, r.err
	}
	root.Table = NewTable(ms)
	return root, nil
}

// EncodeTailoring writes a tailoring.
func EncodeTailoring(t *Tailoring) []byte {
	w := &writer{b: []byte("NCT")}
	w.uint(formatVersion)
	s := t.Settings
	w.uint(uint64(s.Strength))
	w.bool(s.Shifted)
	w.uint(uint64(s.CaseFirst))
	w.bool(s.CaseLevel)
	w.bool(s.Backwards)
	w.bool(s.Numeric)
	w.strs(t.Reorder)
	w.mappings(t.Table.Mappings())
	return w.b
}

// DecodeTailoring reads a tailoring EncodeTailoring wrote.
func DecodeTailoring(b []byte) (*Tailoring, error) {
	if len(b) < 3 || string(b[:3]) != "NCT" {
		return nil, fmt.Errorf("not a collation tailoring")
	}
	r := &reader{b: b[3:]}
	if v := r.uint(); v != formatVersion {
		return nil, fmt.Errorf("collation tailoring format %d, not %d", v, formatVersion)
	}
	t := &Tailoring{}
	t.Settings.Strength = int(r.uint())
	t.Settings.Shifted = r.bool()
	t.Settings.CaseFirst = CaseFirst(r.uint())
	t.Settings.CaseLevel = r.bool()
	t.Settings.Backwards = r.bool()
	t.Settings.Numeric = r.bool()
	t.Reorder = r.strs()
	ms := r.mappings()
	if r.err != nil {
		return nil, r.err
	}
	t.Table = NewTable(ms)
	return t, nil
}
