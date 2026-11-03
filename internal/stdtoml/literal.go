// Package stdtoml is std/toml's host implementation: what a `Toml"..."`
// literal's check and build need. Check reads the literal's static text,
// finds where each `${...}` slot stands, and reads the text as TOML with a
// stand-in for each slot (github.com/BurntSushi/toml, TOML 1.1). Build
// writes each slot's TOML for where it stands.
//
// A slot stands for a whole key or a whole value. A key slot is one key
// part: at the start of a line, after a `.` in a dotted key, after `[` or
// `[[` in a table header, and after `{` or `,` in an inline table. A value
// slot is a whole value: after `=`, and after `[` or `,` in an array. A slot
// anywhere else (inside a string or a comment, or touching other text of the
// same key or value) is refused, since no escaping makes it safe there.
package stdtoml

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
)

// Refusals, the compile errors a misplaced slot gets.
const (
	inString  = "a slot cannot go inside a TOML string; write it where the whole value goes, as in `key = ${value}`"
	inComment = "a slot cannot go inside a TOML comment"
	notWhole  = "a slot must stand for a whole TOML key or value"
)

// place is where a slot stands.
type place int

const (
	placeKey place = iota
	placeValue
)

// mode is what the scanner reads next.
type mode int

const (
	lineStart  mode = iota // a line of the document: a key, a header, a comment or nothing
	keyStart               // a key part
	bareKey                // inside a bare key
	afterKey               // after a key part: `.`, `=`, or a header's `]`
	valueStart             // a value
	scalar                 // inside a number, boolean or date
	afterValue             // after a value: `,`, `]`, `}`, a comment or the line's end
	headerEnd              // after a table header's `]`
	basic                  // inside a "string"
	literal                // inside a 'string'
	mlBasic                // inside a """string"""
	mlLiteral              // inside a '''string'''
	comment                // inside a comment
)

// scanner follows a TOML document far enough to place its slots. It is
// lenient about text that is not TOML: the validator reports that.
type scanner struct {
	mode   mode
	strKey bool   // the string being read is a key part
	escape bool   // a backslash came just before, in a basic string
	nest   []rune // open arrays `[` and inline tables `{`
	header int    // 1 or 2 while a `[` or `[[` header's key is read
	resume mode   // the mode a comment returns to at its line's end
}

func (s *scanner) top() rune {
	if len(s.nest) == 0 {
		return 0
	}
	return s.nest[len(s.nest)-1]
}

func (s *scanner) pop() { s.nest = s.nest[:len(s.nest)-1] }

func isSpace(r rune) bool { return r == ' ' || r == '\t' || r == '\r' }

func isBare(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-'
}

// run counts the runes equal to q from rs[i].
func run(rs []rune, i int, q rune) int {
	n := 0
	for i+n < len(rs) && rs[i+n] == q {
		n++
	}
	return n
}

func (s *scanner) feed(rs []rune) {
	for i := 0; i < len(rs); {
		i += s.step(rs, i)
	}
}

func (s *scanner) startComment(back mode) int {
	s.resume = back
	s.mode = comment
	return 1
}

// startString opens the string at rs[i], a key part when key.
func (s *scanner) startString(rs []rune, i int, key bool) int {
	s.strKey = key
	q := rs[i]
	if !key && run(rs, i, q) >= 3 {
		if q == '"' {
			s.mode = mlBasic
		} else {
			s.mode = mlLiteral
		}
		return 3
	}
	if q == '"' {
		s.mode = basic
	} else {
		s.mode = literal
	}
	return 1
}

func (s *scanner) endString() {
	if s.strKey {
		s.mode = afterKey
	} else {
		s.mode = afterValue
	}
}

