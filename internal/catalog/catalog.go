// Package catalog reads message catalogs: the TOML files that hold a
// locale's translations of one Nomi file's `message` declarations, at the
// file's own path under the catalog directory
// (`locales/pl/shop/cart.toml` for `shop/cart.nomi`).
//
// A catalog is a table per message, named by the message, holding two
// strings: `text`, the translation (MessageFormat 2), and `source`, the
// source-locale text it was translated from, which tells a stale
// translation from a current one:
//
//	[cart_items]
//	source = '''{$count :integer} items in your cart'''
//	text = '''...'''
//
// A noun's table holds its `source`, its `gender` and a form per
// grammatical case instead of `text`; Parse keeps every key besides
// `source` and `text` in Fields, and the compiler says which keys an
// entry may hold.
//
// Parse reads the subset of TOML a catalog is written in: comments, tables
// with bare or quoted names, and keys whose values are strings of any of
// TOML's four kinds. Everything it answers is placed by byte offset into
// the text, so a caller can show a diagnostic where the problem is, and
// knows nothing of the compiler.
package catalog

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Span is a byte range of a catalog's text.
type Span struct{ Start, End int }

// Diagnostic is a problem at Span of a catalog's text.
type Diagnostic struct {
	Span    Span
	Message string
	Hints   []string
}

// File is a catalog's entries in the order they are written.
type File struct {
	Entries []*Entry
}

// Entry returns the entry for the message named name.
func (f *File) Entry(name string) *Entry {
	if f == nil {
		return nil
	}
	for _, e := range f.Entries {
		if e.Name == name {
			return e
		}
	}
	return nil
}

// Entry is one message's table.
type Entry struct {
	Name string
	// Span is the table's header, `[name]`; NameSpan its name.
	Span, NameSpan Span
	// Source and Text are the entry's two strings; a missing one has
	// Present false.
	Source, Text Value
	// Fields are its other keys, in the order they are written.
	Fields []Field
}

// Field is a key of an entry other than `source` and `text`.
type Field struct {
	Name  string
	Value Value
}

// Field returns the value of the key name other than `source` and `text`,
// with Present false when the entry has none.
func (e *Entry) Field(name string) Value {
	if e == nil {
		return Value{}
	}
	for _, f := range e.Fields {
		if f.Name == name {
			return f.Value
		}
	}
	return Value{}
}

// Value is a string a key holds.
type Value struct {
	Present bool
	Text    string
	// KeySpan is the key's; Span is the string's, quotes included.
	KeySpan, Span Span
	// Body is where Text starts in the catalog: a literal string's text is
	// the catalog's bytes from Body on, so a byte of Text is at Body plus
	// its offset. Exact reports that; a basic string's escapes break it,
	// and an offset into Text then places only at Body.
	Body  int
	Exact bool
}

// Offset is the catalog offset of byte i of v.Text.
func (v Value) Offset(i int) int {
	if v.Exact {
		return v.Body + i
	}
	return v.Body
}

// Parse reads a catalog. A problem in one entry does not stop the others
// being read.
func Parse(text string) (*File, []Diagnostic) {
	p := &parser{src: text}
	p.file()
	return &p.out, p.diags
}

// ParseQualified reads a catalog whose tables name each declaration by its
// file, `[cart.cart_items]`, as a catalog loaded while a program runs
// does: it holds the translations of any file's declarations. An entry's
// Name is the qualified name.
func ParseQualified(text string) (*File, []Diagnostic) {
	p := &parser{src: text, qualified: true}
	p.file()
	return &p.out, p.diags
}

type parser struct {
	// qualified reports a catalog whose table names are `file.name`.
	qualified bool
	src       string
	pos       int
	out       File
	cur       *Entry
	diags     []Diagnostic
	seen      map[string]bool
}

func (p *parser) errorf(start, end int, format string, args ...any) {
	if end <= start {
		end = start + 1
	}
	end = min(end, len(p.src))
	if start >= len(p.src) && len(p.src) > 0 {
		start = len(p.src) - 1
	}
	p.diags = append(p.diags, Diagnostic{Span: Span{start, end}, Message: fmt.Sprintf(format, args...)})
}

