// Package mf2 reads, checks, prints and formats MessageFormat 2 messages
// (Unicode LDML Part 9, stable since CLDR 47).
//
// It knows nothing of the compiler: Parse takes text and answers the
// message's data model or diagnostics with byte offsets into the text;
// Validate takes a message and a Signature (each parameter's name and what
// kind of value it is) and answers diagnostics the same way; Print writes a
// message's canonical text; Format renders a message with its arguments
// through an Env that supplies a locale's plural rules and number
// formatting. The compiler's `message` declarations and a runtime catalog
// loader can both call it unchanged.
//
// The functions Nomi formats are the stable defaults `:string`,
// `:number`, `:integer`, `:percent` and `:currency`, the draft `:date`,
// `:time` and `:datetime`, and Nomi's own `:nomi:noun` and `:nomi:gender`
// (nouns.go), `:nomi:list` and `:nomi:weekday`. The others MF2 defines
// (`:offset`, `:unit`) parse and are refused by Validate as not supported
// yet. The Env writes an amount and a calendar value (Env.Money, Env.Date);
// `:datetime`'s field options name a CLDR skeleton (Skeleton).
package mf2

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Span is a byte range of a message's text: Start through End, exclusive.
type Span struct{ Start, End int }

// Diagnostic is a problem at Span of a message's text.
type Diagnostic struct {
	Span    Span
	Message string
	Hints   []string
}

// Message is a message's data model.
//
// A message with no `.match` has Pattern; one with a `.match` has Selectors
// and Variants. Declarations come first in either.
type Message struct {
	Declarations []Declaration
	Selectors    []Variable
	Variants     []Variant
	Pattern      Pattern
	Span         Span
}

// Matches reports whether m selects among variants.
func (m *Message) Matches() bool { return len(m.Selectors) > 0 }

// Declaration is `.input {$name ...}` or `.local $name = {...}`.
type Declaration struct {
	Local    bool
	Name     string
	NameSpan Span
	Value    Expression
	Span     Span
}

// Variable is `$name` where a variable stands alone: a selector.
type Variable struct {
	Name string
	Span Span
}

// Variant is one arm of a `.match`: its keys and its pattern.
type Variant struct {
	Keys    []Key
	Pattern Pattern
	Span    Span
}

// CatchAll reports whether every key of v is `*`.
func (v *Variant) CatchAll() bool {
	for _, k := range v.Keys {
		if !k.CatchAll {
			return false
		}
	}
	return true
}

// Key is a variant key: `*`, or a literal.
type Key struct {
	CatchAll bool
	Value    string
	Span     Span
}

// Pattern is the text a message or variant formats to: text, expressions
// and markup.
type Pattern struct {
	Parts []Part
	Span  Span
}

// Part is one piece of a pattern: Text when Expr and Markup are nil.
type Part struct {
	Text   string
	Expr   *Expression
	Markup *Markup
	Span   Span
}

// Expression is `{operand :function options @attributes}`; the operand or
// the function may be absent, not both.
type Expression struct {
	Operand    *Operand
	Function   *Function
	Attributes []Attribute
	Span       Span
}

// Operand is a literal or a variable.
type Operand struct {
	Variable bool
	Value    string
	Span     Span
}

// Function is `:name` with its options; Name includes a namespace
// (`nomi:noun`).
type Function struct {
	Name    string
	Options []Option
	Span    Span
}

// Option returns the option named name.
func (f *Function) Option(name string) (Option, bool) {
	if f == nil {
		return Option{}, false
	}
	for _, o := range f.Options {
		if o.Name == name {
			return o, true
		}
	}
	return Option{}, false
}

// Option is `name=value`.
type Option struct {
	Name     string
	Value    Operand
	NameSpan Span
	Span     Span
}

// Attribute is `@name` or `@name=literal`. Nomi reads none.
type Attribute struct {
	Name  string
	Value *Operand
	Span  Span
}

// MarkupKind says which of the three markup forms a Markup is.
type MarkupKind int

const (
	MarkupOpen MarkupKind = iota
	MarkupStandalone
	MarkupClose
)

// Markup is `{#name}`, `{#name/}` or `{/name}`, which format to nothing.
type Markup struct {
	Kind       MarkupKind
	Name       string
	Options    []Option
	Attributes []Attribute
	Span       Span
}

