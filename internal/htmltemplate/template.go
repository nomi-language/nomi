// Package htmltemplate reads the text of an `Html` literal (spec §18
// *HTML templates*) as an HTML template: it checks the markup, finds the
// context of each `${...}` slot, and reports what it cannot accept.
//
// The template language is HTML with stricter rules. Every non-void element
// has an explicit end tag; a `/>` closes only a void element or an element
// inside `<svg>` or `<math>`; an element name is an HTML, SVG or MathML name
// or a custom element's, with a hyphen. Attribute values may be quoted with
// `"` or `'`, and the two quotes mean the same.
//
// A slot's context decides which std/html function receives its value, and
// so which type the value must have. The contexts follow Go's html/template
// (stateText, stateRCDATA, stateAttr, stateURL, stateCSS), found once, when
// the program is checked, rather than at a template's first execution.
//
// The package knows nothing of Nomi's AST. The front end passes the static
// text around the slots and receives the slots' contexts, the static text
// to emit (an optional attribute's name moves into its slot), and errors at
// positions in that text.
package htmltemplate

import (
	"fmt"
	"strings"
)

// Context is where a slot stands, which decides how its value is written.
type Context int

const (
	// ContextText is element content: `<p>${x}</p>`.
	ContextText Context = iota
	// ContextRCDATA is the text of `<title>` or `<textarea>`, which may hold
	// no markup.
	ContextRCDATA
	// ContextAttr is a quoted attribute value that is not a URL, a style or
	// script: `class="a ${c}"`.
	ContextAttr
	// ContextURLStart is the start of a URL attribute's value:
	// `href="${link}"`. The value's scheme is checked.
	ContextURLStart
	// ContextURLPath is a slot after static text in a URL, before any `?` or
	// `#`: `href="/posts/${id}"`.
	ContextURLPath
	// ContextURLQuery is a slot after a `?` in a URL: `href="/s?q=${q}"`.
	ContextURLQuery
	// ContextURLFragment is a slot after a `#` in a URL.
	ContextURLFragment
	// ContextCSS is a slot in a `style` attribute's value.
	ContextCSS
	// ContextOptionalAttribute is a slot that is an attribute's whole,
	// unquoted value: `checked=${on}`. The attribute is written or left out
	// by the value, so the attribute's name moves from the static text into
	// the slot (Slot.Attr).
	ContextOptionalAttribute
)

var contextNames = [...]string{
	ContextText:              "text",
	ContextRCDATA:            "rcdata",
	ContextAttr:              "attribute",
	ContextURLStart:          "url start",
	ContextURLPath:           "url path",
	ContextURLQuery:          "url query",
	ContextURLFragment:       "url fragment",
	ContextCSS:               "css",
	ContextOptionalAttribute: "optional attribute",
}

func (c Context) String() string {
	if int(c) < len(contextNames) {
		return contextNames[c]
	}
	return fmt.Sprintf("Context(%d)", int(c))
}

// Slot is one `${...}` and the context it stands in.
type Slot struct {
	Context Context
	// Attr is the attribute the slot is in, lowercased, for the attribute
	// contexts; for ContextOptionalAttribute it is the name as written,
	// which the slot writes.
	Attr string
}

// Pos is a place in a literal: byte Offset of static part Part. An offset
// equal to the part's length is the slot that follows it.
type Pos struct {
	Part   int
	Offset int
}

// Error is markup the template language does not accept, or a slot where
// no escaping is safe.
type Error struct {
	Pos     Pos
	Message string
}

func (e Error) Error() string { return e.Message }

// Template is a checked literal.
type Template struct {
	// Statics is the text to emit around the slots, one more than Slots: the
	// literal's static parts, except that an optional attribute's name, with
	// the space before it and its `=`, is removed from the part before its
	// slot.
	Statics []string
	Slots   []Slot
}

// Options adjusts how errors are written.
type Options struct {
	// Line, when set, is the source line of a position, used in messages
	// that name another place ("opened at line 4"). Otherwise lines count
	// from 1 at the literal's start.
	Line func(Pos) int
}

