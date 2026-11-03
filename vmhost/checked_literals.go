package vmhost

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

// CHECKED LITERALS ARE CHECKED WHEN THE PROGRAM IS CHECKED.
//
// A typed literal whose tag implements CheckedLiteral (spec §18 *Checked
// literals*) has a site (irbuild's irchecked.go) whose cell holds its
// check's Result over the literal's statics. This runs each site as
// checkLiteral runs a backtick literal's handler: under vm.Machine.Effects,
// the same step budget and the same deadline. An Err is a compile error at
// the static and offset its LiteralError names, mapped back to the source;
// an effectful check is one error at the impl, not one per literal.

// checkedLiteralHint closes the diagnostic of a check that has effects.
const checkedLiteralHint = "a checked literal's check runs when the program is checked, so it may not print, read files or the clock, start tasks or read an application field"

// checkCheckedLiteral runs one checked literal's check and answers its
// diagnostic when the literal is a compile error. effectful holds the checks
// already reported as effectful, which are reported once.
func (p *Program) checkCheckedLiteral(m *vm.Machine, site irbuild.LiteralSite, effectful map[any]bool) (d frontend.Diagnostic, failed bool, err error) {
	f := site.Cell.Initializer()
	if f == nil {
		return d, false, nil
	}
	if len(m.Unretained([]*ir.Func{f}, nil)) > 0 {
		return d, false, nil
	}
	path := site.Path
	if path == "" {
		path = p.moduleWithNodeAt(site.Line, site.Col)
	}
	at := func(line, col int, msg string, hints ...string) (frontend.Diagnostic, bool, error) {
		d := frontend.NewDiagnostic(path, p.sources[path], line, col, msg)
		d.Hints = append(d.Hints, hints...)
		d.Code = InvalidLiteralCode
		return d, true, nil
	}
	label := literalLabel(site.Text)
	if effects := m.Effects(f); len(effects) > 0 {
		if effectful[site.Check] {
			return d, false, nil
		}
		effectful[site.Check] = true
		msg := fmt.Sprintf("%s has effects (%s); a checked literal's check runs when the program is checked", site.CheckName, strings.Join(effects, "; "))
		if site.CheckLine > 0 {
			return at(site.CheckLine, site.CheckCol, msg, checkedLiteralHint)
		}
		return at(site.Line, site.Col, msg, checkedLiteralHint)
	}
	lim := vm.Limits{Steps: literalSteps, Deadline: time.Now().Add(literalTime)}
	v, err := m.Evaluate(context.Background(), f, nil, lim)
	if errors.Is(err, vm.ErrLimit) {
		return at(site.Line, site.Col, fmt.Sprintf("%s cannot be checked: %s did not finish within %d steps", label, site.CheckName, literalSteps))
	}
	if err != nil {
		failure, _ := programFailure(err)
		var internal *InternalError
		if errors.As(failure, &internal) {
			return d, false, internal
		}
		return at(site.Line, site.Col, fmt.Sprintf("%s cannot be checked: %s failed: %s", label, site.CheckName, failure.Error()))
	}
	rec, ok := v.(*rt.Record)
	if !ok || rec == nil || rec.Desc.Kind != rt.KindEnum || rec.NumFields() != 1 || rec.Variant().Name == "Ok" {
		return d, false, nil
	}
	payload := rec.Field(0)
	fragment, offset := literalErrorPlace(payload)
	// A program loaded from disk keeps no text, so the file is read here: the
	// position of a static's character needs the source it was written in.
	src, _ := p.sourceOf(path)
	line, col := checkedLiteralPos(src, site, fragment, offset)
	return at(line, col, fmt.Sprintf("%s is invalid: %s", label, p.literalMessage(m, site, payload)))
}

// literalErrorPlace is a LiteralError's static index and offset.
func literalErrorPlace(v any) (fragment, offset int) {
	switch e := v.(type) {
	case rt.LiteralError:
		return int(e.Fragment), int(e.Offset)
	case *rt.LiteralError:
		if e != nil {
			return int(e.Fragment), int(e.Offset)
		}
	case *rt.Record:
		if e != nil {
			return recordInt(e, "fragment"), recordInt(e, "offset")
		}
	}
	return 0, 0
}

// recordInt is the Int field name of r, or -1 when it has none that fits.
func recordInt(r *rt.Record, name string) int {
	v, ok := r.FieldNamed(name)
	if !ok {
		return -1
	}
	switch n := v.(type) {
	case int64:
		return int(n)
	case rt.Int:
		if i, small := n.Int64(); small {
			return int(i)
		}
	}
	return -1
}

