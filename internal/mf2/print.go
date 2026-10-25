package mf2

import (
	"sort"
	"strings"
)

// Print writes m's canonical text: one declaration per line, `.match` and
// each variant on lines of their own, one space between the parts of an
// expression, a literal unquoted when it can be. Two messages with one data
// model print the same, however they were written, so comparing printed
// texts compares messages (a translation's recorded source against the
// current one). A message with no declaration and no `.match` whose
// pattern can stand alone prints as a simple message.
func Print(m *Message) string {
	var b strings.Builder
	for _, d := range m.Declarations {
		if d.Local {
			b.WriteString(".local $" + d.Name + " = ")
		} else {
			b.WriteString(".input ")
		}
		printExpression(&b, &d.Value)
		b.WriteByte('\n')
	}
	if !m.Matches() {
		if len(m.Declarations) == 0 && simplePattern(m.Pattern) {
			printPattern(&b, m.Pattern)
			return b.String()
		}
		b.WriteString("{{")
		printPattern(&b, m.Pattern)
		b.WriteString("}}")
		return b.String()
	}
	b.WriteString(".match")
	for _, s := range m.Selectors {
		b.WriteString(" $" + s.Name)
	}
	for _, v := range m.Variants {
		b.WriteByte('\n')
		for _, k := range v.Keys {
			if k.CatchAll {
				b.WriteString("*")
			} else {
				b.WriteString(literalText(k.Value))
			}
			b.WriteByte(' ')
		}
		b.WriteString("{{")
		printPattern(&b, v.Pattern)
		b.WriteString("}}")
	}
	return b.String()
}

// simplePattern reports whether p can be written as a simple message: it
// starts with neither whitespace nor `.`, which would read as part of `o`
// or as a declaration.
func simplePattern(p Pattern) bool {
	if len(p.Parts) == 0 {
		return true
	}
	first := p.Parts[0]
	if first.Expr != nil || first.Markup != nil {
		return true
	}
	t := first.Text
	if t == "" {
		return true
	}
	return t[0] != '.' && !isWS(rune(t[0])) && !strings.HasPrefix(t, "　")
}

func printPattern(b *strings.Builder, p Pattern) {
	for _, part := range p.Parts {
		switch {
		case part.Expr != nil:
			printExpression(b, part.Expr)
		case part.Markup != nil:
			printMarkup(b, part.Markup)
		default:
			for _, r := range part.Text {
				if r == '\\' || r == '{' || r == '}' {
					b.WriteByte('\\')
				}
				b.WriteRune(r)
			}
		}
	}
}

func printExpression(b *strings.Builder, e *Expression) {
	b.WriteByte('{')
	sep := ""
	if e.Operand != nil {
		b.WriteString(operandText(*e.Operand))
		sep = " "
	}
	if e.Function != nil {
		b.WriteString(sep + ":" + e.Function.Name)
		printOptions(b, e.Function.Options)
		sep = " "
	}
	printAttributes(b, e.Attributes)
	b.WriteByte('}')
}

func printMarkup(b *strings.Builder, m *Markup) {
	b.WriteByte('{')
	if m.Kind == MarkupClose {
		b.WriteByte('/')
	} else {
		b.WriteByte('#')
	}
	b.WriteString(m.Name)
	printOptions(b, m.Options)
	printAttributes(b, m.Attributes)
	if m.Kind == MarkupStandalone {
		b.WriteByte('/')
	}
	b.WriteByte('}')
}

func printOptions(b *strings.Builder, opts []Option) {
	for _, o := range opts {
		b.WriteString(" " + o.Name + "=" + operandText(o.Value))
	}
}

func printAttributes(b *strings.Builder, attrs []Attribute) {
	for _, a := range attrs {
		b.WriteString(" @" + a.Name)
		if a.Value != nil {
			b.WriteString("=" + literalText(a.Value.Value))
		}
	}
}

func operandText(o Operand) string {
	if o.Variable {
		return "$" + o.Value
	}
	return literalText(o.Value)
}

// literalText writes a literal unquoted when it is a name or a number, and
// in `|...|` otherwise.
func literalText(s string) string {
	if IsName(s) || IsNumber(s) {
		return s
	}
	var b strings.Builder
	b.WriteByte('|')
	for _, r := range s {
		if r == '\\' || r == '|' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('|')
	return b.String()
}

// Canonical is text's canonical text, or text itself when it does not
// parse.
func Canonical(text string) string {
	m, errs := Parse(text)
	if len(errs) > 0 {
		return text
	}
	return Print(m)
}

// Same reports whether two texts are one message: their data models are
// equal once each message's declarations are taken in name order, so a
// change of layout, of quoting or of the order of declarations is no
// change. A text that does not parse is the same only as itself.
func Same(a, b string) bool {
	if a == b {
		return true
	}
	ma, ea := Parse(a)
	mb, eb := Parse(b)
	if len(ea) > 0 || len(eb) > 0 {
		return false
	}
	return sortedPrint(ma) == sortedPrint(mb)
}

func sortedPrint(m *Message) string {
	c := *m
	c.Declarations = append([]Declaration(nil), m.Declarations...)
	sort.SliceStable(c.Declarations, func(i, j int) bool { return c.Declarations[i].Name < c.Declarations[j].Name })
	return Print(&c)
}
