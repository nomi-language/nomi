package stdxml

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/nomi-language/nomi/internal/lineinput"
	"github.com/nomi-language/nomi/rt"
)

const (
	xmlSpace   = "http://www.w3.org/XML/1998/namespace"
	xmlnsSpace = "http://www.w3.org/2000/xmlns/"
)

// The kinds of token Next returns.
const (
	kindStop        = 0
	kindStart       = 1
	kindEnd         = 2
	kindText        = 3
	kindComment     = 4
	kindInstruction = 5
	kindDoctype     = 6
)

type tokens struct {
	in           *lineinput.Input
	dec          *xml.Decoder
	allowDoctype bool

	// scopes is the namespace bindings of each open element, innermost
	// last; open is each open element's name as written.
	scopes []map[string]string
	open   []xml.Name

	seenRoot, rootClosed bool

	// The current token.
	name   []string
	attrs  []string
	spaces []string
	text   string
	target string
	line   int64
	column int64

	stop failure
	done bool
}

// failure is how reading stopped: kind 0 at the end, 1 a syntax error at
// line and column, 2 the Reader failed.
type failure struct {
	kind    int64
	line    int64
	column  int64
	message string
}

// Open is a token reader of the XML in r. A DOCTYPE is an error unless
// allowDoctype, which makes it a token and reads no declaration in it.
func Open(r rt.Reader, allowDoctype bool) rt.XmlTokens {
	in := lineinput.New(r)
	dec := xml.NewDecoder(in)
	dec.Strict = true
	return rt.NewXmlTokens(&tokens{in: in, dec: dec, allowDoctype: allowDoctype})
}

func of(h rt.XmlTokens) *tokens {
	t, ok := h.Tokens().(*tokens)
	if !ok {
		rt.Trap("xml: the value is not an XML token reader")
	}
	return t
}

// Next reads the next token and returns its kind, or 0 when reading has
// stopped: at the end of the document, or on a failure Stopped describes.
func Next(fr *rt.Frame, h rt.XmlTokens) int64 {
	t := of(h)
	if t.done {
		return kindStop
	}
	t.in.Frame = fr
	defer func() { t.in.Frame = nil }()
	for {
		line, col := t.dec.InputPos()
		t.in.Forget(line)
		offset := t.dec.InputOffset()
		t.line, t.column = int64(line), int64(t.in.Column(line, col))
		tok, err := t.dec.RawToken()
		if err != nil {
			return t.failed(err)
		}
		kind, skip, msg := t.take(tok, offset)
		if msg != "" {
			return t.syntax(msg, t.line, t.column)
		}
		if !skip {
			return kind
		}
	}
}

func (t *tokens) syntax(msg string, line, column int64) int64 {
	t.done = true
	t.stop = failure{kind: 1, line: line, column: column, message: msg}
	return kindStop
}

func (t *tokens) failed(err error) int64 {
	t.done = true
	var io_ rt.IOFailure
	if errors.As(err, &io_) {
		t.stop = failure{kind: 2}
		return kindStop
	}
	line, col := t.dec.InputPos()
	column := int64(t.in.Column(line, col))
	if errors.Is(err, io.EOF) {
		switch {
		case len(t.open) > 0:
			return t.syntax("unexpected end of input: <"+written(t.open[len(t.open)-1])+"> is not closed",
				int64(line), column)
		case !t.seenRoot:
			return t.syntax("the document has no root element", int64(line), column)
		}
		t.stop = failure{}
		return kindStop
	}
	var se *xml.SyntaxError
	if errors.As(err, &se) {
		if se.Line != line {
			column = 1
		}
		return t.syntax(se.Msg, int64(se.Line), column)
	}
	return t.syntax(strings.TrimPrefix(err.Error(), "xml: "), int64(line), column)
}

func written(n xml.Name) string {
	if n.Space == "" {
		return n.Local
	}
	return n.Space + ":" + n.Local
}

