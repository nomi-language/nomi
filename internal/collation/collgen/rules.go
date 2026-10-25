package collgen

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nomi-language/nomi/internal/collation"
)

// The rule syntax of UTS #35 part 5 section 3 (ICU's), which CLDR's
// collation/*.xml files write their tailorings in:
//
//	&a < b << c <<< d = e       a reset, then relations at each strength
//	&[before 1]ǀ < å            a reset before a character
//	&[last regular] <* 吖阿     a reset to a special position; a star list
//	&c < ch / h                 an extension: ch sorts as c's tailoring + h
//	&ぁ <<< ぁ|ー                a prefix context
//	[reorder Grek] [caseFirst upper] [backwards 2] [import de-u-co-phonebk]
//
// Syntax characters are quoted with '…' or escaped with a backslash
// (\uXXXX, \U00XXXXXX, \x{…}). # starts a comment.

// sink receives what the parser reads.
type sink interface {
	reset(strength int, s string, position string) error
	relation(strength int, prefix, s, extension string) error
	setting(name string, args []string, set []rune) error
	importRules(tag string) error
}

// parseRules reads rules into s.
func parseRules(rules string, s sink) error {
	p := &ruleParser{src: rules, sink: s}
	return p.parse()
}

type ruleParser struct {
	src  string
	i    int
	sink sink
}

func (p *ruleParser) errorf(format string, args ...any) error {
	line := 1 + strings.Count(p.src[:p.i], "\n")
	return fmt.Errorf("rules line %d: %s", line, fmt.Sprintf(format, args...))
}

func (p *ruleParser) skipSpace() {
	for p.i < len(p.src) {
		r, n := utf8.DecodeRuneInString(p.src[p.i:])
		switch {
		case r == '#':
			for p.i < len(p.src) && p.src[p.i] != '\n' && p.src[p.i] != '\r' {
				p.i++
			}
		case isPatternSpace(r):
			p.i += n
		default:
			return
		}
	}
}

func isPatternSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x85, 0x200e, 0x200f, 0x2028, 0x2029:
		return true
	}
	return false
}

// isSyntax is ICU's set of characters a rule must quote.
func isSyntax(r rune) bool {
	return (r >= 0x21 && r <= 0x2f) || (r >= 0x3a && r <= 0x40) || (r >= 0x5b && r <= 0x60) || (r >= 0x7b && r <= 0x7e)
}

func (p *ruleParser) parse() error {
	for {
		p.skipSpace()
		if p.i >= len(p.src) {
			return nil
		}
		switch p.src[p.i] {
		case '&':
			if err := p.parseReset(); err != nil {
				return err
			}
		case '[':
			if err := p.parseSetting(); err != nil {
				return err
			}
		default:
			return p.errorf("expected a reset or a setting, found %q", p.rest())
		}
	}
}

func (p *ruleParser) rest() string {
	s := p.src[p.i:]
	if len(s) > 20 {
		s = s[:20]
	}
	return s
}

func (p *ruleParser) parseReset() error {
	p.i++ // &
	p.skipSpace()
	strength := collation.Identical
	if strings.HasPrefix(p.src[p.i:], "[before") {
		end := strings.IndexByte(p.src[p.i:], ']')
		if end < 0 {
			return p.errorf("unterminated [before")
		}
		n, err := strconv.Atoi(strings.TrimSpace(p.src[p.i+len("[before") : p.i+end]))
		if err != nil || n < 1 || n > 3 {
			return p.errorf("bad [before]")
		}
		strength = n - 1
		p.i += end + 1
		p.skipSpace()
	}
	if p.i < len(p.src) && p.src[p.i] == '[' {
		end := strings.IndexByte(p.src[p.i:], ']')
		if end < 0 {
			return p.errorf("unterminated position")
		}
		pos := strings.Join(strings.Fields(p.src[p.i+1:p.i+end]), " ")
		p.i += end + 1
		if err := p.sink.reset(strength, "", pos); err != nil {
			return p.errorf("&[%s]: %v", pos, err)
		}
	} else {
		s, err := p.parseString()
		if err != nil {
			return err
		}
		if s == "" {
			return p.errorf("empty reset")
		}
		if err := p.sink.reset(strength, s, ""); err != nil {
			return p.errorf("&%s: %v", s, err)
		}
	}
	return p.parseRelations()
}

