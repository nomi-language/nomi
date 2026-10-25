package collation

import (
	"sort"
	"unicode/utf8"
)

// A Mapping maps a string, in NFD, to its CEs: a single code point, or a
// contraction of several. With a Prefix, it applies only where the string
// follows the prefix (a prefix context, as Japanese tailors the length mark
// after each vowel).
type Mapping struct {
	Prefix string
	Str    string
	CEs    []CE
}

// Table is a set of mappings, keyed by each string's first code point: the
// root collation's, or a tailoring's.
type Table struct {
	entries map[rune]*entry
}

// entry is every mapping whose string starts with one code point.
type entry struct {
	// ces is the code point's own CEs, nil when it maps to nothing here.
	ces []CE
	// contractions maps each longer string starting with the code point
	// to its CEs, and starts holds every proper prefix of one (the code
	// point itself excluded), so a match can keep reading.
	contractions map[string][]CE
	starts       map[string]bool
	// prefixed is the mappings that apply after a prefix, longest prefix
	// first; each holds its own CEs and contractions.
	prefixed []prefixed
}

type prefixed struct {
	prefix []rune
	e      *entry
}

// NewTable builds a table of mappings.
func NewTable(ms []Mapping) *Table {
	t := &Table{entries: map[rune]*entry{}}
	for _, m := range ms {
		t.add(m)
	}
	for _, e := range t.entries {
		sort.SliceStable(e.prefixed, func(i, j int) bool { return len(e.prefixed[i].prefix) > len(e.prefixed[j].prefix) })
	}
	return t
}

func (t *Table) add(m Mapping) {
	first, size := utf8.DecodeRuneInString(m.Str)
	e := t.entries[first]
	if e == nil {
		e = &entry{}
		t.entries[first] = e
	}
	if m.Prefix != "" {
		p := []rune(m.Prefix)
		var pe *entry
		for _, x := range e.prefixed {
			if string(x.prefix) == m.Prefix {
				pe = x.e
			}
		}
		if pe == nil {
			pe = &entry{}
			e.prefixed = append(e.prefixed, prefixed{p, pe})
		}
		e = pe
	}
	if size == len(m.Str) {
		e.ces = m.CEs
		return
	}
	if e.contractions == nil {
		e.contractions, e.starts = map[string][]CE{}, map[string]bool{}
	}
	e.contractions[m.Str] = m.CEs
	for i := size; i < len(m.Str); {
		_, n := utf8.DecodeRuneInString(m.Str[i:])
		i += n
		if i < len(m.Str) {
			e.starts[m.Str[:i]] = true
		}
	}
}

// Set replaces every mapping whose string starts with r by ms, each of
// which must start with r. It is for building a tailoring.
func (t *Table) Set(r rune, ms []Mapping) {
	delete(t.entries, r)
	for _, m := range ms {
		t.add(m)
	}
	if e := t.entries[r]; e != nil {
		sort.SliceStable(e.prefixed, func(i, j int) bool { return len(e.prefixed[i].prefix) > len(e.prefixed[j].prefix) })
	}
}

// MappingsOf is every mapping whose string starts with r.
func (t *Table) MappingsOf(r rune) []Mapping {
	e := t.entries[r]
	if e == nil {
		return nil
	}
	sub := &Table{entries: map[rune]*entry{r: e}}
	return sub.Mappings()
}

// Mappings is every mapping of the table, sorted by prefix and string.
func (t *Table) Mappings() []Mapping {
	var out []Mapping
	var walk func(prefix string, first rune, e *entry)
	walk = func(prefix string, first rune, e *entry) {
		if e.ces != nil {
			out = append(out, Mapping{Prefix: prefix, Str: string(first), CEs: e.ces})
		}
		for s, ces := range e.contractions {
			out = append(out, Mapping{Prefix: prefix, Str: s, CEs: ces})
		}
		for _, p := range e.prefixed {
			walk(string(p.prefix), first, p.e)
		}
	}
	for r, e := range t.entries {
		walk("", r, e)
	}
	sortMappings(out)
	return out
}

func sortMappings(ms []Mapping) {
	sort.Slice(ms, func(i, j int) bool {
		if ms[i].Str != ms[j].Str {
			return ms[i].Str < ms[j].Str
		}
		return ms[i].Prefix < ms[j].Prefix
	})
}

// Has reports whether the table maps anything starting with r.
func (t *Table) Has(r rune) bool { return t != nil && t.entries[r] != nil }

// Single is the CEs the table maps r alone to, with no prefix, and whether
// it maps it.
func (t *Table) Single(r rune) ([]CE, bool) {
	if t == nil {
		return nil, false
	}
	e := t.entries[r]
	if e == nil || e.ces == nil {
		return nil, false
	}
	return e.ces, true
}

// Len is the number of code points the table has an entry for.
func (t *Table) Len() int { return len(t.entries) }
