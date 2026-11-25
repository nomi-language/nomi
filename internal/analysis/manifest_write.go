package analysis

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/parser"
)

// Writing nomi.toml's effects tables: the package doc, "nomi.toml".

// effectListWidth is the width a list is written on one line within.
const effectListWidth = 80

// The lists of the effects tables, as a change or an addition names
// them: the key's path below [effects], and for the entries
// (EntryLists) the table's path, beside the key of the entry. ListSelf and
// ListDeps name a whole table.
const (
	ListSelf           = "self"
	ListSelfStd        = "self.std"
	ListSelfStdInTests = "self.std_in_tests"
	ListSelfModules    = "self.modules"
	ListDeps           = "deps"
	ListDepsStd        = "deps.std"
	ListDepsStdInTests = "deps.std_in_tests"
	ListDepsPackages   = "deps.packages"
)

// EffectChange is one name a list gains or loses from one table to
// another: List is one of the List constants, Key the key of the entry
// for one of EntryLists, and Name the std name. Name is "" for a whole
// table (ListSelf,
// ListDeps), an entry with no names, and deps.std with no names, whose key
// one table has and the other lacks. In JSON the key is `module` for an
// entry of [effects.self.modules], whose keys are the project's modules,
// and `package` for one of [effects.deps.packages], whose keys are its
// dependencies (effectChangeJSON).
type EffectChange struct {
	Change string
	List   string
	Key    string
	Name   string
}

// effectChangeJSON is an EffectChange as JSON writes it, with the entry's
// key under the word for its table's keys.
type effectChangeJSON struct {
	Change  string `json:"change"`
	List    string `json:"list"`
	Module  string `json:"module,omitempty"`
	Package string `json:"package,omitempty"`
	Name    string `json:"name"`
}

func (c EffectChange) MarshalJSON() ([]byte, error) {
	j := effectChangeJSON{Change: c.Change, List: c.List, Name: c.Name}
	if c.List == ListDepsPackages {
		j.Package = c.Key
	} else {
		j.Module = c.Key
	}
	return json.Marshal(j)
}

func (c *EffectChange) UnmarshalJSON(data []byte) error {
	var j effectChangeJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return err
	}
	*c = EffectChange{Change: j.Change, List: j.List, Key: j.Module + j.Package, Name: j.Name}
	return nil
}

// Label is the list a change is to, as the report prints it.
func (c EffectChange) Label() string {
	if slices.Contains(EntryLists, c.List) {
		return fmt.Sprintf("%s %q", c.List, c.Key)
	}
	return c.List
}

// EffectChanges is what to add to and remove from old, nil for no
// tables, to make it to: by list, the names to remove after the names to
// add. A table one has and the other lacks is a change of its own, and so
// is deps.std when one has it with no names and the other lacks it, since
// a ceiling that allows nothing is not the same as none. entries_required
// is not compared.
func EffectChanges(old, to *Effects) []EffectChange {
	if old == nil {
		old = &Effects{}
	}
	if to == nil {
		to = &Effects{}
	}
	var adds, removes []EffectChange
	change := func(add bool, list, module, name string) {
		if add {
			adds = append(adds, EffectChange{Change: "add", List: list, Key: module, Name: name})
		} else {
			removes = append(removes, EffectChange{Change: "remove", List: list, Key: module, Name: name})
		}
	}
	diff := func(list, module string, from, into []string) {
		for _, n := range into {
			if !slices.Contains(from, n) {
				change(true, list, module, n)
			}
		}
		for _, n := range from {
			if !slices.Contains(into, n) {
				change(false, list, module, n)
			}
		}
	}
	// present is a key or a table one side has and the other lacks, where
	// the side that has it holds nothing a name would say.
	present := func(list string, had, has, empty bool) {
		if had != has && empty {
			change(has, list, "", "")
		}
	}
	// entries is each entry of list one side has and the other lacks, and
	// each name an entry both have gains or loses.
	entries := func(list string) {
		od, td := old.Entries(list), to.Entries(list)
		var keys []string
		for k := range od {
			keys = append(keys, k)
		}
		for k := range td {
			if _, ok := od[k]; !ok {
				keys = append(keys, k)
			}
		}
		slices.Sort(keys)
		for _, k := range keys {
			from, had := od[k]
			into, has := td[k]
			switch {
			case !had && len(into) == 0:
				change(true, list, k, "")
			case !has && len(from) == 0:
				change(false, list, k, "")
			default:
				diff(list, k, from, into)
			}
		}
	}
	present(ListSelf, old.Self != nil, to.Self != nil, true)
	os, ts := cmpSelf(old.Self), cmpSelf(to.Self)
	diff(ListSelfStd, "", os.Std, ts.Std)
	diff(ListSelfStdInTests, "", os.StdInTests, ts.StdInTests)
	entries(ListSelfModules)
	present(ListDeps, old.Deps != nil, to.Deps != nil, true)
	od, td := cmpDeps(old.Deps), cmpDeps(to.Deps)
	present(ListDepsStd, od.Std != nil, td.Std != nil, len(od.Std)+len(td.Std) == 0)
	diff(ListDepsStd, "", od.Std, td.Std)
	diff(ListDepsStdInTests, "", od.StdInTests, td.StdInTests)
	entries(ListDepsPackages)
	return append(adds, removes...)
}