func (p *parser) file() {
	p.seen = map[string]bool{}
	for p.pos < len(p.src) {
		p.blank()
		if p.pos >= len(p.src) {
			return
		}
		switch c := p.src[p.pos]; {
		case c == '\n' || c == '\r':
			p.pos++
		case c == '#':
			p.skipLine()
		case c == '[':
			p.table()
		default:
			p.keyValue()
		}
	}
}

// blank skips spaces and tabs.
func (p *parser) blank() {
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
		p.pos++
	}
}

func (p *parser) skipLine() {
	for p.pos < len(p.src) && p.src[p.pos] != '\n' {
		p.pos++
	}
}

// endLine expects the rest of the line to hold only a comment.
func (p *parser) endLine() {
	p.blank()
	if p.pos >= len(p.src) || p.src[p.pos] == '\n' || p.src[p.pos] == '\r' {
		return
	}
	if p.src[p.pos] == '#' {
		p.skipLine()
		return
	}
	start := p.pos
	p.skipLine()
	p.errorf(start, p.pos, "unexpected text after the value; a catalog line holds one key and its string")
}

func (p *parser) table() {
	start := p.pos
	if strings.HasPrefix(p.src[p.pos:], "[[") {
		p.skipLine()
		p.errorf(start, p.pos, "a catalog has no arrays of tables: each message is one `[name]` table")
		p.cur = nil
		return
	}
	p.pos++
	p.blank()
	nameStart := p.pos
	name, ok := p.key()
	if ok && p.qualified && p.pos < len(p.src) && p.src[p.pos] == '.' {
		// `[file.message]`: the file's name, then the declaration's.
		p.pos++
		var decl string
		decl, ok = p.key()
		name += "." + decl
	}
	nameEnd := p.pos
	p.blank()
	if !ok || p.pos >= len(p.src) || p.src[p.pos] != ']' {
		p.skipLine()
		if p.qualified {
			p.errorf(start, p.pos, "expected `[file.name]`, a message or noun by the name of its file")
		} else if ok && strings.Contains(p.src[nameEnd:p.pos], ".") {
			p.errorf(start, p.pos, "a catalog's tables are messages: `[name]`, with no dotted name")
		} else {
			p.errorf(start, p.pos, "expected `[name]`, the name of a message")
		}
		p.cur = nil
		return
	}
	p.pos++
	e := &Entry{Name: name, Span: Span{start, p.pos}, NameSpan: Span{nameStart, nameEnd}}
	if p.seen[name] {
		p.errorf(start, p.pos, "`%s` has two tables in this catalog", name)
		p.cur = nil
		p.endLine()
		return
	}
	p.seen[name] = true
	p.out.Entries = append(p.out.Entries, e)
	p.cur = e
	p.endLine()
}

// key reads a bare or quoted key.
func (p *parser) key() (string, bool) {
	if p.pos >= len(p.src) {
		return "", false
	}
	if c := p.src[p.pos]; c == '"' || c == '\'' {
		v, ok := p.str()
		if !ok || strings.HasPrefix(p.src[v.Span.Start:], `"""`) || strings.HasPrefix(p.src[v.Span.Start:], "'''") {
			return "", false
		}
		return v.Text, true
	}
	start := p.pos
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' {
			p.pos++
			continue
		}
		break
	}
	return p.src[start:p.pos], p.pos > start
}

