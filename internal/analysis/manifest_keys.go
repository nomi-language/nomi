package analysis

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/nomi-language/nomi/internal/pkgcache"
	"github.com/nomi-language/nomi/internal/suggest"
)

// The keys nomi.toml accepts are manifestFile's, and the keys
// nomi-workspace.toml accepts pkgcache.WorkspaceFile's (manifestSchema):
// each table is a field with a `toml` tag, each key a field of that
// table's struct, and a field at the top that is neither is a key outside
// any table (nomi-workspace.toml's `members`). A field whose
// type is a struct is a table nested in its own (`[effects.self]`),
// and a map's keys are free, as `[effects.deps.packages]`'s dependency
// names are. A table or key the manifest holds and manifestFile does not
// is an error at its line, so a misspelled key is never read as nothing.
// The error names the nearest key when one is close in spelling, except in
// a table whose field carries a `keys` tag: there it says what each key
// holds instead, since a key written wrong there is often close in
// spelling to one far from it in meaning (`allow_in_tests` and
// `std_in_tests`).
//
// A top-level table named for a package the project requires, or for the
// project's own package, belongs to that package's tools (`nomi tool`), which
// read it themselves: sqlkit's `[sqlkit]`. The compiler does not check it.

// manifestTable is one table manifestFile declares: its keys, and its
// field's `keys` tag, what each key holds, or "".
type manifestTable struct {
	keys  []string
	about string
}

// manifestSchema is the keys one kind of file accepts: its name, as an
// error names the file, and the struct it decodes into (manifestTables).
type manifestSchema struct {
	file  string
	shape reflect.Type
}

var (
	packageSchema   = manifestSchema{"nomi.toml", reflect.TypeOf(manifestFile{})}
	workspaceSchema = manifestSchema{pkgcache.WorkspaceName, reflect.TypeOf(pkgcache.WorkspaceFile{})}
)

// manifestTables is each table shape declares, by its path joined with
// ".", read from the struct's tags, and its keys outside any table under
// "".
func manifestTables(shape reflect.Type) map[string]manifestTable {
	out := map[string]manifestTable{}
	var tables func(prefix string, t reflect.Type)
	tables = func(prefix string, t reflect.Type) {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			path := f.Tag.Get("toml")
			if prefix != "" {
				path = prefix + "." + path
			}
			if ft.Kind() == reflect.Map && ft.Elem().Kind() == reflect.Struct {
				// A table of entries, each a table of the struct's keys
				// under a key of its own ([dependencies]): its keys are
				// free, and an entry's are checked at `<path>.*`.
				out[path] = manifestTable{about: f.Tag.Get("keys")}
				path, ft = path+".*", ft.Elem()
			} else if ft.Kind() == reflect.Map {
				// A table of values under free keys
				// ([effects.deps.packages]).
				out[path] = manifestTable{about: f.Tag.Get("keys")}
				continue
			}
			if ft.Kind() != reflect.Struct {
				if prefix == "" {
					top := out[""]
					top.keys = append(top.keys, path)
					out[""] = top
				}
				continue
			}
			table := manifestTable{about: f.Tag.Get("keys")}
			for j := 0; j < ft.NumField(); j++ {
				table.keys = append(table.keys, ft.Field(j).Tag.Get("toml"))
			}
			out[path] = table
			tables(path, ft)
		}
	}
	tables("", shape)
	return out
}

