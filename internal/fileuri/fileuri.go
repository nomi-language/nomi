// Package fileuri converts between file:// URIs, as the language server
// and its clients name documents, and the operating system's paths, as
// the front end names files.
//
// On every system but Windows a URI is `file://` and the path, unescaped,
// which is what the language server has always used as a document's key,
// so FromPath and ToPath are exact inverses there.
//
// On Windows a path is `C:\Users\me\a.nomi` and its URI
// `file:///C:/Users/me/a.nomi`; a client may spell the same document
// `file:///c%3A/Users/me/a.nomi` (VS Code does). The server keys documents
// by URI, so each URI a client sends is first made Canonical: the URI
// FromPath answers for its path, drive letter upper-cased and escaped as
// net/url escapes a path. ToPath also reads the unescaped
// `file://` + path spelling a careless caller builds.
package fileuri

import (
	"bytes"
	"encoding/json"
	"net/url"
	"runtime"
	"strings"
)

// Scheme is the prefix of every file URI.
const Scheme = "file://"

const windows = runtime.GOOS == "windows"

// FromPath is the file URI of the operating system path p.
func FromPath(p string) string { return fromPath(p, windows) }

// ToPath is the operating system path a file URI names. A string that is
// not a file URI is answered unchanged.
func ToPath(uri string) string { return toPath(uri, windows) }

// Canonical is the one spelling of a file URI the server keys a document
// by. It is the identity except on Windows.
func Canonical(uri string) string { return canonical(uri, windows) }

func canonical(uri string, win bool) string {
	if !win || !strings.HasPrefix(uri, Scheme) {
		return uri
	}
	return fromPath(toPath(uri, true), true)
}

func fromPath(p string, win bool) string {
	if !win {
		return Scheme + p
	}
	s := strings.ReplaceAll(p, `\`, "/")
	u := url.URL{Scheme: "file"}
	switch {
	case strings.HasPrefix(s, "//"):
		// A UNC path, \\host\share\rest.
		host, rest, _ := strings.Cut(s[2:], "/")
		u.Host, u.Path = host, "/"+rest
	case hasDrive(s):
		u.Path = "/" + strings.ToUpper(s[:1]) + s[1:]
	case strings.HasPrefix(s, "/"):
		u.Path = s
	default:
		return Scheme + s
	}
	return u.String()
}

func toPath(uri string, win bool) string {
	rest, ok := strings.CutPrefix(uri, Scheme)
	if !ok {
		return uri
	}
	if !win {
		return rest
	}
	host := ""
	if !strings.HasPrefix(rest, "/") && !hasDrive(rest) {
		host, rest, _ = strings.Cut(rest, "/")
		rest = "/" + rest
	}
	if p, err := url.PathUnescape(rest); err == nil {
		rest = p
	}
	rest = strings.ReplaceAll(rest, `\`, "/")
	if strings.HasPrefix(rest, "/") && hasDrive(rest[1:]) {
		rest = rest[1:]
	}
	if hasDrive(rest) {
		rest = strings.ToUpper(rest[:1]) + rest[1:]
	}
	if host != "" && host != "localhost" {
		rest = "//" + host + rest
	}
	return strings.ReplaceAll(rest, "/", `\`)
}

// hasDrive reports a path that starts with a drive letter and a colon.
func hasDrive(s string) bool {
	if len(s) < 2 || s[1] != ':' {
		return false
	}
	c := s[0]
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

// CanonicalJSON is the JSON message raw with every file URI in it made
// Canonical, for a request or notification the language
// server is about to handle. It is raw itself except on Windows, and
// whenever raw holds no file URI or does not decode.
func CanonicalJSON(raw []byte) []byte { return canonicalJSON(raw, windows) }

func canonicalJSON(raw []byte, win bool) []byte {
	if !win || !bytes.Contains(raw, []byte(Scheme)) {
		return raw
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return raw
	}
	changed := false
	// walk canonicalizes the strings under a key that names a URI (uri,
	// rootUri, targetUri, oldUri, newUri, scopeUri) and nothing else, so a
	// document's text is never touched.
	var walk func(v any, isURI bool) any
	walk = func(v any, isURI bool) any {
		switch v := v.(type) {
		case string:
			if c := canonical(v, true); isURI && c != v {
				changed = true
				return c
			}
		case []any:
			for i, e := range v {
				v[i] = walk(e, isURI)
			}
		case map[string]any:
			for k, e := range v {
				v[k] = walk(e, strings.HasSuffix(strings.ToLower(k), "uri"))
			}
		}
		return v
	}
	v = walk(v, false)
	if !changed {
		return raw
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return raw
	}
	return bytes.TrimRight(out.Bytes(), "\n")
}