func (p *ruleParser) parseRelations() error {
	for {
		p.skipSpace()
		if p.i >= len(p.src) {
			return nil
		}
		var strength int
		switch {
		case strings.HasPrefix(p.src[p.i:], "<<<<"):
			strength, p.i = collation.Quaternary, p.i+4
		case strings.HasPrefix(p.src[p.i:], "<<<"):
			strength, p.i = collation.Tertiary, p.i+3
		case strings.HasPrefix(p.src[p.i:], "<<"):
			strength, p.i = collation.Secondary, p.i+2
		case strings.HasPrefix(p.src[p.i:], "<"):
			strength, p.i = collation.Primary, p.i+1
		case strings.HasPrefix(p.src[p.i:], "="):
			strength, p.i = collation.Identical, p.i+1
		default:
			return nil
		}
		star := false
		if p.i < len(p.src) && p.src[p.i] == '*' {
			star = true
			p.i++
		}
		if star {
			if err := p.parseStarList(strength); err != nil {
				return err
			}
			continue
		}
		p.skipSpace()
		s, err := p.parseString()
		if err != nil {
			return err
		}
		prefix := ""
		p.skipSpace()
		if p.i < len(p.src) && p.src[p.i] == '|' {
			p.i++
			p.skipSpace()
			prefix = s
			if s, err = p.parseString(); err != nil {
				return err
			}
			p.skipSpace()
		}
		ext := ""
		if p.i < len(p.src) && p.src[p.i] == '/' {
			p.i++
			p.skipSpace()
			if ext, err = p.parseString(); err != nil {
				return err
			}
		}
		if s == "" {
			return p.errorf("empty relation string")
		}
		if err := p.sink.relation(strength, prefix, s, ext); err != nil {
			return p.errorf("relation %q: %v", s, err)
		}
	}
}

// parseStarList reads `<*abc` and `<*a-z`: one relation per code point.
func (p *ruleParser) parseStarList(strength int) error {
	p.skipSpace()
	rs, err := p.parseRunes(true)
	if err != nil {
		return err
	}
	for k := 0; k < len(rs); k++ {
		if rs[k] == rangeMark {
			if k == 0 || k+1 >= len(rs) {
				return p.errorf("bad range in a star list")
			}
			for r := rs[k-1] + 1; r <= rs[k+1]; r++ {
				if err := p.sink.relation(strength, "", string(r), ""); err != nil {
					return p.errorf("relation %q: %v", string(r), err)
				}
			}
			k++
			continue
		}
		if err := p.sink.relation(strength, "", string(rs[k]), ""); err != nil {
			return p.errorf("relation %q: %v", string(rs[k]), err)
		}
	}
	return nil
}

// rangeMark stands for an unquoted '-' in a star list.
const rangeMark = -1

// parseString reads a string up to the next syntax character or space.
func (p *ruleParser) parseString() (string, error) {
	rs, err := p.parseRunes(false)
	return string(rs), err
}

func (p *ruleParser) parseRunes(star bool) ([]rune, error) {
	var b []rune
	for p.i < len(p.src) {
		r, n := utf8.DecodeRuneInString(p.src[p.i:])
		switch {
		case r == '\'':
			p.i += n
			if p.i < len(p.src) && p.src[p.i] == '\'' {
				b = append(b, '\'')
				p.i++
				continue
			}
			for {
				if p.i >= len(p.src) {
					return nil, p.errorf("unterminated quote")
				}
				q, m := utf8.DecodeRuneInString(p.src[p.i:])
				p.i += m
				if q == '\'' {
					if p.i < len(p.src) && p.src[p.i] == '\'' {
						b = append(b, '\'')
						p.i++
						continue
					}
					break
				}
				if q == '\\' {
					p.i -= m
					e, err := p.parseEscape()
					if err != nil {
						return nil, err
					}
					b = append(b, e)
					continue
				}
				b = append(b, q)
			}
		case r == '\\':
			e, err := p.parseEscape()
			if err != nil {
				return nil, err
			}
			b = append(b, e)
		case star && r == '-':
			p.i += n
			b = append(b, rangeMark)
		case isSyntax(r) || isPatternSpace(r):
			return b, nil
		default:
			p.i += n
			b = append(b, r)
		}
	}
	return b, nil
}

