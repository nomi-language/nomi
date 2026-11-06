package stdhttp

import (
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/nomi-language/nomi/rt"
)

// The functions here answer what Go's net/http answers about a request or
// its headers, over the parts of a Nomi Request rather than a Go request:
// a middleware may have changed a received request's headers, so the Go
// request the server received is not what a handler holds.

// HeadersWire is `http_host.headers_wire`: Go's Header.WriteSubset of the
// headers, name and value alternating, leaving out the names in except.
// The lines are sorted by name, each name's values in order, with a CR or
// LF in a value written as a space and the value trimmed, as Go writes
// them.
func HeadersWire(headers, except *rt.List[string]) string {
	skip := map[string]bool{}
	for l := except; l != nil; l = l.Tail {
		skip[http.CanonicalHeaderKey(l.Head)] = true
	}
	var b strings.Builder
	_ = header(headers).WriteSubset(&b, skip)
	return b.String()
}

// ParseHTTPVersion is `http_host.parse_http_version`: Go's
// ParseHTTPVersion, as [major, minor], or [] for text it does not read.
func ParseHTTPVersion(text string) *rt.List[int64] {
	major, minor, ok := http.ParseHTTPVersion(text)
	if !ok {
		return nil
	}
	return rt.Cons(int64(major), rt.Cons(int64(minor), (*rt.List[int64])(nil)))
}

// BasicAuth is `http_host.basic_auth`: Go's Request.BasicAuth of an
// Authorization header's value, as [user, password], or [] when it is not
// Basic credentials or they are not UTF-8.
func BasicAuth(authorization string) *rt.List[string] {
	req := &http.Request{Header: http.Header{"Authorization": {authorization}}}
	user, password, ok := req.BasicAuth()
	if !ok || !utf8.ValidString(user) || !utf8.ValidString(password) {
		return nil
	}
	return listOf([]string{user, password})
}

// AddCookie is `http_host.add_cookie`: the Cookie header Go's
// Request.AddCookie leaves after adding cookie (its parts, as
// cookie_text takes them) to a request whose Cookie header is existing:
// the name and value, sanitized and quoted as Go writes them, after
// existing and "; " when existing is not empty.
func AddCookie(existing string, cookie *rt.List[string]) string {
	req := &http.Request{Header: http.Header{}}
	if existing != "" {
		req.Header.Set("Cookie", existing)
	}
	req.AddCookie(cookieOf(strsOf(cookie)))
	return req.Header.Get("Cookie")
}

// CrossOriginCheck is `http_host.cross_origin_check`: Go's
// CrossOriginProtection.Check, trusting the origins in trusted and with no
// bypass pattern, of a request with method, Host host and headers, name
// and value alternating. It answers "" when the request passes, else Go's
// reason.
func CrossOriginCheck(method, host string, headers, trusted *rt.List[string]) string {
	c := http.NewCrossOriginProtection()
	for l := trusted; l != nil; l = l.Tail {
		// std/http's Origin is checked when it is made, and its rules are
		// stricter than AddTrustedOrigin's, so this does not fail.
		_ = c.AddTrustedOrigin(l.Head)
	}
	req := &http.Request{Method: method, Host: host, URL: &url.URL{Path: "/"}, Header: header(headers)}
	if err := c.Check(req); err != nil {
		return err.Error()
	}
	return ""
}
