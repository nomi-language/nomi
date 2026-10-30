package stdstrings

import (
	"strings"
	"unicode"
)

// Words is s split into the words a case style joins: at `_`, `-` and
// whitespace, which are dropped (a run of them is one break); before an
// upper-case letter that follows a lower-case letter or a digit (`userId`
// is user, Id; `v2Api` is v2, Api); and before the last capital of a run
// of capitals that a lower-case letter follows, which ends an acronym
// (`HTTPServer` is HTTP, Server). A digit stays with the word before it
// (`parseHTTP2Request` is parse, HTTP2, Request). Letters are told apart
// by Unicode's upper and lower case, and a title-case letter (ǅ) counts as
// upper case. Anything else, punctuation included, is part of a word.
func Words(s string) []string {
	rs := []rune(s)
	var words []string
	start := -1
	for i, r := range rs {
		if r == '_' || r == '-' || unicode.IsSpace(r) {
			if start >= 0 {
				words = append(words, string(rs[start:i]))
				start = -1
			}
			continue
		}
		if start >= 0 && wordStarts(rs, i) {
			words = append(words, string(rs[start:i]))
			start = i
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		words = append(words, string(rs[start:]))
	}
	return words
}

// wordStarts reports whether rs[i], in a word that rs[i-1] is part of,
// starts a new one.
func wordStarts(rs []rune, i int) bool {
	if !upper(rs[i]) {
		return false
	}
	prev := rs[i-1]
	if unicode.IsLower(prev) || unicode.IsDigit(prev) {
		return true
	}
	return upper(prev) && i+1 < len(rs) && unicode.IsLower(rs[i+1])
}

func upper(r rune) bool { return unicode.IsUpper(r) || unicode.IsTitle(r) }

// SnakeCase is `String.snake_case`: s's words lower-cased, joined by `_`.
func SnakeCase(s string) string { return joinLower(s, "_") }

// KebabCase is `String.kebab_case`: s's words lower-cased, joined by `-`.
func KebabCase(s string) string { return joinLower(s, "-") }

// PascalCase is `String.pascal_case`: each of s's words capitalized (its
// first grapheme in title case, the rest lower-cased), with nothing
// between them.
func PascalCase(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, w := range Words(s) {
		b.WriteString(Capitalize(w, ""))
	}
	return b.String()
}

// CamelCase is `String.camel_case`: PascalCase with the first word
// lower-cased.
func CamelCase(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i, w := range Words(s) {
		if i == 0 {
			b.WriteString(LowerIn(w, ""))
		} else {
			b.WriteString(Capitalize(w, ""))
		}
	}
	return b.String()
}

func joinLower(s, sep string) string {
	words := Words(s)
	for i, w := range words {
		words[i] = LowerIn(w, "")
	}
	return strings.Join(words, sep)
}