// take makes tok the current token: its kind, whether it is skipped (an
// XML declaration, or white space outside the root), or why it is not
// well-formed.
func (t *tokens) take(tok xml.Token, offset int64) (kind int64, skip bool, msg string) {
	switch tok := tok.(type) {
	case xml.StartElement:
		return t.start(tok)
	case xml.EndElement:
		if len(t.open) == 0 {
			return 0, false, "</" + written(tok.Name) + "> closes no element"
		}
		top := t.open[len(t.open)-1]
		if top != tok.Name {
			return 0, false, "<" + written(top) + "> is closed by </" + written(tok.Name) + ">"
		}
		space, ok := t.lookup(tok.Name.Space)
		if !ok {
			return 0, false, "the prefix " + tok.Name.Space + " is not declared"
		}
		t.name = []string{tok.Name.Space, tok.Name.Local, space}
		t.open = t.open[:len(t.open)-1]
		t.scopes = t.scopes[:len(t.scopes)-1]
		if len(t.open) == 0 {
			t.rootClosed = true
		}
		return kindEnd, false, ""
	case xml.CharData:
		text := string(tok)
		if len(t.open) == 0 {
			if strings.TrimLeft(text, " \t\r\n") == "" {
				return 0, true, ""
			}
			return 0, false, "text outside the root element"
		}
		t.text = text
		return kindText, false, ""
	case xml.Comment:
		t.text = string(tok)
		return kindComment, false, ""
	case xml.ProcInst:
		if strings.EqualFold(tok.Target, "xml") {
			if offset != 0 || tok.Target != "xml" {
				return 0, false, "an XML declaration must be the first thing in the document"
			}
			return 0, true, ""
		}
		t.target = tok.Target
		t.text = string(tok.Inst)
		return kindInstruction, false, ""
	case xml.Directive:
		text := string(tok)
		if !strings.HasPrefix(text, "DOCTYPE") {
			return 0, false, "a <!" + firstWord(text) + "> declaration outside a DOCTYPE"
		}
		if !t.allowDoctype {
			return 0, false, "a DOCTYPE is refused: std/xml reads no DTD; allow_doctype skips it"
		}
		if t.seenRoot {
			return 0, false, "a DOCTYPE after the root element"
		}
		t.text = text
		return kindDoctype, false, ""
	}
	return 0, true, ""
}

