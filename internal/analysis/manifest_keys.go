package analysis

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/nomi-language/nomi/internal/modcache"
)

// The keys nomi.toml accepts are manifestFile's: each table is a field with
// a `toml` tag, and each key a field of that table's struct (a map's keys
// are free, as `[capabilities.modules]`'s module paths are). A table or key
// the manifest holds and manifestFile does not is an error at its line, so
// a misspelled key is never read as nothing.
//
// A top-level table named for a module the project requires, or for the
// project's own module, belongs to that module's tools (`nomi tool`), which
// read it themselves: sqlkit's `[sqlkit]`. The compiler does not check it.

// manifestTables is each table manifestFile declares, with its keys, read
// from the struct's tags.
func manifestTables() map[string][]string {
	out := map[string][]string{}
	t := reflect.TypeOf(manifestFile{})
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		table := f.Tag.Get("toml")
		var keys []string
		ft := f.Type
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct {
			for j := 0; j < ft.NumField(); j++ {
				keys = append(keys, ft.Field(j).Tag.Get("toml"))
			}
		}
		out[table] = keys
	}
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
		at := sourceLabel
		if line := ManifestKeyLine(data, k...); line > 0 {
			at = fmt.Sprintf("%s:%d", sourceLabel, line)
		}
		if _, known := tables[k[0]]; !known {
			if len(k) == 1 && md.Type(k...) != "Hash" {
				return fmt.Errorf("%s: nomi.toml has no top-level key `%s`; every key is in a table (%s)", at, k[0], tableList(tables))
			}
			names := make([]string, 0, len(tables))
			for name := range tables {
				names = append(names, name)
			}
			if near := ClosestName(k[0], names); near != "" {
				return fmt.Errorf("%s: nomi.toml has no [%s] table; did you mean [%s]?", at, k[0], near)
			}
			return fmt.Errorf("%s: nomi.toml has no [%s] table; its tables are %s", at, k[0], tableList(tables))
		}
		table, key := strings.Join(k[:len(k)-1], "."), k[len(k)-1]
		keys := tables[k[0]]
		if len(k) > 2 {
			// A table nested in a known one that declares none.
			return fmt.Errorf("%s: [%s] has no key `%s`; its keys are %s", at, k[0], k[1], keyList(keys))
		}
		if near := ClosestName(key, keys); near != "" {
			return fmt.Errorf("%s: [%s] has no key `%s`; did you mean `%s`?", at, table, key, near)
		}
		return fmt.Errorf("%s: [%s] has no key `%s`; its keys are %s", at, table, key, keyList(keys))
	}
	return nil
}

func tableList(tables map[string][]string) string {
	names := make([]string, 0, len(tables))
	for name := range tables {
		names = append(names, "["+name+"]")
	}
	slices.Sort(names)
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
	data, err := os.ReadFile(filepath.Join(dir, "nomi.toml"))
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