// step reads from rs[i] and answers how many runes it consumed; 0 when the
// mode changed and the rune is read again.
func (s *scanner) step(rs []rune, i int) int {
	r := rs[i]
	switch s.mode {
	case comment:
		if r == '\n' {
			s.mode = s.resume
		}
		return 1
	case basic, mlBasic:
		switch {
		case s.escape:
			s.escape = false
		case r == '\\':
			s.escape = true
		case r == '"' && s.mode == basic:
			s.endString()
		case r == '"':
			n := run(rs, i, '"')
			if n >= 3 {
				// Up to two quotes before the closing three are content.
				s.endString()
				return min(n, 5)
			}
			return n
		}
		return 1
	case literal:
		if r == '\'' {
			s.endString()
		}
		return 1
	case mlLiteral:
		if n := run(rs, i, '\''); n >= 3 {
			s.endString()
			return min(n, 5)
		} else if n > 0 {
			return n
		}
		return 1
	case lineStart:
		switch {
		case isSpace(r) || r == '\n':
			return 1
		case r == '#':
			return s.startComment(lineStart)
		case r == '[':
			s.mode = keyStart
			if run(rs, i, '[') >= 2 {
				s.header = 2
				return 2
			}
			s.header = 1
			return 1
		}
		s.mode = keyStart
		return 0
	case keyStart:
		switch {
		case isSpace(r) || r == '\n':
			return 1
		case r == '#':
			return s.startComment(keyStart)
		case r == '"' || r == '\'':
			return s.startString(rs, i, true)
		case r == '}' && s.top() == '{':
			s.pop()
			s.mode = afterValue
			return 1
		}
		s.mode = bareKey
		return 1
	case bareKey:
		if isBare(r) {
			return 1
		}
		s.mode = afterKey
		return 0
	case afterKey:
		switch {
		case isSpace(r):
			return 1
		case r == '.':
			s.mode = keyStart
			return 1
		case r == '=':
			s.mode = valueStart
			return 1
		case r == ']' && s.header > 0:
			n := s.header
			s.header = 0
			s.mode = headerEnd
			return min(n, run(rs, i, ']'))
		}
		s.mode = scalar
		return 1
	case valueStart:
		switch {
		case isSpace(r) || r == '\n':
			return 1
		case r == '#':
			return s.startComment(valueStart)
		case r == '"' || r == '\'':
			return s.startString(rs, i, false)
		case r == '[':
			s.nest = append(s.nest, '[')
			return 1
		case r == '{':
			s.nest = append(s.nest, '{')
			s.mode = keyStart
			return 1
		case r == ']' && s.top() == '[':
			s.pop()
			s.mode = afterValue
			return 1
		}
		s.mode = scalar
		return 1
	case scalar:
		if isSpace(r) || strings.ContainsRune("\n,]}#", r) {
			s.mode = afterValue
			return 0
		}
		return 1
	case afterValue:
		switch {
		case isSpace(r):
			return 1
		case r == '\n':
			if len(s.nest) == 0 {
				s.mode = lineStart
			}
			return 1
		case r == '#':
			if len(s.nest) == 0 {
				return s.startComment(lineStart)
			}
			return s.startComment(afterValue)
		case r == ',' && s.top() == '[':
			s.mode = valueStart
			return 1
		case r == ',' && s.top() == '{':
			s.mode = keyStart
			return 1
		case r == ']' && s.top() == '[', r == '}' && s.top() == '{':
			s.pop()
			return 1
		}
		// The time of a date-time written with a space, or text that is
		// not TOML, which the validator reports.
		s.mode = scalar
		return 1
	case headerEnd:
		switch {
		case isSpace(r):
			return 1
		case r == '\n':
			s.mode = lineStart
			return 1
		case r == '#':
			return s.startComment(lineStart)
		}
		s.mode = scalar
		return 1
	}
	return 1
}

// slot places a slot where the scanner stands, or answers why it cannot go
// there.
func (s *scanner) slot() (place, string) {
	switch s.mode {
	case lineStart, keyStart:
		s.mode = afterKey
		return placeKey, ""
	case valueStart:
		s.mode = afterValue
		return placeValue, ""
	case basic, literal, mlBasic, mlLiteral:
		return 0, inString
	case comment:
		return 0, inComment
	}
	return 0, notWhole
}

// follows reports whether text, the static after a slot at p, starts with
// what may come right after a whole key or value there.
func follows(p place, text string) bool {
	r, n := utf8.DecodeRuneInString(text)
	if n == 0 {
		return true
	}
	if p == placeKey {
		return strings.ContainsRune(" \t.=]", r)
	}
	return strings.ContainsRune(" \t\r\n,]}#", r)
}

// refusal is a misplaced slot or invalid TOML: the static it is in, the
// code point offset into it, and why.
type refusal struct {
	fragment, offset int
	message          string
}

// places answers where each of the statics' slots stands, or the first
// slot that cannot stand where it is and the places of the slots before it.
func places(statics []string) ([]place, *refusal) {
	var s scanner
	var out []place
	for i, text := range statics {
		if i > 0 {
			p, why := s.slot()
			if why == "" && !follows(p, text) {
				why = notWhole
			}
			if why != "" {
				return out, &refusal{i - 1, utf8.RuneCountInString(statics[i-1]), why}
			}
			out = append(out, p)
		}
		s.feed([]rune(text))
	}
	return out, nil
}

// standIn is the text the validator reads in place of slot n at p: a key
// starting with NUL, distinct per slot so two key slots never collide, or a
// value.
func standIn(p place, n int) string {
	if p == placeKey {
		return `"\u0000` + strconv.Itoa(n) + `"`
	}
	return "0"
}