func (p *parser) keyValue() {
	start := p.pos
	key, ok := p.key()
	keyEnd := p.pos
	p.blank()
	if !ok || p.pos >= len(p.src) || p.src[p.pos] != '=' {
		p.skipLine()
		p.errorf(start, p.pos, "expected `key = '''text'''`")
		return
	}
	p.pos++
	p.blank()
	if p.pos >= len(p.src) || p.src[p.pos] != '"' && p.src[p.pos] != '\'' {
		vs := p.pos
		p.skipLine()
		p.errorf(vs, p.pos, "a catalog's values are strings: write `%s = '''...'''`", key)
		return
	}
	v, ok := p.str()
	if !ok {
		return
	}
	v.Present = true
	v.KeySpan = Span{start, keyEnd}
	p.endLine()
	if p.cur == nil {
		if len(p.out.Entries) == 0 && !p.seenTable() {
			if p.qualified {
				p.errorf(start, keyEnd, "`%s` is outside any message: a catalog's keys are in `[file.name]` tables", key)
			} else {
				p.errorf(start, keyEnd, "`%s` is outside any message: a catalog's keys are in `[name]` tables", key)
			}
		}
		return
	}
	switch key {
	case "source":
		if p.cur.Source.Present {
			p.errorf(start, keyEnd, "`%s` gives `source` twice", p.cur.Name)
			return
		}
		p.cur.Source = v
	case "text":
		if p.cur.Text.Present {
			p.errorf(start, keyEnd, "`%s` gives `text` twice", p.cur.Name)
			return
		}
		p.cur.Text = v
	default:
		if p.cur.Field(key).Present {
			p.errorf(start, keyEnd, "`%s` gives `%s` twice", p.cur.Name, key)
			return
		}
		p.cur.Fields = append(p.cur.Fields, Field{Name: key, Value: v})
	}
}

func (p *parser) seenTable() bool { return len(p.seen) > 0 }

// str reads a string of any of TOML's four kinds.
func (p *parser) str() (Value, bool) {
	start := p.pos
	rest := p.src[p.pos:]
	switch {
	case strings.HasPrefix(rest, "'''"):
		p.pos += 3
		body := p.pos
		if strings.HasPrefix(p.src[p.pos:], "\r\n") {
			p.pos += 2
			body = p.pos
		} else if strings.HasPrefix(p.src[p.pos:], "\n") {
			p.pos++
			body = p.pos
		}
		end := strings.Index(p.src[p.pos:], "'''")
		if end < 0 {
			p.pos = len(p.src)
			p.errorf(start, start+3, "this `'''` string is not closed")
			return Value{}, false
		}
		end += p.pos
		// Up to two quotes may end the body, before the closing three.
		for i := 0; i < 2 && end+3 < len(p.src) && p.src[end+3] == '\''; i++ {
			end++
		}
		p.pos = end + 3
		return Value{Text: p.src[body:end], Span: Span{start, p.pos}, Body: body, Exact: true}, true
	case strings.HasPrefix(rest, `"""`):
		p.pos += 3
		if strings.HasPrefix(p.src[p.pos:], "\r\n") {
			p.pos += 2
		} else if strings.HasPrefix(p.src[p.pos:], "\n") {
			p.pos++
		}
		body := p.pos
		var b strings.Builder
		for {
			if p.pos >= len(p.src) {
				p.errorf(start, start+3, `this """ string is not closed`)
				return Value{}, false
			}
			if strings.HasPrefix(p.src[p.pos:], `"""`) {
				p.pos += 3
				break
			}
			if p.src[p.pos] == '\\' {
				if !p.escape(&b, true) {
					return Value{}, false
				}
				continue
			}
			b.WriteByte(p.src[p.pos])
			p.pos++
		}
		return Value{Text: b.String(), Span: Span{start, p.pos}, Body: body, Exact: b.Len() == p.pos-3-body}, true
	case rest[0] == '\'':
		p.pos++
		body := p.pos
		end := strings.IndexAny(p.src[p.pos:], "'\n")
		if end < 0 || p.src[p.pos+end] == '\n' {
			p.skipLine()
			p.errorf(start, start+1, "this `'` string is not closed on its line")
			return Value{}, false
		}
		p.pos += end + 1
		return Value{Text: p.src[body : p.pos-1], Span: Span{start, p.pos}, Body: body, Exact: true}, true
	default: // '"'
		p.pos++
		body := p.pos
		var b strings.Builder
		for {
			if p.pos >= len(p.src) || p.src[p.pos] == '\n' {
				p.errorf(start, start+1, `this " string is not closed on its line`)
				return Value{}, false
			}
			c := p.src[p.pos]
			if c == '"' {
				p.pos++
				break
			}
			if c == '\\' {
				if !p.escape(&b, false) {
					return Value{}, false
				}
				continue
			}
			b.WriteByte(c)
			p.pos++
		}
		return Value{Text: b.String(), Span: Span{start, p.pos}, Body: body, Exact: b.Len() == p.pos-1-body}, true
	}
}

