package rt

import (
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