// cmpSelf is s, or for no table one that holds nothing.
func cmpSelf(s *SelfEffects) *SelfEffects {
	if s == nil {
		return &SelfEffects{}
	}
	return s
}

// cmpDeps is d, or for no table one that holds nothing.
func cmpDeps(d *DepsEffects) *DepsEffects {
	if d == nil {
		return &DepsEffects{}
	}
	return d
}

// effectTable is one of the tables WriteEffectTables writes: its
// path, and its lines as written, nil when it is not written.
type effectTable struct {
	path []string
	at   ManifestTable
	text []string
}

// WriteEffectTables is data, the nomi.toml in dir, with its
// [effects.self], [effects.self.modules], [effects.deps]
// and [effects.deps.packages] tables written as c
// (writeEffectTable), and the text of those tables as written. Every
// line outside them is kept byte for byte. A table c has and data lacks
// goes after the table before it, or before the first table data has, or
// at the end of the file when data has none; a table data has and c lacks
// goes. [effects.self.modules] is written when c has the table, or
// data has it; [effects.deps.packages] when c has an entry, or data
// has the table. A file with CRLF line endings gets them on each line of
// the tables. The edited text must parse back to c, or nothing is
// written.
func WriteEffectTables(dir string, data []byte, c *Effects) ([]byte, string, error) {
	was, err := ParseManifestIn(dir, data)
	if err != nil {
		return nil, "", fmt.Errorf("the file does not parse (%v); edit its [effects] tables by hand", err)
	}
	old := was.Effects
	if old == nil {
		old = &Effects{}
	}
	text := string(data)
	cr := ""
	if strings.Contains(text, "\r\n") {
		cr = "\r"
	}
	lines := strings.Split(text, "\n")
	tables := []effectTable{
		{path: []string{"effects", "self"}},
		{path: []string{"effects", "self", "modules"}},
		{path: []string{"effects", "deps"}},
		{path: []string{"effects", "deps", "packages"}},
	}
	for i := range tables {
		tables[i].at = ManifestTableLayout(data, tables[i].path...)
	}
	header := func(t effectTable) string { return "[" + strings.Join(t.path, ".") + "]" }
	// entries writes the table of list's entries, tables[i].
	entries := func(i int, list string) {
		tables[i].text = writeEffectTable(lines, tables[i].at, header(tables[i]), entryKeys(c.Entries(list), old.Entries(list)))
	}
	if c.Self != nil {
		tables[0].text = writeEffectTable(lines, tables[0].at, header(tables[0]), selfKeys(c.Self, cmpSelf(old.Self)))
		if c.Self.Modules != nil || tables[1].at.Header > 0 {
			entries(1, ListSelfModules)
		}
	}
	if c.Deps != nil {
		tables[2].text = writeEffectTable(lines, tables[2].at, header(tables[2]), depsKeys(c.Deps, cmpDeps(old.Deps)))
		if len(c.Deps.Packages) > 0 || tables[3].at.Header > 0 {
			entries(3, ListDepsPackages)
		}
	}
	// Each table the file has is replaced by its lines, with the tables
	// the file lacks that follow it, and the first by those before it too.
	joined := func(a, b []string) []string {
		if len(a) == 0 || len(b) == 0 {
			return slices.Concat(a, b)
		}
		return slices.Concat(a, []string{""}, b)
	}
	var written, pending []string
	replace := map[int][]string{}
	last := -1
	for i, t := range tables {
		written = joined(written, t.text)
		switch {
		case t.at.Header > 0 && last < 0:
			replace[i], pending = joined(pending, t.text), nil
			last = i
		case t.at.Header > 0:
			replace[i] = t.text
			last = i
		case last < 0:
			pending = joined(pending, t.text)
		default:
			replace[last] = joined(replace[last], t.text)
		}
	}
	ended := func(table []string) []string {
		out := make([]string, len(table))
		for i, line := range table {
			out[i] = line + cr
		}
		return out
	}
	if last < 0 {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += cr + "\n"
		}
		if text != "" {
			text += cr + "\n"
		}
		lines = strings.Split(text+strings.Join(ended(pending), "\n")+cr+"\n", "\n")
	}
	// The later tables first, so the earlier ones' lines stay put.
	order := make([]int, 0, len(replace))
	for i := range replace {
		order = append(order, i)
	}
	slices.SortFunc(order, func(a, b int) int { return tables[b].at.Header - tables[a].at.Header })
	for _, i := range order {
		at := tables[i].at
		start := at.Header - 1
		if len(replace[i]) == 0 {
			// A table that goes takes the blank lines above it along.
			for start > 0 && isBlank(lines[start-1]) {
				start--
			}
		}
		lines = slices.Concat(lines[:start], ended(replace[i]), lines[at.Last:])
	}
	edited := []byte(strings.Join(lines, "\n"))
	m, err := ParseManifestIn(dir, edited)
	if err != nil {
		return nil, "", fmt.Errorf("the edited file does not parse (%v); edit its [effects] tables by hand", err)
	}
	if len(EffectChanges(m.Effects, c)) > 0 {
		return nil, "", errors.New("the edited file does not hold the tables written; edit its [effects] tables by hand")
	}
	return edited, strings.Join(written, "\n") + "\n", nil
}

