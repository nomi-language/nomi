package irbuild

import "testing"

// A type declared in a std module lowered with the program (std/url,
// std/money, std/http, std/sql, std/tls, std/toml) is held as std's Debug
// through the universal Debug the front end synthesizes for it, though the
// module imports no Debug of its own, and renders as `Debug.inspect` of it
// does. Display and Hashable, which those modules import, run beside it.
func TestIRProgramStdDebugErasure_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io
import std::url.Url
import std::money.Money
import std::http.{Method, Status}
import std::sql.Dialect
import std::tls
import std::toml.Toml

fn show(d: Debug) -> String
    Debug.inspect(d)
end

fn main()
    u = Url"https://e.com/a"
    d: Debug = u
    io.print(Debug.inspect(d))
    io.print(show(Money"12.50 EUR"))
    up = False
    m: Debug = if up then Method.Get else Status.ok
    io.print(Debug.inspect(m))
    xs: List<Debug> = [Method.Get, Dialect.Sqlite, tls.Version.Tls12, Toml"a = 1"]
    io.print(Debug.inspect(xs))
    s: Display = u
    io.print(Display.to_string(s))
    h: Hashable = u
    io.inspect(Hashable.hash(h) == Hashable.hash(u))
    dbg d
end
`
	irRunSource(t, src, `Url{scheme: "https", user: None, host: "e.com", path: "/a", raw_path: None, query: None, fragment: None, raw_fragment: None, opaque_part: None, omit_host: False}
Money{amount: 12.50d, currency: Currency"EUR"}
Status(200)
`+"[Get, Sqlite, Tls12, Toml(\"a = 1\")]"+`
https://e.com/a
True
dbg line 27: d = Url{scheme: "https", user: None, host: "e.com", path: "/a", raw_path: None, query: None, fragment: None, raw_fragment: None, opaque_part: None, omit_host: False}
`)
}