// unknownManifestKey is the error for the first key in undecoded (in the
// order the file writes them) that is not a tool's: a key outside any
// table, or a table, that schema's file has none of, or a key its table
// does not have, at its line, with the nearest known name when one is
// close in spelling.
func unknownManifestKey(data []byte, undecoded []toml.Key, md toml.MetaData, sourceLabel string, tools func() []string, schema manifestSchema) error {
	if len(undecoded) == 0 {
		return nil
	}
	tables := manifestTables(schema.shape)
	var toolNames []string
	asked := false
	for _, k := range undecoded {
		if _, known := tables[k[0]]; !known {
			if !asked && tools != nil {
				toolNames, asked = tools(), true
			}
			if slices.Contains(toolNames, k[0]) {
				continue
			}
		}
		at := &ManifestError{Path: sourceLabel, Line: ManifestKeyLine(data, k...)}
		if _, known := tables[k[0]]; !known {
			if top, keyed := tables[""]; len(k) == 1 && md.Type(k...) != "Hash" {
				switch near := suggest.Closest(k[0], top.keys); {
				case !keyed:
					return at.with("%s has no top-level key `%s`; every key is in a table (%s)", schema.file, k[0], tableList(tables))
				case near != "":
					return at.with("%s has no key `%s`; did you mean `%s`?", schema.file, k[0], near)
				}
				if len(topTables(tables)) == 0 {
					return at.with("%s has no key `%s`; its keys are %s", schema.file, k[0], keyList(top.keys))
				}
				return at.with("%s has no key `%s`; its keys are %s, and its tables %s", schema.file, k[0], keyList(top.keys), tableList(tables))
			}
			if near := suggest.Closest(k[0], topTables(tables)); near != "" {
				return at.with("%s has no [%s] table; did you mean [%s]?", schema.file, k[0], near)
			}
			if len(topTables(tables)) == 0 {
				return at.with("%s has no [%s] table; it has no tables, only the keys %s", schema.file, k[0], keyList(tables[""].keys))
			}
			return at.with("%s has no [%s] table; its tables are %s%s", schema.file, k[0], tableList(tables), toolTableList(tools, toolNames))
		}
		// The innermost known table the key is in; a key past its next
		// part is in a table nested in that one, which it declares none of.
		// A table of entries' key is free, and is `*` in its path.
		written := k
		k = slices.Clone(k)
		for i := 1; i < len(k)-1; i++ {
			if _, entries := tables[strings.Join(k[:i], ".")+".*"]; entries {
				k[i] = "*"
			}
		}
		n := 1
		for n < len(k)-1 {
			if _, known := tables[strings.Join(k[:n+1], ".")]; !known {
				break
			}
			n++
		}
		table := tables[strings.Join(k[:n], ".")]
		path, key := strings.Join(written[:n], "."), k[n]
		if table.about != "" {
			return at.with("[%s] has no key `%s`; %s", path, key, table.about)
		}
		if near := suggest.Closest(key, table.keys); near != "" && len(k) == n+1 {
			return at.with("[%s] has no key `%s`; did you mean `%s`?", path, key, near)
		}
		return at.with("[%s] has no key `%s`; its keys are %s", path, key, keyList(table.keys))
	}
	return nil
}