// checkedLiteralPos is the source position of the code point offset counts
// into the literal's static fragment, as its check received it. An offset at
// a static's end, with a slot after it, is that slot's `${`. A position this
// cannot place lands on the literal.
func checkedLiteralPos(src string, site irbuild.LiteralSite, fragment, offset int) (int, int) {
	if fragment < 0 || fragment >= len(site.Statics) || offset < 0 {
		return site.Line, site.Col
	}
	if offset >= utf8.RuneCountInString(site.Statics[fragment]) && fragment < len(site.Slots) {
		s := site.Slots[fragment]
		if line, col, ok := slotOpening(src, s.Line, s.Col); ok {
			return line, col
		}
		return s.Line, s.Col
	}
	if fragment == 0 && offset == 0 {
		return site.Line, site.Col
	}
	if line, col, ok := scanStatic(src, site, fragment, offset); ok {
		return line, col
	}
	return site.Line, site.Col
}

// sourceLines splits src into its lines, without their newlines.
func sourceLines(src string) []string {
	return strings.Split(src, "\n")
}

// slotOpening is the position of the `${` before the slot expression at
// line:col, searched back on that line.
func slotOpening(src string, line, col int) (int, int, bool) {
	lines := sourceLines(src)
	if line < 1 || line > len(lines) {
		return 0, 0, false
	}
	text := []rune(lines[line-1])
	for i := min(col-2, len(text)-2); i >= 0; i-- {
		if text[i] == '$' && text[i+1] == '{' {
			return line, i + 1, true
		}
	}
	return 0, 0, false
}

// scanStatic walks the literal's source from its opening quote, decoding as
// the lexer does, and answers the position of the offset-th code point of
// static fragment. It reads double-quoted bodies, with their escapes and
// slots, and backtick bodies; a triple-quoted body is read line by line with
// its common indentation, the closing quotes' column, stripped.
func scanStatic(src string, site irbuild.LiteralSite, fragment, offset int) (int, int, bool) {
	lines := sourceLines(src)
	if site.Line < 1 || site.Line > len(lines) {
		return 0, 0, false
	}
	row := []rune(lines[site.Line-1])
	i := site.Col - 1 + utf8.RuneCountInString(site.Tag)
	if i >= len(row) {
		return 0, 0, false
	}
	quote := row[i]
	triple := quote == '"' && i+2 < len(row) && row[i+1] == '"' && row[i+2] == '"'
	line, col := site.Line, i+1
	advance := 1
	if triple {
		advance = 3
	}
	col += advance
	indent := 0
	if triple {
		// The body starts on the next line, and loses as much indentation as
		// the closing quotes' line has.
		for j := site.Line; j < len(lines); j++ {
			trimmed := strings.TrimLeft(lines[j], " \t")
			if strings.HasPrefix(trimmed, `"""`) {
				indent = len(lines[j]) - len(trimmed)
				break
			}
		}
		line, col = site.Line+1, indent+1
	}
	static, count := 0, 0
	at := func() rune {
		r := []rune(lines[line-1])
		if col-1 < len(r) {
			return r[col-1]
		}
		return '\n'
	}
	for steps := 0; steps < 100000 && line <= len(lines); steps++ {
		if static == fragment && count == offset {
			return line, col, true
		}
		c := at()
		switch {
		case c == '\n':
			if !triple {
				return 0, 0, false
			}
			line, col = line+1, indent+1
			count++
		case quote == '`' && c == '`':
			return 0, 0, false
		case !triple && quote == '"' && c == '"':
			return 0, 0, false
		case triple && c == '"' && strings.HasPrefix(string([]rune(lines[line-1])[col-1:]), `"""`):
			return 0, 0, false
		case quote != '`' && c == '\\':
			r := []rune(lines[line-1])
			if col < len(r) && r[col] == 'u' && col+1 < len(r) && r[col+1] == '{' {
				for col-1 < len(r) && r[col-1] != '}' {
					col++
				}
				col++
			} else {
				col += 2
			}
			count++
		case quote != '`' && c == '$' && col < len([]rune(lines[line-1])) && []rune(lines[line-1])[col] == '{':
			depth := 0
			for line <= len(lines) {
				d := at()
				if d == '{' {
					depth++
				} else if d == '}' {
					depth--
					if depth == 0 {
						col++
						break
					}
				}
				if d == '\n' {
					line, col = line+1, 1
					continue
				}
				col++
			}
			static, count = static+1, 0
		default:
			col++
			count++
		}
	}
	return 0, 0, false
}