// tableKey is a key --write writes in a table: its path as
// ManifestLines.Key holds it and its text when it is written new, then
// the names its list holds and held.
type tableKey struct {
	path, key  string
	names, was []string
	// kept is a key --write never adds and keeps as the file has it:
	// entries_required.
	kept bool
}

// selfKeys is what [effects.self] holds as c: entries_required as the
// file has it, std, and std_in_tests when it holds a name.
func selfKeys(c, old *SelfEffects) []tableKey {
	keys := []tableKey{{path: "entries_required", kept: true}, {path: "std", names: c.Std, was: old.Std}}
	if len(c.StdInTests) > 0 {
		keys = append(keys, tableKey{path: "std_in_tests", names: c.StdInTests, was: old.StdInTests})
	}
	return named(keys)
}

// depsKeys is what [effects.deps] holds as c: entries_required as the
// file has it, std when c has a ceiling, and std_in_tests when it holds a
// name.
func depsKeys(c, old *DepsEffects) []tableKey {
	keys := []tableKey{{path: "entries_required", kept: true}}
	if c.Std != nil {
		keys = append(keys, tableKey{path: "std", names: c.Std, was: old.Std})
	}
	if len(c.StdInTests) > 0 {
		keys = append(keys, tableKey{path: "std_in_tests", names: c.StdInTests, was: old.StdInTests})
	}
	return named(keys)
}