// topTables is the names of the top-level tables among tables.
func topTables(tables map[string]manifestTable) []string {
	var names []string
	for name := range tables {
		if name != "" && !strings.Contains(name, ".") {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func tableList(tables map[string]manifestTable) string {
	names := topTables(tables)
	for i, name := range names {
		names[i] = "[" + name + "]"
	}
	return strings.Join(names, ", ")
}

// toolTableList ends the list of an unknown table's error with the tables
// that belong to tools (toolTables): by name, names, when the manifest
// names its package or a dependency, and otherwise what names one. It is
// "" for a manifest with no project (tools nil), which holds none.
func toolTableList(tools func() []string, names []string) string {
	if tools == nil {
		return ""
	}
	if len(names) == 0 {
		return ", and its tools' tables, each named for the package or a dependency"
	}
	bracketed := slices.Compact(slices.Sorted(slices.Values(names)))
	for i, name := range bracketed {
		bracketed[i] = "[" + name + "]"
	}
	return ", and its tools' tables " + joinAnd(bracketed)
}

func keyList(keys []string) string {
	quoted := make([]string, len(keys))
	for i, k := range keys {
		quoted[i] = "`" + k + "`"
	}
	return strings.Join(quoted, ", ")
}

// toolTables is the names of the tables in data, a manifest's text, that
// belong to a package's tools: the package's own `[package].name` and each key
// of its [dependencies], the name the project gives a package, which `nomi
// tool` runs its tools under and they read their table by. They are read
// from the text itself, so a table is a tool's in the text that names its
// dependency, whatever the file on disk holds, and reading them needs no
// package graph.
func toolTables(data []byte) []string {
	var raw struct {
		Package struct {
			Name string `toml:"name"`
		} `toml:"package"`
		Dependencies map[string]toml.Primitive `toml:"dependencies"`
	}
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return nil
	}
	var names []string
	if raw.Package.Name != "" {
		names = append(names, raw.Package.Name)
	}
	for name := range raw.Dependencies {
		names = append(names, name)
	}
	return names
}

// ManifestKeyLine is the line (1-based) of nomi.toml's text data that
// writes the key at path: a table's header, or the line of a key in its
// table; 0 when no line does. A key a header and a dotted key spell
// together (`[effects]` and `modules.x = []`) is found too.
func ManifestKeyLine(data []byte, path ...string) int {
	want := strings.Join(path, "\x00")
	var header []string
	tableLine := 0
	for i, line := range strings.Split(string(data), "\n") {
		text := strings.TrimSpace(stripTomlComment(line))
		if text == "" {
			continue
		}
		if strings.HasPrefix(text, "[") {
			inner := strings.Trim(text, "[]")
			header = splitTomlKey(inner)
			if strings.Join(header, "\x00") == want || tableLine == 0 && strings.HasPrefix(strings.Join(header, "\x00"), want+"\x00") {
				if strings.Join(header, "\x00") == want {
					return i + 1
				}
				tableLine = i + 1
			}
			continue
		}
		lhs, _, ok := strings.Cut(text, "=")
		if !ok {
			continue
		}
		full := append(slices.Clone(header), splitTomlKey(lhs)...)
		joined := strings.Join(full, "\x00")
		if joined == want {
			return i + 1
		}
		if tableLine == 0 && strings.HasPrefix(joined, want+"\x00") {
			tableLine = i + 1
		}
	}
	if tableLine == 0 && len(path) > 1 {
		// A key of an inline table (`a = { version = "v1.0.0" }`) is on
		// the line that writes the table.
		if at := ManifestKeyLine(data, path[:len(path)-1]...); at > 0 {
			if _, value, ok := strings.Cut(strings.Split(string(data), "\n")[at-1], "="); ok && strings.HasPrefix(strings.TrimSpace(value), "{") {
				return at
			}
		}
	}
	return tableLine
}

// ManifestTableAt is the path of the table line (1-based) of nomi.toml's
// text data is in: the last header above it, read as ManifestKeyLine reads
// one; nil above the first.
func ManifestTableAt(data []byte, line int) []string {
	var header []string
	for i, l := range strings.Split(string(data), "\n") {
		if i+1 >= line {
			break
		}
		if text := strings.TrimSpace(stripTomlComment(l)); strings.HasPrefix(text, "[") {
			header = splitTomlKey(strings.Trim(text, "[]"))
		}
	}
	return header
}

// ManifestTable is how nomi.toml's text writes one table: its header's
// line, its last line, and the runs of lines between them, each line
// 1-based.
type ManifestTable struct {
	// Header is the line of the table's `[header]`, and Last its last line
	// that holds more than a comment before the next header, so a comment
	// and the blank lines above the next table are not the table's. Both
	// are 0 when no header names the table.
	Header, Last int
	// Body is the lines after the header to Last, in order: each key with
	// its comments, and the runs between them.
	Body []ManifestLines
}

// ManifestLines is a run of lines in a table's body, or in a list written
// one element per line: a key or an element with its comments, or a run
// of blank lines and comments that no key owns.
type ManifestLines struct {
	// Key is the key's parts, unquoted, or a list element's string; nil
	// for a run no key owns.
	Key []string
	// First is the run's first line. For a key, it is the first of the
	// comment lines directly above the key's line, with no blank line
	// between, which belong to it; Line is the key's own line, and Last
	// the last line of its value, which ends the run.
	First, Line, Last int
	// ValueStart is the byte offset of the value on Line, and ValueEnd
	// the offset on Last just past it: what follows is blanks and a
	// comment, or for an element its comma first.
	ValueStart, ValueEnd int
	// Elements is the value's runs when it is a list written one element
	// per line, between a line that ends with its `[` and a line that
	// holds only its `]`: each element with the comment lines directly
	// above it, and the runs between them, as Body holds a table's keys.
	// It is nil for any other value, and empty, not nil, for such a list
	// with no element.
	Elements []ManifestLines
}

// ManifestTableLayout is how nomi.toml's text data writes the table at
// path (ManifestTable). With no path it is the keys written outside any
// table, before the first header, such as nomi-workspace.toml's members:
// Header is 0, and Body runs from the first line.
func ManifestTableLayout(data []byte, path ...string) ManifestTable {
	want := strings.Join(path, "\x00")
	lines := strings.Split(string(data), "\n")
	var t ManifestTable
	if len(path) == 0 {
		top := len(lines)
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(stripTomlComment(line)), "[") {
				top = i
				break
			}
		}
		t.Body = manifestRuns(lines, 1, top, false)
		for _, run := range t.Body {
			if run.Key != nil {
				t.Last = run.Last
			}
		}
		return t
	}
	for i, line := range lines {
		text := strings.TrimSpace(stripTomlComment(line))
		if text == "" {
			continue
		}
		if strings.HasPrefix(text, "[") {
			if t.Header > 0 {
				break
			}
			if strings.Join(splitTomlKey(strings.Trim(text, "[]")), "\x00") == want {
				t.Header, t.Last = i+1, i+1
			}
			continue
		}
		if t.Header > 0 {
			t.Last = i + 1
		}
	}
	if t.Header > 0 {
		t.Body = manifestRuns(lines, t.Header+1, t.Last, false)
	}
	return t
}