// Parse reads text as a message. Every syntax error it finds is a
// diagnostic; the message is nil when there is one.
func Parse(text string) (*Message, []Diagnostic) {
	p := &parser{src: text}
	m := p.message()
	if len(p.errs) > 0 {
		return nil, p.errs[:1]
	}
	return m, nil
}

type parser struct {
	src  string
	pos  int
	errs []Diagnostic
}

// bail records an error at pos and stops the parse.
type bail struct{}

func (p *parser) fail(start, end int, format string, args ...any) {
	// An error at the end of the text is shown on its last character.
	if start >= len(p.src) && len(p.src) > 0 {
		start = len(p.src) - 1
	}
	if end <= start {
		end = start + 1
	}
	end = min(end, len(p.src))
	p.errs = append(p.errs, Diagnostic{Span: Span{start, end}, Message: fmt.Sprintf(format, args...)})
	panic(bail{})
}

func (p *parser) message() (m *Message) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(bail); !ok {
				panic(r)
			}
			m = nil
		}
	}()
	m = &Message{Span: Span{0, len(p.src)}}
	p.ws()
	if p.at(".") || p.at("{{") {
		p.complex(m)
		return m
	}
	// A simple message: the pattern is the whole text but the whitespace
	// before it.
	m.Pattern = p.pattern(false)
	if p.pos < len(p.src) {
		p.fail(p.pos, p.pos+1, "unexpected `%s`: write it as `\\%s` to mean the character", string(p.src[p.pos]), string(p.src[p.pos]))
	}
	return m
}

func (p *parser) complex(m *Message) {
	for {
		p.ws()
		switch {
		case p.at(".input"):
			m.Declarations = append(m.Declarations, p.input())
		case p.at(".local"):
			m.Declarations = append(m.Declarations, p.local())
		case p.at(".match"):
			p.matcher(m)
			p.ws()
			if p.pos < len(p.src) {
				p.fail(p.pos, p.pos+1, "unexpected text after the last variant")
			}
			return
		case p.at("{{"):
			m.Pattern = p.quotedPattern()
			p.ws()
			if p.pos < len(p.src) {
				p.fail(p.pos, p.pos+1, "unexpected text after the message's `}}`")
			}
			return
		case p.at("."):
			kw := p.pos
			p.pos++
			p.name()
			p.fail(kw, p.pos, "unknown keyword `%s`: a message declares with `.input` and `.local` and selects with `.match`", p.src[kw:p.pos])
		case p.pos >= len(p.src):
			p.fail(len(p.src), len(p.src), "a message with declarations ends with a pattern in `{{...}}` or a `.match`")
		default:
			p.fail(p.pos, p.pos+1, "expected `.input`, `.local`, `.match` or `{{`")
		}
	}
}

func (p *parser) input() Declaration {
	start := p.pos
	p.pos += len(".input")
	p.ws()
	if !p.at("{") {
		p.fail(p.pos, p.pos+1, "`.input` is followed by an expression of a variable: `.input {$name :function}`")
	}
	e := p.expression()
	if e.Operand == nil || !e.Operand.Variable {
		p.fail(e.Span.Start, e.Span.End, "`.input` declares a variable: its expression starts with `$name`")
	}
	return Declaration{Name: e.Operand.Value, NameSpan: e.Operand.Span, Value: e, Span: Span{start, p.pos}}
}

func (p *parser) local() Declaration {
	start := p.pos
	p.pos += len(".local")
	if !p.ws() {
		p.fail(p.pos, p.pos+1, "expected a space after `.local`")
	}
	if !p.at("$") {
		p.fail(p.pos, p.pos+1, "`.local` declares a variable: `.local $name = {...}`")
	}
	v := p.variable()
	p.ws()
	if !p.at("=") {
		p.fail(p.pos, p.pos+1, "expected `=` after `.local %s`", "$"+v.Name)
	}
	p.pos++
	p.ws()
	if !p.at("{") {
		p.fail(p.pos, p.pos+1, "expected an expression in `{...}` after `=`")
	}
	e := p.expression()
	return Declaration{Local: true, Name: v.Name, NameSpan: v.Span, Value: e, Span: Span{start, p.pos}}
}

