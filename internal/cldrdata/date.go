package cldrdata

import (
	"fmt"
	"strings"
)

// DateField is one part of a CLDR date pattern (UTS #35 Part 4, "Date
// Format Patterns"): a pattern letter repeated Count times, or, when Letter
// is 0, literal Text (a quoted run, or anything that is not a letter).
type DateField struct {
	Letter rune
	Count  int
	Text   string
}

// dateLetters are the pattern letters UTS #35 defines.
const dateLetters = "GyYuUrQqMLlwWdDFgEecabBhHKkjJCmsSAzZOvVXx"

// ParseDatePattern reads a date pattern into its fields. A letter UTS #35
// does not define, or an unclosed quote, is an error.
func ParseDatePattern(p string) ([]DateField, error) {
	var out []DateField
	lit := func(s string) {
		if n := len(out); n > 0 && out[n-1].Letter == 0 {
			out[n-1].Text += s
			return
		}
		out = append(out, DateField{Text: s})
	}
	rs := []rune(p)
	for i := 0; i < len(rs); {
		r := rs[i]
		switch {
		case r == '\'':
			if i+1 < len(rs) && rs[i+1] == '\'' {
				lit("'")
				i += 2
				continue
			}
			end := i + 1
			var b strings.Builder
			for ; end < len(rs); end++ {
				if rs[end] == '\'' {
					if end+1 < len(rs) && rs[end+1] == '\'' {
						b.WriteRune('\'')
						end++
						continue
					}
					break
				}
				b.WriteRune(rs[end])
			}
			if end == len(rs) {
				return nil, fmt.Errorf("pattern %q: an unclosed quote", p)
			}
			lit(b.String())
			i = end + 1
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z':
			if !strings.ContainsRune(dateLetters, r) {
				return nil, fmt.Errorf("pattern %q: %q is not a date pattern letter", p, r)
			}
			n := 1
			for i+n < len(rs) && rs[i+n] == r {
				n++
			}
			out = append(out, DateField{Letter: r, Count: n})
			i += n
		default:
			lit(string(r))
			i++
		}
	}
	return out, nil
}

// QuoteDateText is s as a date pattern's literal text: quoted when it holds
// a letter or a quote.
func QuoteDateText(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '\'' }) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// FormatDatePattern writes fields back as a pattern.
func FormatDatePattern(fields []DateField) string {
	var b strings.Builder
	for _, f := range fields {
		if f.Letter == 0 {
			b.WriteString(QuoteDateText(f.Text))
			continue
		}
		b.WriteString(strings.Repeat(string(f.Letter), f.Count))
	}
	return b.String()
}