// Parse checks the literal whose static text is parts, with one slot
// between each two parts, and finds each slot's context. It returns every
// error it finds; a Template is returned only when there are none.
func Parse(parts []string, opts Options) (*Template, []Error) {
	p := &parser{parts: parts, opts: opts}
	p.run()
	if len(p.errs) > 0 {
		return nil, p.errs
	}
	return &Template{Statics: p.statics, Slots: p.slots}, nil
}

type state int

const (
	stText state = iota
	stRCDATA
	stRawText
	stComment
	stDoctype
	stTagOpen          // just after `<`
	stEndTagOpen       // just after `</`
	stTagName          // in a start tag's name
	stEndTagName       // in an end tag's name
	stAfterEndTagName  // whitespace before an end tag's `>`
	stInTag            // between attributes
	stAttrName         // in an attribute's name
	stAfterAttrName    // whitespace after an attribute's name
	stBeforeAttrValue  // after `=`
	stAttrValueQuoted  // in a value quoted with p.quote
	stAttrValueBare    // in an unquoted value
	stAfterOptional    // just after an optional attribute's slot
	stSelfClosingSlash // after a `/` in a tag
)

// openElement is an element whose end tag has not been seen.
type openElement struct {
	name    string
	pos     Pos
	foreign bool // inside <svg> or <math>, or one of them
}

type parser struct {
	parts []string
	opts  Options

	part, off int // the current position
	st        state

	stack []openElement

	// The tag being read.
	tagName    strings.Builder
	tagPos     Pos
	endTagName strings.Builder

	// The attribute being read.
	attrName     strings.Builder
	attrWsStart  int  // offset in the current part of the space before the name
	attrWsValid  bool // the space and the name are in the current part
	inTagWsStart int  // start of the current run of space in a tag, or -1
	quote        byte
	valuePrefix  strings.Builder // the value so far; a slot is "\x00"

	// The element whose raw text or RCDATA is being read.
	rawElem string

	sawContent bool // anything but space before, for <!DOCTYPE>

	statics []string
	slots   []Slot
	errs    []Error

	// events is each tag and comment in order (`<p`, `/p`, `!--`), which the
	// escaping property compares across renderings (template_fuzz_test.go).
	events []string
}

func (p *parser) errorAt(pos Pos, format string, args ...any) {
	p.errs = append(p.errs, Error{Pos: pos, Message: fmt.Sprintf(format, args...)})
}

func (p *parser) here() Pos { return Pos{Part: p.part, Offset: p.off} }

func (p *parser) line(pos Pos) int {
	if p.opts.Line != nil {
		return p.opts.Line(pos)
	}
	n := 1
	for i := 0; i < pos.Part && i < len(p.parts); i++ {
		n += strings.Count(p.parts[i], "\n")
	}
	if pos.Part < len(p.parts) {
		n += strings.Count(p.parts[pos.Part][:pos.Offset], "\n")
	}
	return n
}

func (p *parser) foreign() bool {
	return len(p.stack) > 0 && p.stack[len(p.stack)-1].foreign
}