func (p *parser) matcher(m *Message) {
	p.pos += len(".match")
	for {
		ws := p.ws()
		if !p.at("$") {
			break
		}
		if !ws {
			p.fail(p.pos, p.pos+1, "expected a space before the selector")
		}
		m.Selectors = append(m.Selectors, p.variable())
	}
	if len(m.Selectors) == 0 {
		p.fail(p.pos, p.pos+1, "`.match` is followed by the variables it selects on: `.match $count`")
	}
	for {
		p.ws()
		if p.pos >= len(p.src) {
			break
		}
		m.Variants = append(m.Variants, p.variant())
	}
	if len(m.Variants) == 0 {
		p.fail(len(p.src), len(p.src), "a `.match` has at least one variant: `* {{...}}`")
	}
}

func (p *parser) variant() Variant {
	start := p.pos
	var keys []Key
	for !p.at("{{") && p.pos < len(p.src) {
		if len(keys) > 0 && !p.wsBefore() {
			p.fail(p.pos, p.pos+1, "expected a space between keys")
		}
		keys = append(keys, p.key())
		p.ws()
	}
	if len(keys) == 0 {
		p.fail(p.pos, p.pos+2, "a variant starts with its keys: `one {{...}}`")
	}
	pat := p.quotedPattern()
	return Variant{Keys: keys, Pattern: pat, Span: Span{start, p.pos}}
}

// wsBefore reports whether the byte before pos is whitespace.
func (p *parser) wsBefore() bool {
	if p.pos == 0 {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(p.src[:p.pos])
	return isWS(r)
}

func (p *parser) key() Key {
	start := p.pos
	if p.at("*") {
		p.pos++
		return Key{CatchAll: true, Span: Span{start, p.pos}}
	}
	lit, ok := p.literal()
	if !ok {
		p.fail(p.pos, p.pos+1, "expected a key: `*`, a name, a number or a `|quoted|` literal")
	}
	return Key{Value: lit.Value, Span: lit.Span}
}

func (p *parser) quotedPattern() Pattern {
	if !p.at("{{") {
		p.fail(p.pos, p.pos+1, "expected `{{`")
	}
	p.pos += 2
	pat := p.pattern(true)
	if !p.at("}}") {
		p.fail(p.pos, p.pos+1, "expected `}}` to close the pattern")
	}
	p.pos += 2
	return pat
}

// pattern reads text and placeholders up to the end, or to `}}` when quoted.
func (p *parser) pattern(quoted bool) Pattern {
	start := p.pos
	var parts []Part
	var text strings.Builder
	textStart := p.pos
	flush := func() {
		if text.Len() > 0 {
			parts = append(parts, Part{Text: text.String(), Span: Span{textStart, p.pos}})
			text.Reset()
		}
	}
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch {
		case quoted && p.at("}}"):
			flush()
			return Pattern{Parts: parts, Span: Span{start, p.pos}}
		case c == '\\':
			if p.pos+1 >= len(p.src) || !strings.ContainsRune(`\{|}`, rune(p.src[p.pos+1])) {
				p.fail(p.pos, p.pos+2, "`\\` escapes only `\\`, `{`, `|` and `}`")
			}
			text.WriteByte(p.src[p.pos+1])
			p.pos += 2
		case c == '{':
			flush()
			ps := p.pos
			if p.isMarkup() {
				mk := p.markup()
				parts = append(parts, Part{Markup: &mk, Span: Span{ps, p.pos}})
			} else {
				e := p.expression()
				parts = append(parts, Part{Expr: &e, Span: Span{ps, p.pos}})
			}
			textStart = p.pos
		case c == '}':
			p.fail(p.pos, p.pos+1, "unexpected `}`: write `\\}` to mean the character")
		default:
			_, size := utf8.DecodeRuneInString(p.src[p.pos:])
			text.WriteString(p.src[p.pos : p.pos+size])
			p.pos += size
		}
	}
	flush()
	return Pattern{Parts: parts, Span: Span{start, p.pos}}
}

func (p *parser) isMarkup() bool {
	save := p.pos
	defer func() { p.pos = save }()
	p.pos++
	p.ws()
	return p.at("#") || p.at("/")
}

