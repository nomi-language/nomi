package analysis

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/nomi-language/nomi/internal/modcache"
	"github.com/nomi-language/nomi/internal/suggest"
)

// The keys nomi.toml accepts are manifestFile's: each table is a field with
// a `toml` tag, and each key a field of that table's struct. A field whose
// type is a struct is a table nested in its own (`[capabilities.self]`),
// and a map's keys are free, as `[capabilities.deps.modules]`'s module
// paths are. A table or key the manifest holds and manifestFile does not
// is an error at its line, so a misspelled key is never read as nothing.
// The error names the nearest key when one is close in spelling, except in
// a table whose field carries a `keys` tag: there it says what each key
// holds instead, since keys of one table that differ in a word are close
// in spelling and far in meaning (`std_in_tests` and `go_in_tests`).
//
// A top-level table named for a module the project requires, or for the
// project's own module, belongs to that module's tools (`nomi tool`), which
// read it themselves: sqlkit's `[sqlkit]`. The compiler does not check it.

// manifestTable is one table manifestFile declares: its keys, and its
// field's `keys` tag, what each key holds, or "".
type manifestTable struct {
	keys  []string
	about string
}

// manifestTables is each table manifestFile declares, by its path joined
// with ".", read from the struct's tags.
func manifestTables() map[string]manifestTable {
	out := map[string]manifestTable{}
	var tables func(prefix string, t reflect.Type)
	tables = func(prefix string, t reflect.Type) {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() != reflect.Struct {
				continue
			}
			path := f.Tag.Get("toml")
			if prefix != "" {
				path = prefix + "." + path
			}
			table := manifestTable{about: f.Tag.Get("keys")}
			for j := 0; j < ft.NumField(); j++ {
				table.keys = append(table.keys, ft.Field(j).Tag.Get("toml"))
			}
			out[path] = table
			tables(path, ft)
		}
	}
	tables("", reflect.TypeOf(manifestFile{}))
	return out
}

// unknownManifestKey is the error for the first key in undecoded (in the
// order the manifest writes them) that is not a tool's: a table nomi.toml
// has none of, or a key its table does not have, at its line, with the
// nearest known name when one is close in spelling.
func unknownManifestKey(data []byte, undecoded []toml.Key, md toml.MetaData, sourceLabel string, tools func() []string) error {
	if len(undecoded) == 0 {
		return nil
	}
	tables := manifestTables()
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
			if len(k) == 1 && md.Type(k...) != "Hash" {
				return at.with("nomi.toml has no top-level key `%s`; every key is in a table (%s)", k[0], tableList(tables))
			}
			if near := suggest.Closest(k[0], topTables(tables)); near != "" {
				return at.with("nomi.toml has no [%s] table; did you mean [%s]?", k[0], near)
			}
			return at.with("nomi.toml has no [%s] table; its tables are %s", k[0], tableList(tables))
		}
		// The innermost known table the key is in; a key past its next
		// part is in a table nested in that one, which it declares none of.
		n := 1
		for n < len(k)-1 {
			if _, known := tables[strings.Join(k[:n+1], ".")]; !known {
				break
			}
			n++
		}
		path, key := strings.Join(k[:n], "."), k[n]
		table := tables[path]
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
		if !strings.Contains(name, ".") {
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

func keyList(keys []string) string {
	quoted := make([]string, len(keys))
	for i, k := range keys {
		quoted[i] = "`" + k + "`"
	}
	return strings.Join(quoted, ", ")
}

// toolTables is the names of the tables in the manifest at dir that belong
// to a module's tools: the module's own `[module].name` and that of each
// Nomi module its go.mod requires (modcache).
func toolTables(dir string) []string {
	var names []string
	if name := manifestName(dir); name != "" {
		names = append(names, name)
	}
	g, err := modcache.Load(dir)
	if err != nil || g == nil {
		return names
	}
	for _, d := range g.Deps {
		if d.Nomi && d.Dir != "" {
			if name := manifestName(d.Dir); name != "" {
				names = append(names, name)
			}
		}
	}
	return names
}

// manifestName is `[module].name` of the manifest at dir, read without
// checking the rest, or "".
func manifestName(dir string) string {
	data, err := ReadManifest(dir)
	if err != nil {
		return ""
	}
	var raw struct {
		Module struct {
			Name string `toml:"name"`
		} `toml:"module"`
	}
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return ""
	}
	return raw.Module.Name
}

// ManifestKeyLine is the line (1-based) of nomi.toml's text data that
// writes the key at path: a table's header, or the line of a key in its
// table; 0 when no line does. A key a header and a dotted key spell
// together (`[capabilities]` and `modules.x = []`) is found too.
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
// path (ManifestTable).
func ManifestTableLayout(data []byte, path ...string) ManifestTable {
	want := strings.Join(path, "\x00")
	lines := strings.Split(string(data), "\n")
	var t ManifestTable
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
		if strings.Contains(text, `"`+value+`"`) || strings.Contains(text, `'`+value+`'`) {
			return i + 1
		}
		if i > start-1 && (strings.HasPrefix(text, "[") && !strings.HasPrefix(text, `["`) || strings.Contains(text, "=")) {
			break
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

// ManifestErrorAt is where in nomi.toml err, an error ParseManifestIn
// answers, is: its line, 1-based, or 0 when it has none, and its message
// without the file's name. A ManifestError's line, or the TOML decoder's
// for text that is not TOML.
func ManifestErrorAt(err error) (line int, message string) {
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