// manifestRuns is lines from..to (1-based, inclusive) as runs: keys, or
// with elements the elements of a list written one per line, each with
// the comment lines directly above it, and the runs of blank lines and
// comments between them. With elements, it is nil when a line holds
// anything but one string, its comma and a comment.
func manifestRuns(lines []string, from, to int, elements bool) []ManifestLines {
	runs := []ManifestLines{}
	gap := func(first, last int) {
		if first <= last {
			runs = append(runs, ManifestLines{First: first, Last: last})
		}
	}
	free := from  // the first line no run holds yet
	comments := 0 // the first of the comment lines directly above, or 0
	for line := from; line <= to; line++ {
		raw := lines[line-1]
		text := strings.TrimSpace(stripTomlComment(raw))
		if strings.TrimSpace(raw) == "" {
			comments = 0
			continue
		}
		if text == "" {
			if comments == 0 {
				comments = line
			}
			continue
		}
		first := line
		if comments > 0 {
			first = comments
		}
		gap(free, first-1)
		run := ManifestLines{First: first, Line: line, Last: line}
		if elements {
			value, end, ok := tomlString(raw)
			if !ok {
				return nil
			}
			run.Key, run.ValueStart, run.ValueEnd = []string{value}, len(raw)-len(strings.TrimLeft(raw, " \t")), end
		} else {
			lhs, _, _ := strings.Cut(text, "=")
			run.Key = splitTomlKey(lhs)
			if eq := strings.Index(raw, "="); eq >= 0 {
				rest := raw[eq+1:]
				run.ValueStart = eq + 1 + len(rest) - len(strings.TrimLeft(rest, " \t"))
			}
			for depth := tomlDepth(text); depth > 0 && run.Last < to; {
				run.Last++
				depth += tomlDepth(stripTomlComment(lines[run.Last-1]))
			}
			last := lines[run.Last-1]
			run.ValueEnd = len(strings.TrimRight(stripTomlComment(last), " \t\r"))
			if run.Last > run.Line && strings.HasSuffix(text, "[") && strings.TrimSpace(stripTomlComment(last)) == "]" {
				run.Elements = manifestRuns(lines, run.Line+1, run.Last-1, true)
			}
		}
		runs = append(runs, run)
		line, free, comments = run.Last, run.Last+1, 0
	}
	gap(free, to)
	return runs
}

// tomlDepth is how many more brackets text, a line without its comment,
// opens than it closes, outside strings.
func tomlDepth(text string) int {
	depth := 0
	quote := byte(0)
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case quote != 0 && c == '\\' && quote == '"':
			i++
		case quote != 0 && c == quote:
			quote = 0
		case quote != 0:
		case c == '"' || c == '\'':
			quote = c
		case c == '[' || c == '{':
			depth++
		case c == ']' || c == '}':
			depth--
		}
	}
	return depth
}