func (p *parser) markup() Markup {
	start := p.pos
	p.pos++ // {
	p.ws()
	mk := Markup{Kind: MarkupOpen}
	if p.at("/") {
		mk.Kind = MarkupClose
	}
	p.pos++ // # or /
	mk.Name = p.identifier()
	for {
		ws := p.ws()
		if p.at("}") || p.at("/") || p.at("@") {
			break
		}
		if !ws {
			p.fail(p.pos, p.pos+1, "expected a space before an option")
		}
		mk.Options = append(mk.Options, p.option())
	}
	mk.Attributes = p.attributes()
	p.ws()
	if p.at("/") && mk.Kind == MarkupOpen {
		p.pos++
		mk.Kind = MarkupStandalone
	}
	if !p.at("}") {
		p.fail(p.pos, p.pos+1, "expected `}` to close the markup")
	}
	p.pos++
	mk.Span = Span{start, p.pos}
	return mk
}

func (p *parser) expression() Expression {
	start := p.pos
	p.pos++ // {
	p.ws()
	e := Expression{}
	if p.at("$") {
		v := p.variable()
		e.Operand = &Operand{Variable: true, Value: v.Name, Span: v.Span}
	} else if !p.at(":") {
		lit, ok := p.literal()
		if !ok {
			p.fail(p.pos, p.pos+1, "expected `$name`, a literal or `:function` in the expression")
		}
		e.Operand = &lit
	}
	ws := p.ws()
	if p.at(":") {
		if e.Operand != nil && !ws {
			p.fail(p.pos, p.pos+1, "expected a space before the function")
		}
		f := p.function()
		e.Function = &f
	}
	e.Attributes = p.attributes()
	p.ws()
	if !p.at("}") {
		if p.pos >= len(p.src) {
			p.fail(start, start+1, "this `{` is not closed")
		}
		p.fail(p.pos, p.pos+1, "expected `}` to close the expression")
	}
	p.pos++
	e.Span = Span{start, p.pos}
	return e
}

func (p *parser) attributes() []Attribute {
	var out []Attribute
	for {
		save := p.pos
		p.ws()
		if !p.at("@") {
			p.pos = save
			return out
		}
		start := p.pos
		p.pos++
		a := Attribute{Name: p.identifier()}
		save = p.pos
		p.ws()
		if p.at("=") {
			p.pos++
			p.ws()
			lit, ok := p.literal()
			if !ok {
				p.fail(p.pos, p.pos+1, "an attribute's value is a literal")
			}
			a.Value = &lit
		} else {
			p.pos = save
		}
		a.Span = Span{start, p.pos}
		out = append(out, a)
	}
}

func (p *parser) function() Function {
	start := p.pos
	p.pos++ // :
	f := Function{Name: p.identifier()}
	for {
		save := p.pos
		ws := p.ws()
		if p.at("}") || p.at("@") || p.pos >= len(p.src) || !ws {
			p.pos = save
			break
		}
		f.Options = append(f.Options, p.option())
	}
	f.Span = Span{start, p.pos}
	return f
}

func (p *parser) option() Option {
	start := p.pos
	name := p.identifier()
	nameSpan := Span{start, p.pos}
	p.ws()
	if !p.at("=") {
		p.fail(p.pos, p.pos+1, "expected `=` after the option name `%s`", name)
	}
	p.pos++
	p.ws()
	var v Operand
	if p.at("$") {
		vr := p.variable()
		v = Operand{Variable: true, Value: vr.Name, Span: vr.Span}
	} else {
		lit, ok := p.literal()
		if !ok {
			p.fail(p.pos, p.pos+1, "an option's value is a literal or a `$variable`")
		}
		v = lit
	}
	return Option{Name: name, Value: v, NameSpan: nameSpan, Span: Span{start, p.pos}}
}

func (p *parser) variable() Variable {
	start := p.pos
	p.pos++ // $
	name := p.name()
	return Variable{Name: name, Span: Span{start, p.pos}}
}

// identifier is `name` or `namespace:name`.
func (p *parser) identifier() string {
	n := p.name()
	if p.at(":") {
		p.pos++
		n += ":" + p.name()
	}
	return n
}

func (p *parser) name() string {
	start := p.pos
	r, size := utf8.DecodeRuneInString(p.src[p.pos:])
	if p.pos >= len(p.src) || !isNameStart(r) {
		p.fail(p.pos, p.pos+1, "expected a name")
	}
	p.pos += size
	for p.pos < len(p.src) {
		r, size = utf8.DecodeRuneInString(p.src[p.pos:])
		if !isNameChar(r) {
			break
		}
		p.pos += size
	}
	return p.src[start:p.pos]
}