// parseEscape reads a backslash escape at p.i.
func (p *ruleParser) parseEscape() (rune, error) {
	p.i++ // backslash
	if p.i >= len(p.src) {
		return 0, p.errorf("a backslash at the end")
	}
	hex := func(n int) (rune, error) {
		if p.i+n > len(p.src) {
			return 0, p.errorf("short escape")
		}
		v, err := strconv.ParseUint(p.src[p.i:p.i+n], 16, 32)
		if err != nil {
			return 0, p.errorf("bad escape %q", p.src[p.i:p.i+n])
		}
		p.i += n
		return rune(v), nil
	}
	c := p.src[p.i]
	switch c {
	case 'u':
		p.i++
		return hex(4)
	case 'U':
		p.i++
		return hex(8)
	case 'x':
		p.i++
		if p.i < len(p.src) && p.src[p.i] == '{' {
			end := strings.IndexByte(p.src[p.i:], '}')
			if end < 0 {
				return 0, p.errorf("unterminated \\x{")
			}
			v, err := strconv.ParseUint(p.src[p.i+1:p.i+end], 16, 32)
			if err != nil {
				return 0, p.errorf("bad escape")
			}
			p.i += end + 1
			return rune(v), nil
		}
		return hex(2)
	case 't':
		p.i++
		return '\t', nil
	case 'n':
		p.i++
		return '\n', nil
	case 'r':
		p.i++
		return '\r', nil
	}
	r, n := utf8.DecodeRuneInString(p.src[p.i:])
	p.i += n
	return r, nil
}

// parseSetting reads `[name args]`, where a UnicodeSet argument is itself
// in brackets.
func (p *ruleParser) parseSetting() error {
	start := p.i
	p.i++ // [
	depth := 1
	for p.i < len(p.src) && depth > 0 {
		switch p.src[p.i] {
		case '[':
			depth++
		case ']':
			depth--
		case '\\':
			p.i++
		}
		p.i++
	}
	if depth != 0 {
		p.i = start
		return p.errorf("unterminated setting")
	}
	body := strings.TrimSpace(p.src[start+1 : p.i-1])
	name, args, _ := strings.Cut(body, " ")
	args = strings.TrimSpace(args)
	if name == "import" {
		if err := p.sink.importRules(args); err != nil {
			return p.errorf("[import %s]: %v", args, err)
		}
		return nil
	}
	var set []rune
	if strings.HasPrefix(args, "[") {
		var err error
		if set, err = parseSet(args); err != nil {
			return p.errorf("[%s]: %v", name, err)
		}
	}
	if err := p.sink.setting(name, strings.Fields(args), set); err != nil {
		return p.errorf("[%s]: %v", body, err)
	}
	return nil
}

// parseSet reads the simple UnicodeSets CLDR's settings use: code points,
// ranges a-b and escapes, in brackets.
func parseSet(s string) ([]rune, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return nil, fmt.Errorf("bad set %q", s)
	}
	p := &ruleParser{src: s[1 : len(s)-1]}
	var rs []rune
	for p.i < len(p.src) {
		r, n := utf8.DecodeRuneInString(p.src[p.i:])
		switch {
		case unicode.IsSpace(r):
			p.i += n
			continue
		case r == '\\':
			e, err := p.parseEscape()
			if err != nil {
				return nil, err
			}
			rs = append(rs, e)
		case r == '-' && len(rs) > 0:
			p.i += n
			hi, m := utf8.DecodeRuneInString(p.src[p.i:])
			if hi == '\\' {
				e, err := p.parseEscape()
				if err != nil {
					return nil, err
				}
				hi = e
			} else {
				p.i += m
			}
			for c := rs[len(rs)-1] + 1; c <= hi; c++ {
				rs = append(rs, c)
			}
		default:
			p.i += n
			rs = append(rs, r)
		}
	}
	return rs, nil
}