// check refuses statics whose slots cannot stand where they are, or whose
// text is not TOML with a stand-in for every slot.
func check(statics []string) *refusal {
	ps, r := places(statics)
	if r != nil {
		if r.message != notWhole {
			return r
		}
		// Text that is not TOML before the slot, as in `a = = ${v}`, is the
		// mistake to report: the slot is misplaced only because of it. An
		// error in the spaces right before the slot, or at a stand-in, is
		// the slot's.
		for len(ps) < len(statics)-1 {
			ps = append(ps, placeValue)
		}
		before := statics[r.fragment]
		cut := r.offset - utf8.RuneCountInString(before) + utf8.RuneCountInString(strings.TrimRight(before, " \t"))
		t := validate(statics, ps)
		if t != nil && t.fragment < r.fragment && t.offset < utf8.RuneCountInString(statics[t.fragment]) ||
			t != nil && t.fragment == r.fragment && t.offset < cut {
			return t
		}
		return r
	}
	return validate(statics, ps)
}

// validate reads the statics as TOML with a stand-in for each slot, placed
// at ps.
func validate(statics []string, ps []place) *refusal {
	var b strings.Builder
	for i, text := range statics {
		if i > 0 {
			b.WriteString(standIn(ps[i-1], i-1))
		}
		b.WriteString(text)
	}
	doc := b.String()
	var into map[string]any
	_, err := toml.Decode(doc, &into)
	if err == nil {
		return nil
	}
	var pe toml.ParseError
	if !errors.As(err, &pe) {
		return &refusal{0, 0, err.Error()}
	}
	start := min(max(pe.Position.Start, 0), len(doc))
	fragment, offset := locate(statics, ps, utf8.RuneCountInString(doc[:start]))
	return &refusal{fragment, offset, pe.Message}
}

// locate turns a code point offset into the validated text into the static
// it falls in and the offset there. An offset inside a stand-in is the slot
// it stands for, which a refusal points at as the end of the static before.
func locate(statics []string, ps []place, at int) (int, int) {
	for i, text := range statics {
		n := utf8.RuneCountInString(text)
		if at <= n || i == len(statics)-1 {
			return i, min(at, n)
		}
		at -= n
		w := utf8.RuneCountInString(standIn(ps[i], i))
		if at < w {
			return i, n
		}
		at -= w
	}
	return 0, 0
}

// FFICheck is std/toml's `check`: fragment -1 when the statics are a TOML
// literal, else the static, code point offset and message of the refusal.
func FFICheck(statics []string) (fragment, offset int64, message string) {
	r := check(statics)
	if r == nil {
		return -1, 0, ""
	}
	return int64(r.fragment), int64(r.offset), r.message
}

// FFIBuild is std/toml's `build`: the statics with each slot's TOML between
// them, written as a key where the slot stands for a key. slots holds each
// slot's `Toml.Value` as std/toml's `Toml.Value.text` writes it, never text
// a program wrote: a `String` is one basic string, which a key slot writes
// as it is, and any other value is quoted as a key.
func FFIBuild(statics, slots []string) string {
	// check accepted the statics before the program ran.
	ps, _ := places(statics)
	var b strings.Builder
	for i, text := range statics {
		if i > 0 && i-1 < len(slots) {
			v := slots[i-1]
			if i-1 < len(ps) && ps[i-1] == placeKey && !isBasicString(v) {
				v = FFIQuote(v)
			}
			b.WriteString(v)
		}
		b.WriteString(text)
	}
	return b.String()
}

// isBasicString reports whether text is one single-line basic string and
// nothing else, which a key slot writes as it is.
func isBasicString(text string) bool {
	if len(text) < 2 || text[0] != '"' {
		return false
	}
	for i := 1; i < len(text); i++ {
		switch text[i] {
		case '\\':
			i++
		case '\n', '\r':
			return false
		case '"':
			return i == len(text)-1
		}
	}
	return false
}

// FFIQuote is text as a TOML basic string: `"` and `\` escaped, and every
// control character written as an escape.
func FFIQuote(text string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range text {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				b.WriteString(`\u00`)
				b.WriteString(strings.ToUpper(strconv.FormatInt(int64(r)>>4, 16)))
				b.WriteString(strings.ToUpper(strconv.FormatInt(int64(r)&0xf, 16)))
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// FFIFloat is value as a TOML float: `inf`, `-inf` and `nan` for those, and
// otherwise the shortest text that reads back as value, with a `.0` when it
// would otherwise read as an integer.
func FFIFloat(value float64) string {
	switch {
	case math.IsNaN(value):
		return "nan"
	case math.IsInf(value, 1):
		return "inf"
	case math.IsInf(value, -1):
		return "-inf"
	}
	text := strconv.FormatFloat(value, 'g', -1, 64)
	if !strings.ContainsAny(text, ".e") {
		text += ".0"
	}
	return text
}