func firstWord(s string) string {
	if i := strings.IndexAny(s, " \t\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

func (t *tokens) start(tok xml.StartElement) (int64, bool, string) {
	if t.rootClosed {
		return 0, false, "a second root element <" + written(tok.Name) + ">"
	}
	t.seenRoot = true
	scope := map[string]string{}
	var spaces []string
	for _, a := range tok.Attr {
		isDecl := a.Name.Space == "" && a.Name.Local == "xmlns" || a.Name.Space == "xmlns"
		if isDecl {
			p := a.Name.Local
			if a.Name.Space == "" {
				p = ""
			}
			if _, dup := scope[p]; dup {
				return 0, false, "the namespace declaration " + written(a.Name) + " appears twice"
			}
		}
		switch {
		case a.Name.Space == "" && a.Name.Local == "xmlns":
			if a.Value == xmlSpace || a.Value == xmlnsSpace {
				return 0, false, "the namespace " + a.Value + " cannot be the default"
			}
			scope[""] = a.Value
			spaces = append(spaces, "", a.Value)
		case a.Name.Space == "xmlns":
			p := a.Name.Local
			switch {
			case p == "xmlns":
				return 0, false, "the prefix xmlns cannot be declared"
			case p == "xml" && a.Value != xmlSpace:
				return 0, false, "the prefix xml is bound to " + xmlSpace + " and no other namespace"
			case a.Value == "":
				return 0, false, "the prefix " + p + " cannot be bound to no namespace"
			case p != "xml" && (a.Value == xmlSpace || a.Value == xmlnsSpace):
				return 0, false, "the namespace " + a.Value + " cannot be bound to the prefix " + p
			}
			scope[p] = a.Value
			spaces = append(spaces, p, a.Value)
		}
	}
	t.scopes = append(t.scopes, scope)
	t.open = append(t.open, tok.Name)
	space, ok := t.lookup(tok.Name.Space)
	if !ok {
		return 0, false, "the prefix " + tok.Name.Space + " is not declared"
	}
	t.name = []string{tok.Name.Space, tok.Name.Local, space}
	t.attrs = t.attrs[:0]
	seen := map[[2]string]bool{}
	for _, a := range tok.Attr {
		if (a.Name.Space == "" && a.Name.Local == "xmlns") || a.Name.Space == "xmlns" {
			continue
		}
		space := ""
		if a.Name.Space != "" {
			s, ok := t.lookup(a.Name.Space)
			if !ok {
				return 0, false, "the prefix " + a.Name.Space + " is not declared"
			}
			space = s
		}
		key := [2]string{space, a.Name.Local}
		if seen[key] {
			return 0, false, "the attribute " + written(a.Name) + " appears twice"
		}
		seen[key] = true
		t.attrs = append(t.attrs, a.Name.Space, a.Name.Local, space, a.Value)
	}
	t.spaces = spaces
	return kindStart, false, ""
}

// lookup is the namespace prefix is bound to, "" for no prefix with no
// default namespace.
func (t *tokens) lookup(prefix string) (string, bool) {
	if prefix == "xml" {
		return xmlSpace, true
	}
	for i := len(t.scopes) - 1; i >= 0; i-- {
		if s, ok := t.scopes[i][prefix]; ok {
			return s, true
		}
	}
	return "", prefix == ""
}

// Name is the current start or end tag's name: its prefix, local name and
// namespace.
func Name(h rt.XmlTokens) *rt.List[string] {
	return rt.ListFromGo(of(h).name, same)
}

// Attributes is the current start tag's attributes, four items each: the
// prefix, the local name, the namespace and the value. Namespace
// declarations are not among them.
func Attributes(h rt.XmlTokens) *rt.List[string] {
	return rt.ListFromGo(of(h).attrs, same)
}

// Namespaces is the current start tag's namespace declarations, two items
// each: the prefix ("" for the default namespace) and the namespace.
func Namespaces(h rt.XmlTokens) *rt.List[string] {
	return rt.ListFromGo(of(h).spaces, same)
}

// Text is the current text, comment, instruction's data or DOCTYPE.
func Text(h rt.XmlTokens) string { return of(h).text }

// Target is the current instruction's target.
func Target(h rt.XmlTokens) string { return of(h).target }

// Position is the line and column, in code points, where the current
// token starts.
func Position(h rt.XmlTokens) *rt.List[int64] {
	t := of(h)
	return rt.ListFromGo([]int64{t.line, t.column}, func(n int64) int64 { return n })
}

// Stopped is why reading stopped, as [kind, line, column]: kind 0 at the end of the document or
// while it has not, 1 a syntax error at line and column, 2 the Reader
// failed (Failure has its error).
func Stopped(h rt.XmlTokens) *rt.List[int64] {
	s := of(h).stop
	return rt.ListFromGo([]int64{s.kind, s.line, s.column}, func(n int64) int64 { return n })
}

// Message is the message of the failure Stopped describes.
func Message(h rt.XmlTokens) string { return of(h).stop.message }

// Failure is the io.Error the Reader failed with, after Stopped said kind
// 2.
func Failure(h rt.XmlTokens) rt.IOError {
	t := of(h)
	if t.in.Failure == nil {
		rt.Trap("xml: the reader did not fail")
	}
	return *t.in.Failure
}

func same(s string) string { return s }

// EscapeText is s as XML character data: &, < and > as entities, a
// carriage return as &#13; so a reader keeps it, and each character XML
// 1.0 cannot hold as U+FFFD.
func EscapeText(s string) string {
	return escape(s, false)
}

// EscapeAttribute is s as a double-quoted attribute value: &, < and " as
// entities, and a tab, line feed and carriage return as character
// references, so a reader's attribute normalization keeps them.
func EscapeAttribute(s string) string {
	return escape(s, true)
}

func escape(s string, attribute bool) string {
	var b bytes.Buffer
	for _, r := range s {
		switch {
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>' && !attribute:
			b.WriteString("&gt;")
		case r == '"' && attribute:
			b.WriteString("&quot;")
		case r == '\r':
			b.WriteString("&#13;")
		case r == '\n' && attribute:
			b.WriteString("&#10;")
		case r == '\t' && attribute:
			b.WriteString("&#9;")
		case !isChar(r):
			b.WriteRune(utf8.RuneError)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isChar is XML 1.0's Char production.
func isChar(r rune) bool {
	return r == 0x09 || r == 0x0A || r == 0x0D ||
		r >= 0x20 && r <= 0xD7FF ||
		r >= 0xE000 && r <= 0xFFFD ||
		r >= 0x10000 && r <= 0x10FFFF
}

// IsName reports whether s is an XML name with no colon (an NCName), as
// the fifth edition of XML 1.0 defines a name.
func IsName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == utf8.RuneError {
			return false
		}
		if i == 0 && !isNameStart(r) || i > 0 && !isNameChar(r) {
			return false
		}
	}
	return true
}

func isNameStart(r rune) bool {
	return r >= 'A' && r <= 'Z' || r == '_' || r >= 'a' && r <= 'z' ||
		r >= 0xC0 && r <= 0xD6 || r >= 0xD8 && r <= 0xF6 || r >= 0xF8 && r <= 0x2FF ||
		r >= 0x370 && r <= 0x37D || r >= 0x37F && r <= 0x1FFF || r >= 0x200C && r <= 0x200D ||
		r >= 0x2070 && r <= 0x218F || r >= 0x2C00 && r <= 0x2FEF || r >= 0x3001 && r <= 0xD7FF ||
		r >= 0xF900 && r <= 0xFDCF || r >= 0xFDF0 && r <= 0xFFFD || r >= 0x10000 && r <= 0xEFFFF
}

func isNameChar(r rune) bool {
	return isNameStart(r) || r == '-' || r == '.' || r >= '0' && r <= '9' || r == 0xB7 ||
		r >= 0x300 && r <= 0x36F || r >= 0x203F && r <= 0x2040
}
