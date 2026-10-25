package collation

import (
	"sort"
	"strings"
	"unicode"
)

// Root is the root collation: CLDR's ordering of every character before any
// language tailors it.
type Root struct {
	Table *Table
	// VariableTop is the last primary that `alternate: shifted` makes
	// ignorable: CLDR's maxVariable, punctuation.
	VariableTop uint32
	// Groups are the script reordering groups in root order, and GroupsEnd
	// the primary the last one ends before (the unassigned code points').
	Groups    []Group
	GroupsEnd uint32
	// Unified is the Unified_Ideograph ranges, inclusive, sorted.
	Unified [][2]rune
}

// Settings are how a collator compares, as a tailoring states them and as
// a caller changes them.
type Settings struct {
	// Strength is the last level compared: Primary to Quaternary, or
	// Identical.
	Strength int
	// Shifted makes variable characters (spaces and punctuation)
	// ignorable at the first three levels and compares them at the
	// quaternary level (UTS #10 "shifted").
	Shifted bool
	// CaseFirst orders upper case before lower case, or after, ahead of
	// the rest of the tertiary level.
	CaseFirst CaseFirst
	// CaseLevel compares case alone between the secondary and tertiary
	// levels.
	CaseLevel bool
	// Backwards compares secondary weights from the end of the string
	// (French as Canada orders it).
	Backwards bool
	// Numeric orders runs of decimal digits by their value.
	Numeric bool
}

// CaseFirst is how case orders at the tertiary level.
type CaseFirst uint8

const (
	// CaseFirstOff orders case as the tertiary weights do: lower case
	// first for most letters.
	CaseFirstOff CaseFirst = iota
	// CaseFirstLower puts lower case first, before any other tertiary
	// difference.
	CaseFirstLower
	// CaseFirstUpper puts upper case first.
	CaseFirstUpper
)

// DefaultSettings are the root collation's settings.
var DefaultSettings = Settings{Strength: Tertiary}

// Tailoring is a language's changes to the root: its own mappings, which
// replace the root's for every code point they start with, its settings,
// and its script order.
type Tailoring struct {
	Table    *Table
	Settings Settings
	// Reorder is the reorder codes, in order (`Grek`, `others`, `digit`).
	Reorder []string
}

// typeKeywords are the CLDR collation types BCP 47's `co` keyword names
// differently.
var typeKeywords = map[string]string{
	"phonebk": "phonebook", "trad": "traditional", "dict": "dictionary",
	"gb2312": "gb2312han", "big5han": "big5han",
}

// TypeOfKeyword is the CLDR collation type a `-u-co-` keyword value names
// (`phonebk` is `phonebook`).
func TypeOfKeyword(keyword string) string {
	if t, ok := typeKeywords[keyword]; ok {
		return t
	}
	return keyword
}

// unified reports whether r is a Unified_Ideograph, and whether it is one
// of the core blocks (CJK Unified Ideographs and the unified characters of
// CJK Compatibility Ideographs), which sort before the rest.
func (root *Root) unified(r rune) (han, core bool) {
	i := sort.Search(len(root.Unified), func(i int) bool { return root.Unified[i][1] >= r })
	if i == len(root.Unified) || root.Unified[i][0] > r {
		return false, false
	}
	return true, (r >= 0x4e00 && r <= 0x9fff) || (r >= 0xf900 && r <= 0xfaff)
}

// implicit is the CE the root gives a code point it does not list (UTS #10
// section 10.1.3): a Han ideograph's, core blocks first, or an unassigned
// code point's, each in code point order.
func (root *Root) implicit(r rune) CE {
	han, core := root.unified(r)
	var p uint32
	switch {
	case core:
		p = coreHanBase + uint32(r)<<coreHanShift
	case han:
		p = otherHanBase + uint32(r)<<otherHanShift
	default:
		p = UnassignedPrimary + uint32(r)
	}
	return PrimaryCE(p)
}

// Implicit is the CE the root gives r when it lists no mapping for it.
func (root *Root) Implicit(r rune) CE { return root.implicit(r) }