// named is keys, each written new as its path.
func named(keys []tableKey) []tableKey {
	for i := range keys {
		keys[i].key = keys[i].path
	}
	return keys
}

// entryKeys is what a table of entries holds as entries, which held old:
// an entry per key, in key order.
func entryKeys(entries, old map[string][]string) []tableKey {
	paths := make([]string, 0, len(entries))
	for p := range entries {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	keys := make([]tableKey, len(paths))
	for i, p := range paths {
		keys[i] = tableKey{path: p, key: moduleKey(p), names: entries[p], was: old[p]}
	}
	return keys
}

// moduleKey is an entry's key as a table of entries writes it new.
func moduleKey(module string) string { return fmt.Sprintf("%q", module) }

// ModuleEntryText is module's entry in a table of entries
// ([effects.self.modules], [effects.deps.packages]) holding
// names, as the tables write an entry the file lacks
// (writeEffectTable): `"pg" = ["std::fs"]`, one name a
// line when it does not fit on one.
func ModuleEntryText(module string, names []string) string {
	return strings.Join(tomlList(moduleKey(module)+" = ", names, ""), "\n")
}

// writeEffectTable is the lines of one table, at in lines, as --write
// writes it with keys: its header as the file has it, then its body in the
// file's order (keptRuns). A key keys holds keeps the comment lines
// directly above it and the comment on its line; its lines are as they
// were when its value is the same, and its list is written again when not
// (writeListValue). A key keys does not hold goes, with its comments. Then
// each key the file lacks, in keys' order, with no comment; after a blank
// line when a blank line separates the keys the table has.
func writeEffectTable(lines []string, at ManifestTable, header string, keys []tableKey) []string {
	out := []string{header}
	if at.Header > 0 {
		out[0] = strings.TrimSuffix(lines[at.Header-1], "\r")
	}
	written := map[string]bool{}
	spaced := false
	out = append(out, keptRuns(lines, at.Body, func(run ManifestLines) ([]string, bool) {
		i := slices.IndexFunc(keys, func(k tableKey) bool { return len(run.Key) == 1 && k.path == run.Key[0] })
		if i < 0 {
			return nil, false
		}
		k := keys[i]
		written[k.path] = true
		if k.kept || sameNames(k.was, k.names) {
			return runLines(lines, run.First, run.Last), true
		}
		return writeListValue(lines, run, k.was, k.names), true
	})...)
	for _, run := range at.Body {
		if run.Key == nil && slices.ContainsFunc(runLines(lines, run.First, run.Last), isBlank) {
			spaced = true
		}
	}
	var added []string
	for _, k := range keys {
		if !written[k.path] && !k.kept {
			added = append(added, tomlList(k.key+" = ", k.names, "")...)
		}
	}
	if spaced && len(added) > 0 && len(out) > 1 {
		out = append(out, "")
	}
	return append(out, added...)
}

// keptRuns is runs, a table's body or a list's elements, as written: a key
// or an element is write's lines, or nothing when write drops it with its
// comments; a comment no key owns stays where it is; and a run of blank
// lines stays when a line of runs is written after it. Two runs of blank
// lines that dropping a key brings together are the first.
func keptRuns(lines []string, runs []ManifestLines, write func(ManifestLines) ([]string, bool)) []string {
	var out, blank []string
	blankEnds := 0
	text := func(more ...string) {
		out = append(slices.Concat(out, blank), more...)
		blank = nil
	}
	for _, run := range runs {
		if run.Key != nil {
			if more, ok := write(run); ok {
				text(more...)
			}
			continue
		}
		for line := run.First; line <= run.Last; line++ {
			l := strings.TrimSuffix(lines[line-1], "\r")
			switch {
			case !isBlank(l):
				text(l)
			case blank == nil || blankEnds == line-1:
				blank, blankEnds = append(blank, l), line
			}
		}
	}
	return out
}

// writeListValue is a key's lines, run in lines, with its list written as
// names, which held was: the comment lines above it and the text around
// the value stay. A list written one element per line keeps each element
// names holds, with its comments, and gets the others at its end, one per
// line. Any other list is written again whole, the names it keeps in its
// order and then the others, on one line when it fits; a comment inside
// it goes.
func writeListValue(lines []string, run ManifestLines, was, names []string) []string {
	out := runLines(lines, run.First, run.Line-1)
	var added []string
	for _, n := range names {
		if !slices.Contains(was, n) {
			added = append(added, n)
		}
	}
	first := strings.TrimSuffix(lines[run.Line-1], "\r")
	last := strings.TrimSuffix(lines[run.Last-1], "\r")
	if run.Elements != nil {
		indent := "    "
		out = append(out, first)
		out = append(out, keptRuns(lines, run.Elements, func(e ManifestLines) ([]string, bool) {
			if !slices.Contains(names, e.Key[0]) {
				return nil, false
			}
			indent = lines[e.Line-1][:e.ValueStart]
			return runLines(lines, e.First, e.Last), true
		})...)
		for _, n := range added {
			out = append(out, fmt.Sprintf("%s%q,", indent, n))
		}
		return append(out, last)
	}
	var order []string
	for _, n := range was {
		if slices.Contains(names, n) {
			order = append(order, n)
		}
	}
	return append(out, tomlList(first[:run.ValueStart], append(order, added...), last[run.ValueEnd:])...)
}

// runLines is lines first..last (1-based, inclusive), each without a
// carriage return.
func runLines(lines []string, first, last int) []string {
	out := make([]string, 0, max(0, last-first+1))
	for _, l := range lines[first-1 : max(first-1, last)] {
		out = append(out, strings.TrimSuffix(l, "\r"))
	}
	return out
}

func isBlank(line string) bool { return strings.TrimSpace(line) == "" }

// sameNames reports whether a and b hold the same names.
func sameNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, n := range a {
		if !slices.Contains(b, n) {
			return false
		}
	}
	return true
}

