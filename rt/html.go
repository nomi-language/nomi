package rt

import (
	"html"
	"net/url"
	"strings"
)

// The escaping std/html's `Html` templates write slot values with (spec §18
// *HTML templates*). Each slot's context is found when the program is
// checked (internal/htmltemplate); these are the run-time halves.

var htmlEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&#34;",
	"'", "&#39;",
	"\x00", "�",
)

// HTMLEscape is text escaped for element content or for a quoted attribute
// value, whichever quote the attribute uses: `&`, `<`, `>`, `"` and `'` as
// character references, and NUL, which HTML drops or replaces, as U+FFFD.
func HTMLEscape(text string) string { return htmlEscaper.Replace(text) }

// HTMLUnsafeURL replaces a URL whose scheme a page should not follow.
const HTMLUnsafeURL = "about:invalid#unsafe-url"

// HTMLFilterURL is the URL a slot at the start of a URL attribute writes: u
// itself when it is relative or its scheme is http, https, mailto or tel,
// and HTMLUnsafeURL otherwise, so data cannot make a `javascript:` link.
// The scheme is what precedes the first `:` that comes before any `/`, `?`
// or `#`; browsers ignore a tab or newline inside it and space before it,
// so such a scheme is compared as written and refused.
func HTMLFilterURL(u string) string {
	i := strings.IndexByte(u, ':')
	if i < 0 || strings.ContainsAny(u[:i], "/?#") {
		return u
	}
	switch strings.ToLower(u[:i]) {
	case "http", "https", "mailto", "tel":
		return u
	}
	return HTMLUnsafeURL
}

// HTMLEscapeURLPath is a slot's value in a URL's path: one path segment,
// percent-encoded as std/url's escape_path_segment encodes it, with `:` also
// encoded, so text joined to a relative URL's first segment cannot form a
// scheme (`java${s}` with s = `script:...`).
func HTMLEscapeURLPath(text string) string {
	return strings.ReplaceAll(url.PathEscape(text), ":", "%3A")
}

// HTMLUnescape is `html.unescape`: text with its character references
// decoded as HTML5's tokenizer decodes them in element content, by Go's
// html.UnescapeString and the cuts below. A named reference matches the longest
// name in the HTML5 table, and the legacy names that may go without a
// semicolon match without one (`&amp`, `&copy2` as `©2`). A numeric
// reference may also go without one; 0, a surrogate and a value past
// U+10FFFF decode as U+FFFD, and 0x80 to 0x9F as their Windows-1252
// characters. Anything that is not a reference stays as written.
//
// The texts unescapeCuts holds are decoded here: the text is cut around
// each, and the parts between are decoded on their own. A reference ends
// before the next `&`, so no part's decoding reads into a cut.
func HTMLUnescape(text string) string {
	var out strings.Builder
	start := 0
	for i := 0; i < len(text); i++ {
		k := strings.IndexByte(text[i:], '&')
		if k < 0 {
			break
		}
		i += k
		for cut, decoded := range unescapeCuts {
			if strings.HasPrefix(text[i:], cut) {
				out.WriteString(html.UnescapeString(text[start:i]))
				out.WriteString(decoded)
				start = i + len(cut)
				i = start - 1
				break
			}
		}
	}
	if start == 0 {
		return html.UnescapeString(text)
	}
	out.WriteString(html.UnescapeString(text[start:]))
	return out.String()
}

// unescapeCuts are the texts html.UnescapeString decodes otherwise than
// HTML5 does, with what HTML5 decodes them to: `&#x;`, a hexadecimal
// reference with no digits, which it reads as U+FFFD and HTML5 leaves as
// written, and the two names in the HTML5 table it leaves out, because
// their text is longer in UTF-8 than the reference and it decodes in
// place.
var unescapeCuts = map[string]string{
	"&#x;":  "&#x;",
	"&#X;":  "&#X;",
	"&nLt;": "\u226A\u20D2",
	"&nGt;": "\u226B\u20D2",
}

// HTMLEntity is `html.entity`: what the named character reference
// `&name;` stands for, or None when the HTML5 table has no such name.
//
// Go's html package keeps its table to itself, so this asks
// html.UnescapeString for "&name;". A name in the table decodes whole. One
// that is not decodes to itself, or, when a legacy name that may go
// without a semicolon is a prefix of it, to that name's character followed
// by the rest of the name and the semicolon (`&notit;` is `¬it;`). Both of
// those end in an ASCII letter or number and `;`, which no reference's text
// does (the one that ends in `;` is `&semi;`, which is only that), so the
// answer is the whole decoding exactly when it does not end so. The names
// Go's table leaves out are in unescapeCuts.
func HTMLEntity(name string) Maybe[string] {
	if name == "" || strings.IndexFunc(name, notASCIIAlnum) >= 0 {
		return None[string]()
	}
	if text, ok := unescapeCuts["&"+name+";"]; ok {
		return Some(text)
	}
	got := html.UnescapeString("&" + name + ";")
	if n := len(got); n >= 2 && got[n-1] == ';' && !notASCIIAlnum(rune(got[n-2])) {
		return None[string]()
	}
	return Some(got)
}

// notASCIIAlnum reports whether r is anything but an ASCII letter or
// number, the bytes an HTML5 reference's name is made of.
func notASCIIAlnum(r rune) bool {
	return !('a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9')
}