// PrimaryAfterImplicit is the smallest primary greater than p that the
// root could give another code point, for p an implicit primary: the
// limit a tailoring inserts below after a Han character.
func PrimaryAfterImplicit(p uint32) uint32 {
	switch {
	case p >= UnassignedPrimary:
		return p + 1
	case p >= otherHanBase:
		return p + 1<<otherHanShift
	}
	return p + 1<<coreHanShift
}

// digitValue is r's value when it is a decimal digit (general category
// Nd), which Unicode encodes in runs of ten from zero.
func digitValue(r rune) (int, bool) {
	if r < 0x80 {
		if r >= '0' && r <= '9' {
			return int(r - '0'), true
		}
		return 0, false
	}
	if !unicode.Is(unicode.Nd, r) {
		return 0, false
	}
	n := 0
	for unicode.Is(unicode.Nd, r-rune(n)-1) {
		n++
	}
	return n % 10, true
}

// reorderTable moves each script group's primaries to where a reordering
// puts the group.
type reorderTable struct {
	starts  []uint32
	offsets []int64
	end     uint32
}

// specialGroups are the reorder codes of the groups that stay before the
// scripts unless a reordering names them.
var specialGroups = []string{"space", "punct", "symbol", "currency", "digit"}

// codeAliases names the codes that stand for a group under another code.
var codeAliases = map[string]string{"Zzzz": "others", "Zyyy": "others"}

// newReorderTable lays the root's groups out in the order codes ask, as
// UTS #35 part 5 section 3.13 does: the special groups the codes do not
// name first, in root order; then the named groups, in their order, with
// `others` standing for every script group not named; and any script
// group left over after them.
func (root *Root) newReorderTable(codes []string) *reorderTable {
	if len(codes) == 0 || len(root.Groups) == 0 {
		return nil
	}
	index := map[string]int{}
	for i, g := range root.Groups {
		for _, c := range g.Codes {
			index[strings.ToLower(c)] = i
		}
	}
	isSpecial := map[int]bool{}
	for _, s := range specialGroups {
		if i, ok := index[s]; ok {
			isSpecial[i] = true
		}
	}
	named := map[int]bool{}
	var order []int
	sawOthers := false
	for _, c := range codes {
		if a, ok := codeAliases[c]; ok {
			c = a
		}
		if strings.EqualFold(c, "others") {
			sawOthers = true
			continue
		}
		if i, ok := index[strings.ToLower(c)]; ok {
			named[i] = true
		}
	}
	for i := range root.Groups {
		if isSpecial[i] && !named[i] {
			order = append(order, i)
		}
	}
	rest := func() {
		for i := range root.Groups {
			if !isSpecial[i] && !named[i] {
				order = append(order, i)
			}
		}
	}
	placed := map[int]bool{}
	for _, c := range codes {
		if a, ok := codeAliases[c]; ok {
			c = a
		}
		if strings.EqualFold(c, "others") {
			rest()
			continue
		}
		if i, ok := index[strings.ToLower(c)]; ok && !placed[i] {
			placed[i] = true
			order = append(order, i)
		}
	}
	if !sawOthers {
		rest()
	}
	end := func(i int) uint32 {
		if i+1 < len(root.Groups) {
			return root.Groups[i+1].Start
		}
		return root.GroupsEnd
	}
	t := &reorderTable{end: root.GroupsEnd}
	t.starts = make([]uint32, len(root.Groups))
	t.offsets = make([]int64, len(root.Groups))
	cursor := int64(root.Groups[0].Start)
	for _, i := range order {
		t.offsets[i] = cursor - int64(root.Groups[i].Start)
		cursor += int64(end(i) - root.Groups[i].Start)
	}
	for i, g := range root.Groups {
		t.starts[i] = g.Start
	}
	return t
}

func (t *reorderTable) apply(p uint32) uint32 {
	if t == nil || p < t.starts[0] || p >= t.end {
		return p
	}
	i := sort.Search(len(t.starts), func(i int) bool { return t.starts[i] > p }) - 1
	return uint32(int64(p) + t.offsets[i])
}
