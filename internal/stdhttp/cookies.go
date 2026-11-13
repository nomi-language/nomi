package stdhttp

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nomi-language/nomi/rt"
)

// A cookie crosses as its fields in this order, as strings: name, value,
// quoted, path, domain, expires (nanoseconds, or "" for none), raw_expires,
// max_age, secure, http_only, same_site, partitioned, raw, then each
// unparsed attribute. max_age is "" for unset, "-1" for delete now, else
// seconds; same_site is "", "default", "lax", "strict" or "none".
const cookieFields = 13

func cookieParts(c *http.Cookie) []string {
	b := strconv.FormatBool
	expires := ""
	if !c.Expires.IsZero() {
		expires = strconv.FormatInt(c.Expires.UnixNano(), 10)
	}
	maxAge := ""
	switch {
	case c.MaxAge < 0:
		maxAge = "-1"
	case c.MaxAge > 0:
		maxAge = strconv.Itoa(c.MaxAge)
	}
	sameSite := map[http.SameSite]string{0: "", http.SameSiteDefaultMode: "default", http.SameSiteLaxMode: "lax",
		http.SameSiteStrictMode: "strict", http.SameSiteNoneMode: "none"}[c.SameSite]
	out := []string{c.Name, c.Value, b(c.Quoted), c.Path, c.Domain, expires, c.RawExpires, maxAge, b(c.Secure),
		b(c.HttpOnly), sameSite, b(c.Partitioned), c.Raw}
	return append(out, c.Unparsed...)
}

func cookieOf(parts []string) *http.Cookie {
	for len(parts) < cookieFields {
		parts = append(parts, "")
	}
	c := &http.Cookie{Name: parts[0], Value: parts[1], Quoted: isTrue(parts[2]), Path: parts[3], Domain: parts[4],
		RawExpires: parts[6], Secure: isTrue(parts[8]), HttpOnly: isTrue(parts[9]), Partitioned: isTrue(parts[11]),
		Raw: parts[12], Unparsed: parts[cookieFields:]}
	if ns, err := strconv.ParseInt(parts[5], 10, 64); err == nil {
		c.Expires = time.Unix(0, ns).UTC()
	}
	switch parts[7] {
	case "":
	case "-1":
		c.MaxAge = -1
	default:
		c.MaxAge, _ = strconv.Atoi(parts[7])
	}
	c.SameSite = map[string]http.SameSite{"default": http.SameSiteDefaultMode, "lax": http.SameSiteLaxMode,
		"strict": http.SameSiteStrictMode, "none": http.SameSiteNoneMode}[parts[10]]
	return c
}

// isTrue reads a Bool as either side writes it: Go's "true", Nomi's "True".
func isTrue(s string) bool { return strings.EqualFold(s, "true") }

func strsOf(l *rt.List[string]) []string {
	var out []string
	for ; l != nil; l = l.Tail {
		out = append(out, l.Head)
	}
	return out
}

func cookieList(cs []*http.Cookie) *rt.List[*rt.List[string]] {
	var out *rt.List[*rt.List[string]]
	for i := len(cs) - 1; i >= 0; i-- {
		out = rt.Cons(listOf(cookieParts(cs[i])), out)
	}
	return out
}

// CookieText is `std::http::host.cookie_text`: Go's Cookie.String, the
// Set-Cookie value, or "" for a cookie Go cannot write.
func CookieText(parts *rt.List[string]) string { return cookieOf(strsOf(parts)).String() }

// CookieValid is `std::http::host.cookie_valid`: Go's Cookie.Valid, "" when valid.
func CookieValid(parts *rt.List[string]) string {
	if err := cookieOf(strsOf(parts)).Valid(); err != nil {
		return err.Error()
	}
	return ""
}

// ParseCookie is `std::http::host.parse_cookie`: Go's ParseCookie of a Cookie
// header.
func ParseCookie(text string) rt.Result[*rt.List[*rt.List[string]], string] {
	cs, err := http.ParseCookie(text)
	if err != nil {
		return rt.Err[*rt.List[*rt.List[string]]](err.Error())
	}
	return rt.Ok[*rt.List[*rt.List[string]], string](cookieList(cs))
}

// ParseSetCookie is `std::http::host.parse_set_cookie`: Go's ParseSetCookie.
func ParseSetCookie(text string) rt.Result[*rt.List[string], string] {
	c, err := http.ParseSetCookie(text)
	if err != nil {
		return rt.Err[*rt.List[string]](err.Error())
	}
	return rt.Ok[*rt.List[string], string](listOf(cookieParts(c)))
}

// NewJar is `std::http::host.new_jar`: Go's cookiejar with no public suffix list.
func NewJar() rt.HTTPJar {
	j, _ := cookiejar.New(nil)
	return rt.NewHTTPJar(j)
}

func jarOf(j rt.HTTPJar) http.CookieJar {
	jar, ok := j.Value().(http.CookieJar)
	if !ok {
		rt.Trap("http: a CookieJar holds no jar")
	}
	return jar
}

// JarCookies is `std::http::host.jar_cookies`: the cookies the jar would send to
// target.
func JarCookies(j rt.HTTPJar, target string) *rt.List[*rt.List[string]] {
	u, err := url.Parse(target)
	if err != nil {
		return nil
	}
	return cookieList(jarOf(j).Cookies(u))
}

// JarSet is `std::http::host.jar_set`: store cookies received from target.
func JarSet(j rt.HTTPJar, target string, cookies *rt.List[*rt.List[string]]) rt.Unit {
	u, err := url.Parse(target)
	if err != nil {
		return rt.Unit{}
	}
	var cs []*http.Cookie
	for l := cookies; l != nil; l = l.Tail {
		cs = append(cs, cookieOf(strsOf(l.Head)))
	}
	jarOf(j).SetCookies(u, cs)
	return rt.Unit{}
}

// WithJar is `std::http::host.with_jar`: a client sharing c's transport and
// options that keeps cookies in j.
func WithJar(c rt.HTTPClient, j rt.HTTPJar) rt.HTTPClient {
	cl := *clientOf(c).c
	cl.Jar = jarOf(j)
	return rt.NewHTTPClient(&client{c: &cl, b: clientOf(c).b})
}