// escape reads one escape of a basic string into b.
func (p *parser) escape(b *strings.Builder, multiline bool) bool {
	start := p.pos
	if p.pos+1 >= len(p.src) {
		p.errorf(start, start+1, "a `\\` ends the catalog")
		return false
	}
	c := p.src[p.pos+1]
	switch c {
	case 'b':
		b.WriteByte('\b')
	case 't':
		b.WriteByte('\t')
	case 'n':
		b.WriteByte('\n')
	case 'f':
		b.WriteByte('\f')
	case 'r':
		b.WriteByte('\r')
	case '"':
		b.WriteByte('"')
	case '\\':
		b.WriteByte('\\')
	case 'u', 'U':
		n := 4
		if c == 'U' {
			n = 8
		}
		if p.pos+2+n > len(p.src) {
			p.errorf(start, p.pos+2, "a `\\%c` escape takes %d hexadecimal digits", c, n)
			return false
		}
		v, err := strconv.ParseUint(p.src[p.pos+2:p.pos+2+n], 16, 32)
		if err != nil || !utf8.ValidRune(rune(v)) {
			p.errorf(start, p.pos+2+n, "`%s` is not a Unicode scalar value", p.src[start:p.pos+2+n])
			return false
		}
		b.WriteRune(rune(v))
		p.pos += 2 + n
		return true
	default:
		if multiline && (c == '\n' || c == ' ' || c == '\t' || c == '\r') {
			// A line-ending backslash trims the newline and the
			// whitespace after it.
			q := p.pos + 1
			for q < len(p.src) && strings.ContainsRune(" \t\r\n", rune(p.src[q])) {
				q++
			}
			p.pos = q
			return true
		}
		p.errorf(start, p.pos+2, "`\\%c` is not a TOML escape; a `'''` string needs none", c)
		return false
	}
	p.pos += 2
	return true
}

// LineCol is the 1-based line and byte column of offset in text.
func LineCol(text string, offset int) (int, int) {
	offset = max(0, min(offset, len(text)))
	line := 1 + strings.Count(text[:offset], "\n")
	return line, offset - strings.LastIndexByte(text[:offset], '\n')
}

// Literal writes text as a TOML multi-line literal string when it can be,
// and as a basic string otherwise: the form a tool writes an entry's
// `source` and `text` in. A text of several lines starts on the line after
// the opening quotes, which TOML does not count as part of it.
func Literal(text string) string {
	if !strings.Contains(text, "'''") && !strings.HasSuffix(text, "'") && !strings.ContainsAny(text, "\r") {
		if strings.Contains(text, "\n") {
			return "'''\n" + text + "'''"
		}
		return "'''" + text + "'''"
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range text {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// Block writes a message's text as an entry's string: Literal's form, with
// the text's surrounding line breaks dropped and, for a text of several
// lines, one line break at its end, so the closing quotes stand on their
// own line. The line breaks are no part of the message.
func Block(text string) string {
	text = strings.Trim(text, "\n")
	if strings.Contains(text, "\n") {
		text += "\n"
	}
	return Literal(text)
}

// SetSource rewrites the `source` of name's entry in the catalog text to
// source, the text its translation was made from, and adds one when the
// entry has none: what accepting a translation as current writes.
func SetSource(text, name, source string) (string, error) {
	f, diags := Parse(text)
	if len(diags) > 0 {
		return "", fmt.Errorf("the catalog does not read: %s", diags[0].Message)
	}
	e := f.Entry(name)
	if e == nil {
		return "", fmt.Errorf("the catalog has no entry for `%s`", name)
	}
	line := "source = " + Block(source)
	if e.Source.Present {
		return text[:e.Source.KeySpan.Start] + line + text[e.Source.Span.End:], nil
	}
	at := e.Span.End
	if nl := strings.IndexByte(text[at:], '\n'); nl >= 0 {
		at += nl + 1
		return text[:at] + line + "\n" + text[at:], nil
	}
	return text + "\n" + line + "\n", nil
}
