package rt

import (
	"fmt"
	"net/url"
	"strings"
)

// std/url's `Url` crosses to Go as its parts, a list of strings in the order
// below, so the functions here answer with Go's net/url and std/url builds
// and takes apart the struct (urlParts in std/url.nomi). A flag part is "1"
// or "".
const (
	urlScheme = iota
	urlOpaque
	urlHasUser
	urlUser
	urlHasPassword
	urlPassword
	urlHost
	urlPath
	urlRawPath
	urlForceQuery
	urlRawQuery
	urlFragment
	urlRawFragment
	urlOmitHost
	urlPartCount
)

func flag(b bool) string {
	if b {
		return "1"
	}
	return ""
}

func partsOf(u *url.URL) *List[string] {
	p := make([]string, urlPartCount)
	p[urlScheme] = u.Scheme
	p[urlOpaque] = u.Opaque
	if u.User != nil {
		p[urlHasUser] = "1"
		p[urlUser] = u.User.Username()
		pw, ok := u.User.Password()
		p[urlHasPassword] = flag(ok)
		p[urlPassword] = pw
	}
	p[urlHost] = u.Host
	p[urlPath] = u.Path
	p[urlRawPath] = u.RawPath
	p[urlForceQuery] = flag(u.ForceQuery)
	p[urlRawQuery] = u.RawQuery
	p[urlFragment] = u.Fragment
	p[urlRawFragment] = u.RawFragment
	p[urlOmitHost] = flag(u.OmitHost)
	return listOf(p)
}

func urlOf(parts *List[string]) *url.URL {
	p := make([]string, 0, urlPartCount)
	for l := parts; l != nil; l = l.Tail {
		p = append(p, l.Head)
	}
	if len(p) != urlPartCount {
		Trap(fmt.Sprintf("url: a Url crossed with %d parts, not %d", len(p), urlPartCount))
	}
	u := &url.URL{
		Scheme:      p[urlScheme],
		Opaque:      p[urlOpaque],
		Host:        p[urlHost],
		Path:        p[urlPath],
		RawPath:     p[urlRawPath],
		ForceQuery:  p[urlForceQuery] != "",
		RawQuery:    p[urlRawQuery],
		Fragment:    p[urlFragment],
		RawFragment: p[urlRawFragment],
		OmitHost:    p[urlOmitHost] != "",
	}
	switch {
	case p[urlHasPassword] != "":
		u.User = url.UserPassword(p[urlUser], p[urlPassword])
	case p[urlHasUser] != "":
		u.User = url.User(p[urlUser])
	}
	return u
}

// urlError is a *url.Error's reason, without the operation and URL Go's
// Error() puts before it: std/url's UrlError holds the URL beside it.
func urlError(err error) string {
	if e, ok := err.(*url.Error); ok {
		return e.Err.Error()
	}
	return err.Error()
}

// URLParse is std/url's `parse_parts`: Go's url.Parse, or
// url.ParseRequestURI when requestURI is true.
func URLParse(text string, requestURI bool) Result[*List[string], string] {
	parse := url.Parse
	if requestURI {
		parse = url.ParseRequestURI
	}
	u, err := parse(text)
	if err != nil {
		return Err[*List[string]](urlError(err))
	}
	return Ok[*List[string], string](partsOf(u))
}

// URLFormat is std/url's `Display for Url`: Go's URL.String.
func URLFormat(parts *List[string]) string { return urlOf(parts).String() }

// URLRedacted is `Url.redacted`: the URL with its password replaced by
// "xxxxx".
func URLRedacted(parts *List[string]) string { return urlOf(parts).Redacted() }

// URLEscapedPath is `Url.escaped_path`.
func URLEscapedPath(parts *List[string]) string { return urlOf(parts).EscapedPath() }

// URLEscapedFragment is `Url.escaped_fragment`.
func URLEscapedFragment(parts *List[string]) string { return urlOf(parts).EscapedFragment() }

// URLRequestURI is `Url.request_uri`: the path and query an HTTP request
// line carries, or the opaque part.
func URLRequestURI(parts *List[string]) string { return urlOf(parts).RequestURI() }

// URLHostname is `Url.hostname`: the host without its port or the brackets
// around an IPv6 address.
func URLHostname(parts *List[string]) string { return urlOf(parts).Hostname() }

// URLPort is `Url.port`: the host's port, or "" when it has none.
func URLPort(parts *List[string]) string { return urlOf(parts).Port() }

// URLResolve is `Url.resolve`: RFC 3986's resolution of reference against
// base.
func URLResolve(base, reference *List[string]) *List[string] {
	return partsOf(urlOf(base).ResolveReference(urlOf(reference)))
}

// URLJoinPath is `Url.join_path`: the segments joined to the path, and the
// result cleaned of `./` and `../`.
func URLJoinPath(parts, segments *List[string]) *List[string] {
	var elems []string
	for l := segments; l != nil; l = l.Tail {
		elems = append(elems, l.Head)
	}
	return partsOf(urlOf(parts).JoinPath(elems...))
}

// URLEscapeFragment is the escaping a slot after `#` gets in a `Url"..."`
// literal: Go's fragment encoding.
func URLEscapeFragment(text string) string {
	return (&url.URL{Fragment: text}).EscapedFragment()
}

// QueryPairs is `Query.parse`'s pairs: query's `key=value` pairs in order,
// each unescaped as url.ParseQuery does, and a pair Go refuses (a `;`, a
// bad escape) left out, as URL.Query leaves it out. The list alternates key
// and value.
func QueryPairs(query string) *List[string] {
	pairs, _ := parseQuery(query)
	return listOf(pairs)
}

// QueryError is the error url.ParseQuery answers for query: its first
// refused pair, or "" when every pair parses.
func QueryError(query string) string {
	_, err := parseQuery(query)
	if err != nil {
		return err.Error()
	}
	return ""
}

// parseQuery is net/url's parseQuery with the pairs kept in order, which
// url.Values, a map, loses.
func parseQuery(query string) (pairs []string, err error) {
	for query != "" {
		var key string
		key, query, _ = strings.Cut(query, "&")
		if strings.Contains(key, ";") {
			if err == nil {
				err = urlErrorf("invalid semicolon separator in query")
			}
			continue
		}
		if key == "" {
			continue
		}
		key, value, _ := strings.Cut(key, "=")
		key, err1 := url.QueryUnescape(key)
		if err1 != nil {
			if err == nil {
				err = err1
			}
			continue
		}
		value, err1 = url.QueryUnescape(value)
		if err1 != nil {
			if err == nil {
				err = err1
			}
			continue
		}
		pairs = append(pairs, key, value)
	}
	return pairs, err
}

type urlErrorf string

func (e urlErrorf) Error() string { return string(e) }

// QueryEncode is `Query.encode`: Go's Values.Encode over the pairs (keys
// sorted, each key's values in order). pairs alternates key and value.
func QueryEncode(pairs *List[string]) string {
	v := url.Values{}
	for l := pairs; l != nil && l.Tail != nil; l = l.Tail.Tail {
		v.Add(l.Head, l.Tail.Head)
	}
	return v.Encode()
}
