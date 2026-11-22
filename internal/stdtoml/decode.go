package stdtoml

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/BurntSushi/toml"
)

// The kinds HostDecode lists, one per `Toml.Value` variant. std/toml's
// `read_value` reads the same words.
const (
	kindString        = "string"
	kindInt           = "int"
	kindFloat         = "float"
	kindBool          = "bool"
	kindDate          = "date"
	kindTime          = "time"
	kindNaiveDateTime = "naive"
	kindOffset        = "offset"
	kindArray         = "array"
	kindTable         = "table"
)

// maxOffsetSeconds is the widest UTC offset std/calendar's OffsetDateTime
// holds, ±18 hours. RFC 3339, and so TOML, allows up to ±23:59.
const maxOffsetSeconds = 18 * 60 * 60

// HostDecode is std/toml's `decode`: text read as a TOML document, by the
// reader a `Toml"..."` literal's text is checked by. offset is -1 when the
// text is TOML, and nodes lists the document's values in document order:
// each value is two entries, its kind and then its text, or for an array or
// a table the count of its items or entries. An array's items follow it,
// and a table's entries follow it, each a key and then its value. A date or
// time is its ISO 8601 text, which std/calendar's parse functions read, and
// a float is HostFloat's text.
//
// When the text is not TOML, nodes is empty and offset, line, col and
// message say where reading stopped and why: offset counts code points from
// 0, and line and col count from 1, col in code points.
func HostDecode(text string) (nodes []string, offset, line, col int64, message string) {
	doc, md, f := read(text)
	if f == nil {
		l := lister{rank: map[string]int{}}
		for i, key := range md.Keys() {
			if _, seen := l.rank[key.String()]; !seen {
				l.rank[key.String()] = i
			}
		}
		l.table(nil, doc)
		if l.failure == "" {
			return l.nodes, -1, 0, 0, ""
		}
		f = &failure{0, l.failure}
	}
	ln, c := lineCol(text, f.offset)
	return nil, int64(f.offset), int64(ln), int64(c), f.message
}

// lineCol is the line and the column, both from 1 and the column in code
// points, of code point offset into text.
func lineCol(text string, offset int) (int, int) {
	line, col, at := 1, 1, 0
	for _, r := range text {
		if at == offset {
			break
		}
		at++
		if r == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

// lister writes a document's values as HostDecode lists them.
type lister struct {
	// rank is each key path's first place in the document, so a table's
	// entries are listed in the order the text gives them.
	rank    map[string]int
	nodes   []string
	failure string
}

func (l *lister) add(kind, text string) { l.nodes = append(l.nodes, kind, text) }

func (l *lister) table(path []string, entries map[string]any) {
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	place := func(k string) (int, bool) {
		at, ok := l.rank[toml.Key(append(path[:len(path):len(path)], k)).String()]
		return at, ok
	}
	sort.Slice(keys, func(i, j int) bool {
		a, aok := place(keys[i])
		b, bok := place(keys[j])
		if aok != bok {
			return aok
		}
		if aok && a != b {
			return a < b
		}
		return keys[i] < keys[j]
	})
	l.add(kindTable, strconv.Itoa(len(keys)))
	for _, k := range keys {
		l.nodes = append(l.nodes, k)
		l.value(append(path[:len(path):len(path)], k), entries[k])
	}
}

func (l *lister) value(path []string, v any) {
	switch v := v.(type) {
	case map[string]any:
		l.table(path, v)
	case []map[string]any:
		l.add(kindArray, strconv.Itoa(len(v)))
		for _, item := range v {
			l.table(path, item)
		}
	case []any:
		l.add(kindArray, strconv.Itoa(len(v)))
		for _, item := range v {
			l.value(path, item)
		}
	case string:
		l.add(kindString, v)
	case int64:
		l.add(kindInt, strconv.FormatInt(v, 10))
	case float64:
		l.add(kindFloat, HostFloat(v))
	case bool:
		l.add(kindBool, strconv.FormatBool(v))
	case time.Time:
		l.time(path, v)
	default:
		// The reader decodes into these types alone.
		l.failure = fmt.Sprintf("%s: a TOML value of Go type %T", toml.Key(path), v)
	}
}

// time lists a date or time by the location the reader gives one with no
// offset: a local date-time, date or time.
func (l *lister) time(path []string, t time.Time) {
	switch t.Location().String() {
	case "date-local":
		l.add(kindDate, t.Format("2006-01-02"))
	case "time-local":
		l.add(kindTime, t.Format("15:04:05.999999999"))
	case "datetime-local":
		l.add(kindNaiveDateTime, t.Format("2006-01-02T15:04:05.999999999"))
	default:
		if _, off := t.Zone(); off > maxOffsetSeconds || off < -maxOffsetSeconds {
			if l.failure == "" {
				l.failure = fmt.Sprintf("%s: the offset of %s is past ±18:00", toml.Key(path), t.Format(time.RFC3339Nano))
			}
			l.add(kindOffset, "")
			return
		}
		l.add(kindOffset, t.Format(time.RFC3339Nano))
	}
}