// tomlList is a list of names after prefix (`std = `) and before suffix,
// a comment: on one line when that fits in effectListWidth, and with one
// name per line, the suffix on the first, when not.
func tomlList(prefix string, names []string, suffix string) []string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = fmt.Sprintf("%q", n)
	}
	one := prefix + "[" + strings.Join(quoted, ", ") + "]" + suffix
	if len(one) <= effectListWidth || len(names) < 2 {
		return []string{one}
	}
	out := []string{prefix + "[" + suffix}
	for _, q := range quoted {
		out = append(out, "    "+q+",")
	}
	return append(out, "]")
}

// Clone is a copy of c that shares no list with it. A list or table c
// leaves out stays left out, and Deps' Modules is a map.
func (c *Effects) Clone() *Effects {
	out := &Effects{}
	cloneEntries := func(entries map[string][]string) map[string][]string {
		out := make(map[string][]string, len(entries))
		for m, names := range entries {
			out[m] = slices.Clone(names)
		}
		return out
	}
	if c.Self != nil {
		s := *c.Self
		s.Std, s.StdInTests = slices.Clone(c.Self.Std), slices.Clone(c.Self.StdInTests)
		if c.Self.Modules != nil {
			s.Modules = cloneEntries(c.Self.Modules)
		}
		out.Self = &s
	}
	if c.Deps != nil {
		d := *c.Deps
		d.Std, d.StdInTests = slices.Clone(c.Deps.Std), slices.Clone(c.Deps.StdInTests)
		d.Packages = cloneEntries(c.Deps.Packages)
		out.Deps = &d
	}
	return out
}

// SetEntry sets the entry for key in list's table of entries (EntryLists)
// to names, giving c the table, and its own table above it, when it has
// neither.
func (c *Effects) SetEntry(list, key string, names []string) {
	switch list {
	case ListSelfModules:
		if c.Self == nil {
			c.Self = &SelfEffects{}
		}
		if c.Self.Modules == nil {
			c.Self.Modules = map[string][]string{}
		}
		c.Self.Modules[key] = names
	case ListDepsPackages:
		if c.Deps == nil {
			c.Deps = &DepsEffects{}
		}
		if c.Deps.Packages == nil {
			c.Deps.Packages = map[string][]string{}
		}
		c.Deps.Packages[key] = names
	}
}