// tomlString is the string a list element written on its own line holds
// (`    "std::fs",`), and the byte offset just past it; ok only when
// nothing but blanks, one comma and a comment surround it.
func tomlString(line string) (value string, end int, ok bool) {
	start := len(line) - len(strings.TrimLeft(line, " \t"))
	if start == len(line) || line[start] != '"' && line[start] != '\'' {
		return "", 0, false
	}
	quote := line[start]
	end = -1
	for i := start + 1; i < len(line); i++ {
		if quote == '"' && line[i] == '\\' {
			i++
			continue
		}
		if line[i] == quote {
			end = i + 1
			break
		}
	}
	if end < 0 {
		return "", 0, false
	}
	if rest := strings.TrimSpace(stripTomlComment(line[end:])); rest != "" && rest != "," {
		return "", 0, false
	}
	if quote == '\'' {
		return line[start+1 : end-1], end, true
	}
	value, err := strconv.Unquote(line[start:end])
	return value, end, err == nil
}

// splitTomlKey is a dotted TOML key's parts, each unquoted.
func splitTomlKey(key string) []string {
	var parts []string
	var b strings.Builder
	quote := byte(0)
	for i := 0; i < len(key); i++ {
		c := key[i]
		switch {
		case quote != 0 && c == quote:
			quote = 0
		case quote != 0:
			b.WriteByte(c)
		case c == '"' || c == '\'':
			quote = c
		case c == '.':
			parts = append(parts, strings.TrimSpace(b.String()))
			b.Reset()
		case c == ' ' || c == '\t':
		default:
			b.WriteByte(c)
		}
	}
	return append(parts, strings.TrimSpace(b.String()))
}

// stripTomlComment is line without a `#` comment outside a string.
func stripTomlComment(line string) string {
	quote := byte(0)
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0 && c == '\\' && quote == '"':
			i++
		case quote != 0 && c == quote:
			quote = 0
		case quote == 0 && (c == '"' || c == '\''):
			quote = c
		case quote == 0 && c == '#':
			return line[:i]
		}
	}
	return line
}

// ManifestEntryLine is the line of nomi.toml's text data that writes the
// string value in the list at path: the first line from the key's own on
// that quotes it, or the key's line when none does; 0 when the key is
// written nowhere.
func ManifestEntryLine(data []byte, value string, path ...string) int {
	start := ManifestKeyLine(data, path...)
	if start == 0 {
		return 0
	}
	lines := strings.Split(string(data), "\n")
	for i := start - 1; i < len(lines); i++ {
		text := strings.TrimSpace(stripTomlComment(lines[i]))
		if i > start-1 && (strings.HasPrefix(text, "[") && !strings.HasPrefix(text, `["`) || strings.Contains(text, "=")) {
			break
		}
		if strings.Contains(text, `"`+value+`"`) || strings.Contains(text, `'`+value+`'`) {
			return i + 1
		}
	}
	return start
}

// ManifestError is a problem in the nomi.toml at Path that the parser
// places at a line: Line is 1-based, 0 when no line writes what it is
// about.
type ManifestError struct {
	Path    string
	Line    int
	Message string
}

func (e *ManifestError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", e.Path, e.Line, e.Message)
	}
	return e.Path + ": " + e.Message
}

// with is e with its message formatted from format and args.
func (e *ManifestError) with(format string, args ...any) error {
	e.Message = fmt.Sprintf(format, args...)
	return e
}

// ManifestErrorAt is where in data, the text of the nomi.toml or
// nomi-workspace.toml at path, err is, as an editor shows it in that file:
// its line, 1-based, or 0 when it has none, and its message. An error
// ParseManifestIn answers is at a ManifestError's line, or the TOML
// decoder's for text that is not TOML, with its message without the
// file's name. A problem in the nomi.toml of a package the file leads to
// (pkgcache.PackageManifestError) is at the entry that leads there, with
// the whole message, which names the package's file and its line.
func ManifestErrorAt(path string, data []byte, err error) (line int, message string) {
	var reached *pkgcache.PackageManifestError
	if errors.As(err, &reached) {
		if key, value, ok := reached.EntryIn(path); ok {
			return manifestLine(data, key, value), err.Error()
		}
		return 0, err.Error()
	}
	var me *ManifestError
	if errors.As(err, &me) {
		return me.Line, me.Message
	}
	var pe toml.ParseError
	if errors.As(err, &pe) {
		return pe.Position.Line, pe.Error()
	}
	return 0, err.Error()
}