// literal reads a quoted or unquoted literal; ok is false when none starts
// at pos.
func (p *parser) literal() (Operand, bool) {
	start := p.pos
	if p.at("|") {
		p.pos++
		var b strings.Builder
		for {
			if p.pos >= len(p.src) {
				p.fail(start, start+1, "this `|` literal is not closed")
			}
			c := p.src[p.pos]
			if c == '|' {
				p.pos++
				break
			}
			if c == '\\' {
				if p.pos+1 >= len(p.src) || !strings.ContainsRune(`\{|}`, rune(p.src[p.pos+1])) {
					p.fail(p.pos, p.pos+2, "`\\` escapes only `\\`, `{`, `|` and `}`")
				}
				b.WriteByte(p.src[p.pos+1])
				p.pos += 2
				continue
			}
			b.WriteByte(c)
			p.pos++
		}
		return Operand{Value: b.String(), Span: Span{start, p.pos}}, true
	}
	if p.pos >= len(p.src) {
		return Operand{}, false
	}
	if c := p.src[p.pos]; c == '-' || c >= '0' && c <= '9' {
		end := numberEnd(p.src, p.pos)
		if end < 0 {
			p.fail(p.pos, p.pos+1, "a number literal is `-?digits[.digits][e±digits]`; quote other text as `|...|`")
		}
		p.pos = end
		return Operand{Value: p.src[start:end], Span: Span{start, end}}, true
	}
	r, _ := utf8.DecodeRuneInString(p.src[p.pos:])
	if !isNameStart(r) {
		return Operand{}, false
	}
	n := p.name()
	return Operand{Value: n, Span: Span{start, p.pos}}, true
}

// numberEnd is the end of the number literal at i, or -1.
func numberEnd(s string, i int) int {
	j := i
	if j < len(s) && s[j] == '-' {
		j++
	}
	digits := func() int {
		k := j
		for k < len(s) && s[k] >= '0' && s[k] <= '9' {
			k++
		}
		return k - j
	}
	n := digits()
	if n == 0 || n > 1 && s[j] == '0' {
		return -1
	}
	j += n
	if j < len(s) && s[j] == '.' {
		j++
		n = digits()
		if n == 0 {
			return -1
		}
		j += n
	}
	if j < len(s) && (s[j] == 'e' || s[j] == 'E') {
		j++
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		n = digits()
		if n == 0 {
			return -1
		}
		j += n
	}
	if j < len(s) {
		if r, _ := utf8.DecodeRuneInString(s[j:]); isNameChar(r) {
			return -1
		}
	}
	return j
}

// IsNumber reports whether s is an MF2 number literal.
func IsNumber(s string) bool { return s != "" && numberEnd(s, 0) == len(s) }

// IsName reports whether s is an MF2 name.
func IsName(s string) bool {
	for i, r := range s {
		if i == 0 && !isNameStart(r) || i > 0 && !isNameChar(r) {
			return false
		}
	}
	return s != ""
}

// ws skips optional whitespace and bidi marks, and reports whether it
// skipped any.
func (p *parser) ws() bool {
	start := p.pos
	for p.pos < len(p.src) {
		r, size := utf8.DecodeRuneInString(p.src[p.pos:])
		if !isWS(r) && !isBidi(r) {
			break
		}
		p.pos += size
	}
	return p.pos > start
}

func (p *parser) at(s string) bool { return strings.HasPrefix(p.src[p.pos:], s) }

func isWS(r rune) bool {
	return r == ' ' || r == '\t' || r == '\r' || r == '\n' || r == '　'
}

func isBidi(r rune) bool {
	return r == '؜' || r == '‎' || r == '‏' || r >= '⁦' && r <= '⁩'
}

func isNameStart(r rune) bool {
	switch {
	case r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z':
		return true
	case r < 0x80:
		return false
	case isWS(r) || isBidi(r) || r == utf8.RuneError:
		return false
	}
	return unicode.IsLetter(r) || unicode.IsMark(r) || unicode.In(r, unicode.Han, unicode.So) || r > 0xFFFF
}

func isNameChar(r rune) bool {
	return isNameStart(r) || r >= '0' && r <= '9' || r == '-' || r == '.' || r == '·' || r >= 0x80 && unicode.IsDigit(r)
}