func (p *parser) run() {
	for p.part = 0; p.part < len(p.parts); p.part++ {
		s := p.parts[p.part]
		static := s
		p.inTagWsStart = -1
		p.attrWsValid = false
		for p.off = 0; p.off < len(s); {
			p.step(s)
		}
		if p.part < len(p.parts)-1 {
			static = p.slot(static)
		}
		p.statics = append(p.statics, static)
	}
	p.finish()
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

func isLetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// step consumes at least one byte of s at p.off.
func (p *parser) step(s string) {
	c := s[p.off]
	switch p.st {
	case stText:
		if c == '<' {
			p.tagPos = p.here()
			p.st = stTagOpen
		} else if !isSpace(c) {
			p.sawContent = true
		}
		p.off++

	case stTagOpen:
		switch {
		case isLetter(c):
			p.tagName.Reset()
			p.st = stTagName
			return // the letter is the name's first
		case c == '/':
			p.st = stEndTagOpen
			p.off++
		case c == '!':
			p.markupDeclaration(s)
		case c == '?':
			p.errorAt(p.tagPos, "`<?` is not HTML; a template holds elements, text and comments")
			p.skipToTagEnd(s)
		default:
			p.sawContent = true
			p.st = stText // a `<` that starts no tag is text
		}

	case stEndTagOpen:
		if isLetter(c) {
			p.endTagName.Reset()
			p.st = stEndTagName
			return
		}
		p.errorAt(p.tagPos, "`</` begins an end tag, and an end tag is a name and `>`")
		p.skipToTagEnd(s)

	case stTagName:
		switch {
		case isSpace(c):
			p.startTagNamed()
			p.st = stInTag
			p.inTagWsStart = p.off
			p.off++
		case c == '>':
			p.startTagNamed()
			p.endStartTag(false)
			p.off++
		case c == '/':
			p.startTagNamed()
			p.st = stSelfClosingSlash
			p.off++
		default:
			p.tagName.WriteByte(c)
			p.off++
		}

	case stEndTagName:
		switch {
		case isSpace(c):
			p.st = stAfterEndTagName
			p.off++
		case c == '>':
			p.off++
			p.endTag()
		default:
			p.endTagName.WriteByte(c)
			p.off++
		}

	case stAfterEndTagName:
		if c == '>' {
			p.off++
			p.endTag()
			return
		}
		if !isSpace(c) {
			p.errorAt(p.here(), "an end tag has no attributes: write `</%s>`", strings.ToLower(p.endTagName.String()))
			p.skipToTagEnd(s)
			p.endTag()
			return
		}
		p.off++

	case stInTag, stAfterAttrName:
		switch {
		case isSpace(c):
			if p.inTagWsStart < 0 {
				p.inTagWsStart = p.off
			}
			p.off++
		case c == '>':
			p.endStartTag(false)
			p.off++
		case c == '/':
			p.st = stSelfClosingSlash
			p.off++
		case c == '=' && p.st == stAfterAttrName:
			p.st = stBeforeAttrValue
			p.off++
		case c == '"' || c == '\'' || c == '<' || c == '=':
			p.errorAt(p.here(), "unexpected `%c` in `<%s>`: an attribute is a name, and `=` and a value", c, p.currentTag())
			p.off++
		default:
			if p.st == stInTag && p.inTagWsStart < 0 && !p.attrWsValid {
				// Attributes must be separated by space: `a="1"b="2"`.
				p.errorAt(p.here(), "put a space before the attribute `%s` in `<%s>`", attrNameAt(s, p.off), p.currentTag())
			}
			p.attrName.Reset()
			p.attrWsStart, p.attrWsValid = p.inTagWsStart, p.inTagWsStart >= 0
			p.inTagWsStart = -1
			p.st = stAttrName
		}

	case stAttrName:
		switch {
		case isSpace(c):
			p.st = stAfterAttrName
			p.inTagWsStart = p.off
			p.off++
		case c == '=':
			p.st = stBeforeAttrValue
			p.off++
		case c == '>' || c == '/':
			p.st = stInTag
			p.inTagWsStart = -1
			p.attrWsValid = true // `<input disabled/>`: no space needed before `/`
		case c == '"' || c == '\'' || c == '<':
			p.errorAt(p.here(), "unexpected `%c` in an attribute's name", c)
			p.off++
		default:
			p.attrName.WriteByte(c)
			p.off++
		}

	case stBeforeAttrValue:
		switch {
		case isSpace(c):
			p.off++
		case c == '"' || c == '\'':
			p.quote = c
			p.valuePrefix.Reset()
			p.st = stAttrValueQuoted
			p.off++
		case c == '>':
			p.errorAt(p.here(), "`%s=` has no value", p.attrName.String())
			p.endStartTag(false)
			p.off++
		default:
			p.valuePrefix.Reset()
			p.st = stAttrValueBare
		}

	case stAttrValueQuoted:
		if c == p.quote {
			p.st = stInTag
			p.inTagWsStart = -1
			p.attrWsValid = false
		} else {
			p.valuePrefix.WriteByte(c)
		}
		p.off++

	case stAttrValueBare:
		switch {
		case isSpace(c):
			p.st = stInTag
			p.inTagWsStart = p.off
		case c == '>':
			p.endStartTag(false)
		case c == '"' || c == '\'' || c == '<' || c == '=' || c == '`':
			p.errorAt(p.here(), "unexpected `%c` in an unquoted attribute value; quote the value", c)
		}
		p.off++

	case stAfterOptional:
		switch {
		case isSpace(c):
			p.st = stInTag
			p.inTagWsStart = p.off
			p.off++
		case c == '>':
			p.endStartTag(false)
			p.off++
		case c == '/':
			p.st = stSelfClosingSlash
			p.off++
		default:
			p.errorAt(Pos{Part: p.part - 1, Offset: len(p.parts[p.part-1])},
				"quote the attribute's value to put a slot in it: `%s=\"...${...}...\"`", p.attrName.String())
			p.st = stAttrValueBare
		}

	case stSelfClosingSlash:
		if c == '>' {
			p.endStartTag(true)
			p.off++
			return
		}
		if isSpace(c) {
			p.off++
			return
		}
		// `<a href=x/y>` never gets here; a stray `/` is only allowed before `>`.
		p.errorAt(p.here(), "a `/` in a tag is followed by `>`")
		p.st = stInTag
		p.inTagWsStart = -1
		p.attrWsValid = true

	case stRCDATA, stRawText:
		if c == '<' && p.closesRaw(s) {
			p.tagPos = p.here()
			p.off += 2
			p.endTagName.Reset()
			p.st = stEndTagName
			return
		}
		p.off++

	case stComment:
		if strings.HasPrefix(s[p.off:], "-->") {
			p.off += 3
			p.st = stText
			return
		}
		p.off++

	case stDoctype:
		if c == '>' {
			p.st = stText
		}
		p.off++
	}
}

// attrNameAt is the attribute name that starts at s[i], for a message.
func attrNameAt(s string, i int) string {
	j := i
	for j < len(s) && !isSpace(s[j]) && s[j] != '=' && s[j] != '>' && s[j] != '/' {
		j++
	}
	return s[i:j]
}

func (p *parser) currentTag() string { return strings.ToLower(p.tagName.String()) }

// markupDeclaration reads what follows `<!`: a comment, or a doctype at
// the literal's start.
func (p *parser) markupDeclaration(s string) {
	rest := s[p.off:]
	switch {
	case strings.HasPrefix(rest, "!--"):
		p.events = append(p.events, "!--")
		p.sawContent = true
		p.off += 3
		p.st = stComment
	case len(rest) >= 8 && strings.EqualFold(rest[1:8], "DOCTYPE"):
		if p.sawContent || len(p.stack) > 0 || p.part > 0 {
			p.errorAt(p.tagPos, "`<!DOCTYPE html>` comes first in a literal")
		}
		if !strings.EqualFold(strings.TrimSpace(strings.SplitN(rest[8:], ">", 2)[0]), "html") {
			p.errorAt(p.tagPos, "the only doctype is `<!DOCTYPE html>`")
		}
		p.sawContent = true
		p.off += 8
		p.st = stDoctype
	default:
		p.errorAt(p.tagPos, "`<!` begins only a comment, `<!-- ... -->`, or `<!DOCTYPE html>`")
		p.skipToTagEnd(s)
	}
}

// skipToTagEnd recovers from a bad tag by skipping past its `>`.
func (p *parser) skipToTagEnd(s string) {
	if i := strings.IndexByte(s[p.off:], '>'); i >= 0 {
		p.off += i + 1
	} else {
		p.off = len(s)
	}
	p.st = stText
}

// closesRaw reports whether s at p.off is the end tag of the raw text or
// RCDATA element being read.
func (p *parser) closesRaw(s string) bool {
	rest := s[p.off:]
	n := 2 + len(p.rawElem)
	if len(rest) < n || rest[1] != '/' || !strings.EqualFold(rest[2:n], p.rawElem) {
		return false
	}
	return len(rest) == n || isSpace(rest[n]) || rest[n] == '>' || rest[n] == '/'
}

// startTagNamed checks the name of the start tag just read.
func (p *parser) startTagNamed() {
	p.sawContent = true
	name := p.currentTag()
	if !knownElement(name) {
		p.errorAt(p.tagPos, "unknown element `<%s>`; a custom element's name has a hyphen", name)
	}
	p.attrWsValid = false
}

// endStartTag finishes a start tag at its `>`.
func (p *parser) endStartTag(selfClosing bool) {
	name := p.currentTag()
	p.events = append(p.events, "<"+name)
	p.st = stText
	isForeign := p.foreign() || name == "svg" || name == "math"
	if voidElements[name] && !isForeign {
		return
	}
	if selfClosing {
		if !isForeign {
			p.errorAt(p.tagPos, "a `/>` does not close `<%s>` in HTML; write `</%s>`", name, name)
		}
		return
	}
	p.stack = append(p.stack, openElement{name: name, pos: p.tagPos, foreign: isForeign})
	switch {
	case name == "script" || name == "style":
		p.rawElem, p.st = name, stRawText
	case !isForeign && (name == "title" || name == "textarea"):
		p.rawElem, p.st = name, stRCDATA
	}
}

// endTag finishes an end tag at its `>`.
func (p *parser) endTag() {
	p.st = stText
	name := strings.ToLower(p.endTagName.String())
	p.events = append(p.events, "/"+name)
	if voidElements[name] && !p.foreign() {
		p.errorAt(p.tagPos, "`<%s>` has no end tag: it is a void element", name)
		return
	}
	if len(p.stack) == 0 {
		p.errorAt(p.tagPos, "`</%s>` closes nothing: no `<%s>` is open in this literal", name, name)
		return
	}
	top := p.stack[len(p.stack)-1]
	if top.name == name {
		p.stack = p.stack[:len(p.stack)-1]
		return
	}
	p.errorAt(p.tagPos, "`</%s>` closes `<%s>`, opened at line %d", name, top.name, p.line(top.pos))
	// Recover: close up to a matching element if there is one.
	for i := len(p.stack) - 1; i >= 0; i-- {
		if p.stack[i].name == name {
			p.stack = p.stack[:i]
			return
		}
	}
}

// slot handles the slot after the current part, whose static text is
// static, and returns the static text to emit before it.
func (p *parser) slot(static string) string {
	pos := Pos{Part: p.part, Offset: len(p.parts[p.part])}
	add := func(ctx Context, attr string) {
		p.slots = append(p.slots, Slot{Context: ctx, Attr: attr})
	}
	// A slot is content: the doctype may not follow it.
	p.sawContent = true
	attr := strings.ToLower(p.attrName.String())
	switch p.st {
	case stText:
		add(ContextText, "")
		return static

	case stRCDATA:
		add(ContextRCDATA, "")
		return static

	case stRawText:
		p.errorAt(pos, "a slot in a `<%s>` element would be %s; pass the value in a `data-` attribute", p.rawElem, rawLanguage(p.rawElem))

	case stComment:
		p.errorAt(pos, "a slot in an HTML comment is not rendered safely")

	case stBeforeAttrValue:
		switch {
		case isEventHandler(attr):
			p.errorAt(pos, "a slot in `%s` would be JavaScript; pass the value in a `data-` attribute and read it in a script", attr)
		case attr == "srcset":
			p.errorAt(pos, "a slot in `srcset` is not supported; build the attribute from `SafeUrl` values")
		case attr == "style" || isURLAttribute(p.currentTag(), attr):
			p.errorAt(pos, "quote `%s`'s value to put a slot in it: `%s=\"${...}\"`", attr, attr)
		case !p.attrWsValid:
			// The attribute's name began in an earlier part, which only a
			// slot ends; the name could not have come from there.
			p.errorAt(pos, "a slot cannot name a tag or an attribute: `<%s ${...}>`", p.currentTag())
		default:
			add(ContextOptionalAttribute, p.attrName.String())
			p.st = stAfterOptional
			return static[:p.attrWsStart]
		}

	case stAttrValueBare, stAfterOptional:
		p.errorAt(pos, "quote the attribute's value to put a slot in it: `%s=\"...${...}...\"`", p.attrName.String())

	case stAttrValueQuoted:
		ctx, ok := p.quotedContext(pos, attr)
		p.valuePrefix.WriteByte(0)
		if ok {
			add(ctx, attr)
		}
		return static

	case stDoctype:
		p.errorAt(pos, "the only doctype is `<!DOCTYPE html>`")

	default:
		p.errorAt(pos, "a slot cannot name a tag or an attribute: `<${tag}>`")
	}
	// The slot was refused. Record a text slot so slot numbers stay aligned.
	add(ContextText, "")
	return static
}

func rawLanguage(elem string) string {
	if elem == "style" {
		return "CSS"
	}
	return "JavaScript"
}

// quotedContext is the context of a slot inside a quoted value of attr.
func (p *parser) quotedContext(pos Pos, attr string) (Context, bool) {
	switch {
	case isEventHandler(attr):
		p.errorAt(pos, "a slot in `%s` would be JavaScript; pass the value in a `data-` attribute and read it in a script", attr)
		return 0, false
	case attr == "srcset":
		p.errorAt(pos, "a slot in `srcset` is not supported; build the attribute from `SafeUrl` values")
		return 0, false
	case attr == "style":
		return ContextCSS, true
	case !isURLAttribute(p.currentTag(), attr):
		return ContextAttr, true
	}
	prefix := strings.TrimLeft(p.valuePrefix.String(), " \t\n\r\f")
	switch {
	case prefix == "":
		return ContextURLStart, true
	case inURLAuthority(prefix):
		p.errorAt(pos, "a slot in a URL's scheme or host cannot be escaped: `%s=\"https://${host}/\"`", attr)
		return 0, false
	case strings.ContainsRune(prefix, '#'):
		return ContextURLFragment, true
	case strings.ContainsRune(prefix, '?'):
		return ContextURLQuery, true
	}
	return ContextURLPath, true
}

// inURLAuthority reports whether a URL that begins with prefix is still in
// its scheme or host: `https://`, `https://example.`, `//cdn.`.
func inURLAuthority(prefix string) bool {
	rest := ""
	if strings.HasPrefix(prefix, "//") {
		rest = prefix[2:]
	} else if i := strings.Index(prefix, "://"); i > 0 && isScheme(prefix[:i]) {
		rest = prefix[i+3:]
	} else {
		return false
	}
	return !strings.ContainsAny(rest, "/?#")
}

func isScheme(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(isLetter(c) || i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.')) {
			return false
		}
	}
	return s != ""
}

// finish reports what the literal leaves open at its end.
func (p *parser) finish() {
	end := Pos{Part: len(p.parts) - 1}
	if len(p.parts) > 0 {
		end.Offset = len(p.parts[len(p.parts)-1])
	}
	switch p.st {
	case stText:
	case stComment:
		p.errorAt(end, "a comment is not closed in this literal: end it with `-->`")
	case stRCDATA, stRawText:
		// The element itself is reported as unclosed below.
	case stAttrValueQuoted:
		p.errorAt(end, "the value of `%s` is not closed: end it with `%c`", p.attrName.String(), p.quote)
	default:
		p.errorAt(end, "a tag is not closed in this literal: end it with `>`")
	}
	for i := len(p.stack) - 1; i >= 0; i-- {
		e := p.stack[i]
		p.errorAt(e.pos, "`<%s>` is not closed in this literal", e.name)
	}
}
