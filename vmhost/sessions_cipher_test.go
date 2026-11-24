package vmhost_test

import "testing"

// A session cookie sealed before std::sessions sealed with std::cipher
// opens under the same secret: the cookie's format (a 12-byte nonce, then
// AES-256-GCM's ciphertext and tag over `nomi session 1 <name>`), the key's
// derivation and the label are what they were. The cookie below was
// written by `sessions.write` under the secret and the 200-year `max_age`
// the program gives, by a nomi built from the commit before the change.
func TestSessionsCipher_ACookieSealedBeforeStdCipherStillOpens(t *testing.T) {
	got := runSourceOutput(t, `import
    std::duration.Duration
    std::http.{Body, Cookie, Method, Request, Response}
    std::http::testing
    std::io
    std::sessions.{self, CookieStore, Session, SessionKey}
end

fn main()
    key = SessionKey.derive("a fixed secret for a cookie sealed before std::cipher")!
    store = CookieStore{key:, max_age: Duration.hours(24 * 365 * 200)}
    show = fn _: Request, session: Session<String> -> Response{body: Body.Text(Option.with_default(session.value, "none"))}
    app = show |> sessions.with_session(store)
    value = "iM1Q4KM96NL30n6tWmg7XbYHCy8afLiyOQJcvWFKO5K_0-GirTya0ylZdZlhYjWdV59oLqvBunyqKpc-gtGoOc95ffUrJQKHfQ3BFA"
    cookie = Cookie.parse_set_cookie("session=${value}")!
    io.print(Response.text(app(Request.add_cookie(testing.request(Method.Get, "/"), cookie)))!)

    other = {..store, key: SessionKey.derive("another secret of 32 bytes or more")!}
    io.print(Response.text((show |> sessions.with_session(other))(Request.add_cookie(testing.request(Method.Get, "/"), cookie)))!)
end
`)
	if want := "ada\nnone\n"; got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}