// EffectAdd is one name a fix adds to one list of the effect
// tables: List is one of the List constants for a list, Module the key of
// the entry for one of EntryLists, and Name the std name.
type EffectAdd struct {
	List, Module, Name string
}

// AllowEffects is the fix titled title that adds each of adds to its
// list in data, the nomi.toml at path whose effects tables are c: the
// tables as WriteEffectTables writes them with the names added, as
// one edit over the lines that change, applied to path (parser.Fix's
// Path). A name its list already holds adds nothing; nil when nothing is
// added.
func AllowEffects(path string, data []byte, c *Effects, title string, adds []EffectAdd) (*parser.Fix, error) {
	if c == nil {
		return nil, errors.New("the file has no [effects] tables")
	}
	to := c.Clone()
	added := false
	add := func(list *[]string, name string) {
		if !slices.Contains(*list, name) {
			added = true
			*list = append(*list, name)
		}
	}
	for _, a := range adds {
		isSelf := strings.HasPrefix(a.List, ListSelf+".")
		if isSelf && to.Self == nil {
			to.Self = &SelfEffects{}
		}
		if !isSelf && to.Deps == nil {
			to.Deps = &DepsEffects{Packages: map[string][]string{}}
		}
		switch a.List {
		case ListSelfStd:
			add(&to.Self.Std, a.Name)
		case ListSelfStdInTests:
			add(&to.Self.StdInTests, a.Name)
		case ListDepsStd:
			add(&to.Deps.Std, a.Name)
		case ListDepsStdInTests:
			add(&to.Deps.StdInTests, a.Name)
		case ListSelfModules, ListDepsPackages:
			entry := slices.Clone(to.Entries(a.List)[a.Module])
			add(&entry, a.Name)
			to.SetEntry(a.List, a.Module, entry)
		default:
			return nil, fmt.Errorf("[effects] has no list %q", a.List)
		}
	}
	if !added {
		return nil, nil
	}
	return EffectsFix(path, data, title, to)
}

// EffectsFix is the fix titled title that writes to as the effect
// tables of data, the nomi.toml at path (WriteEffectTables), as one
// edit over the lines that change, applied to path (parser.Fix's Path).
func EffectsFix(path string, data []byte, title string, to *Effects) (*parser.Fix, error) {
	edited, _, err := WriteEffectTables(filepath.Dir(path), data, to)
	if err != nil {
		return nil, err
	}
	return &parser.Fix{Title: title, Path: path, Edits: []parser.Edit{lineEdit(data, edited)}}, nil
}

// lineEdit is the one edit that turns old into new, over whole lines: the
// lines from the first that differs to the last that differs, counted
// from each end.
func lineEdit(old, new []byte) parser.Edit {
	a, b := strings.SplitAfter(string(old), "\n"), strings.SplitAfter(string(new), "\n")
	first := 0
	for first < len(a) && first < len(b) && a[first] == b[first] {
		first++
	}
	lastA, lastB := len(a), len(b)
	for lastA > first && lastB > first && a[lastA-1] == b[lastB-1] {
		lastA, lastB = lastA-1, lastB-1
	}
	start := parser.Pos{Line: first + 1, Col: 1}
	end := parser.Pos{Line: lastA + 1, Col: 1}
	if lastA == len(a) && lastA > 0 && !strings.HasSuffix(a[lastA-1], "\n") {
		// The change reaches the old text's last line, which has no line
		// break: the edit ends at that line's end.
		end = parser.Pos{Line: lastA, Col: len(a[lastA-1]) + 1}
	}
	return parser.Edit{Start: start, End: end, Text: strings.Join(b[first:lastB], "")}
}
