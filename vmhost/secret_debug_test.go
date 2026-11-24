package vmhost_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A SessionKey's written Debug, `SessionKey(<hidden>)`, is how it renders
// on every route a program prints a value by Debug: `Debug.inspect`, `dbg`,
// and nested in a struct, a tuple, an Option, a List, a Map value, an
// enum payload and std's CookieStore. No Bytes literal, `<<...>>`, reaches
// the output.
func TestSecretDebug_ASessionKeyRendersNothingOfItself(t *testing.T) {
	got := runSourceOutput(t, `import
    std::io
    std::sessions.{CookieStore, SessionKey}
end

struct Wrap
    key: SessionKey
end

enum Holder
    One(SessionKey)
    Empty
end

fn main()
    key = SessionKey.derive("a test secret of 32 bytes or more")!
    io.print(Debug.inspect(key))
    dbg key
    io.print(Debug.inspect(Wrap{key: key}))
    io.print(Debug.inspect((key, 1)))
    io.print(Debug.inspect(Some(key)))
    io.print(Debug.inspect([key]))
    io.print(Debug.inspect({1 => key}))
    io.print(Debug.inspect(Holder.One(key)))
    io.print(Debug.inspect(CookieStore{key: key}))
end
`)
	want := `SessionKey(<hidden>)
dbg line 18: key = SessionKey(<hidden>)
Wrap{key: SessionKey(<hidden>)}
(SessionKey(<hidden>), 1)
Some(SessionKey(<hidden>))
[SessionKey(<hidden>)]
{1 => SessionKey(<hidden>)}
One(SessionKey(<hidden>))
CookieStore{key: SessionKey(<hidden>), previous_keys: [], name: "session", max_age: 336h, idle_timeout: None, secure: True, path: "/", domain: None}
`
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(got, "<<") {
		t.Fatalf("a key's bytes reach the output:\n%s", got)
	}
}

// A SessionKey held as a `Debug` value renders through its written Debug,
// alone, in a List, an Option and a tuple of Debug, in a struct field
// typed Debug and as a parameter typed Debug. SessionKey is an opaque type
// of a std module lowered with the program, so its Debug body is the one
// the checker's record of `Debug.inspect` at SessionKey names, not one
// found by its kind in the shared stdlib's index.
func TestSecretDebug_ASessionKeyHeldAsDebugRendersItsOwnDebug(t *testing.T) {
	got := runSourceOutput(t, `import
    std::io
    std::sessions.SessionKey
end

struct Holder
    d: Debug
end

fn show(d: Debug) -> String
    Debug.inspect(d)
end

fn main()
    key = SessionKey.derive("a test secret of 32 bytes or more")!
    d: Debug = key
    io.print(Debug.inspect(d))
    ys: List<Debug> = [key]
    io.print(Debug.inspect(ys))
    o: Option<Debug> = Some(key)
    io.print(Debug.inspect(o))
    pair: (Debug, Int) = (key, 1)
    io.print(Debug.inspect(pair))
    io.print(Debug.inspect(Holder{d: key}))
    io.print(show(key))
end
`)
	want := `SessionKey(<hidden>)
[SessionKey(<hidden>)]
Some(SessionKey(<hidden>))
(SessionKey(<hidden>), 1)
Holder{d: SessionKey(<hidden>)}
SessionKey(<hidden>)
`
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

// A list of Dates converted to `List<Display>` runs where an assertion
// records its values. A Date enters Display boxed under its own type's
// name, and the shared calendar module records Date's written Debug under
// that name too, so an assertion's `values:` row offers the box to the
// row's hook: the impl the box names receives the Date inside it, not the
// box.
func TestSecretDebug_AStdValueHeldAsDisplayRunsWhereAnAssertionRecordsIt(t *testing.T) {
	got := runSourceOutput(t, `import
    std::io
    std::assertions.AssertionFailure
    std::calendar.Date
end

fn main() -> Result<Unit, AssertionFailure>
    xs = [Date`+"`2026-01-05`"+`, Date`+"`2026-01-05`"+`]
    ds: List<Display> = xs
    assert (Iter.map(ds, Display.to_string) |> Iter.to_list()) == ["2026-01-05", "2026-01-05"]
    out = Date`+"`2026-01-05`"+`
    io.print(Debug.inspect(out))
    Ok(Unit)
end
`)
	if want := "2026-01-05\n"; got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

// A failed assertion's `values:` rows render a std value held as an
// interface value through its type's written Debug, in each covariant
// position the value converts through (a List, an Option, a Result, a
// program's generic enum) and as Display and Debug directly, and so for a
// program's `embeds` enum held as Debug: each is boxed under its static
// type's name, and its written Debug receives the value inside the box.
func TestSecretDebug_AFailedAssertionsRowsRenderStdValuesHeldAsInterfaceValues(t *testing.T) {
	t.Setenv("NOMI_COLOR", "never")
	const name = "held_test.nomi"
	p, err := vmhost.LoadSource(name, `import
    std::calendar.{Date, DateTime, NaiveDateTime, OffsetDateTime, Time}
    std::json.Json
end

struct Circ
    r: Int
end

enum Ring
    embeds Circ
    Dot
end

impl Debug for Ring
    fn inspect(s: Ring) -> String
        case s
            .Circ{r:} -> "ring ${r}"
            .Dot -> "dot"
        end
    end
end

fn ring(r: Int) -> Ring
    Circ{r:}
end

enum Held<A>
    One A
    No
end

fn count<A>(_: A) -> Int
    1
end

test "held"
    xs = [Date"2026-01-05", Date"2026-01-06"]
    ds: List<Display> = xs
    o: Option<Display> = Some(Time"12:30:00")
    r: Result<Display, String> = Ok(NaiveDateTime"2026-01-05T12:30:00")
    h: Held<Display> = Held.One(OffsetDateTime"2026-01-05T12:30:00Z")
    d: Display = DateTime"2026-01-05T12:30:00-05:00[America/New_York]"
    bs: List<Debug> = [Json.decode("[1]")!, ring(1)]
    b: Debug = Date"2026-01-07"
    assert count((ds, o, r, h, d, bs, b)) == 0
end
`)
	if err != nil {
		t.Fatalf("the front end rejects the tests: %v", err)
	}
	var buf bytes.Buffer
	rep := vmhost.NewTestReport(&buf)
	p.Test(&buf, rep, name, vmhost.TestOptions{}, func(n string) string { return vmhost.TestName(name, n) })
	rep.Summary()
	want := `FAIL held_test.nomi :: held
  line 46: assertion failed
    assert count((ds, o, r, h, d, bs, b)) == 0
    values:
      (ds, o, r, h, d, bs, b)
        = ([2026-01-05, 2026-01-06], Some(12:30:00), Ok(2026-01-05T12:30:00), One(2026-01-05T12:30:00+00:00), 2026-01-05T12:30:00-05:00[America/New_York], [[1], ring 1], 2026-01-07)
      count((ds, o, r, h, d, bs, b))
        = 1
test result: FAILED. 0 passed, 1 failed
`
	if got := buf.String(); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A SessionKey has no Display, so interpolating one is a compile error and
// no text of it reaches a String that way.
func TestSecretDebug_ASessionKeyHasNoDisplay(t *testing.T) {
	_, err := vmhost.LoadSource("main.nomi", `import
    std::io
    std::sessions.SessionKey
end

fn main()
    key = SessionKey.derive("a test secret of 32 bytes or more")!
    io.print("${key}")
end
`)
	if err == nil {
		t.Fatal("the front end accepts an interpolated SessionKey, which has no Display")
	}
	if want := "no impl of `Display` for `SessionKey`"; !strings.Contains(err.Error(), want) {
		t.Fatalf("the rejection does not say %q:\n%v", want, err)
	}
}

// A failed assertion's `values:` rows render a std type through the type's
// written Debug, at any depth, as they render a program's own type: a
// SessionKey reads `SessionKey(<hidden>)` alone, in a List and in std's
// CookieStore, a signatures PrivateKey reads its kind, not its key's
// bytes, and a Duration reads `5s`. These modules are lowered once for the
// process and linked into the program, so the rows depend on the shared
// modules recording their written Debug bodies.
func TestSecretDebug_AFailedAssertionsRowsUseStdsWrittenDebug(t *testing.T) {
	t.Setenv("NOMI_COLOR", "never")
	const name = "secret_test.nomi"
	p, err := vmhost.LoadSource(name, `import
    std::duration.Duration
    std::sessions.{CookieStore, SessionKey}
    std::signatures.PrivateKey
end

test "a key"
    key = SessionKey.derive("a test secret of 32 bytes or more")!
    other = SessionKey.derive("another secret of 32 bytes or more")!
    assert key == other
end

test "a key held deeper"
    key = SessionKey.derive("a test secret of 32 bytes or more")!
    store = CookieStore{key: key}
    assert [Some(key)] == [] or [store] == []
end

test "std's other written Debug"
    private = PrivateKey.from_ed25519_seed(String.to_bytes("0123456789abcdef0123456789abcdef"))!
    span = Duration.seconds(5)
    assert [private] == [] or span == Duration.seconds(6)
end
`)
	if err != nil {
		t.Fatalf("the front end rejects the tests: %v", err)
	}
	var buf bytes.Buffer
	rep := vmhost.NewTestReport(&buf)
	p.Test(&buf, rep, name, vmhost.TestOptions{}, func(n string) string { return vmhost.TestName(name, n) })
	rep.Summary()
	want := `FAIL secret_test.nomi :: a key
  line 10: assertion failed
    assert key == other
    values:
      key
        = SessionKey(<hidden>)
      other
        = SessionKey(<hidden>)
FAIL secret_test.nomi :: a key held deeper
  line 16: assertion failed
    assert [Some(key)] == [] or [store] == []
    values:
      [Some(key)]
        = [Some(SessionKey(<hidden>))]
      [Some(key)] == []
        = False
      [store]
        = [CookieStore{domain: None, idle_timeout: None, key: SessionKey(<hidden>), max_age: 336h, name: "session", path: "/", previous_keys: [], secure: True}]
      [store] == []
        = False
FAIL secret_test.nomi :: std's other written Debug
  line 22: assertion failed
    assert [private] == [] or span == Duration.seconds(6)
    values:
      [private]
        = [PrivateKey(Ed25519)]
      [private] == []
        = False
      span
        = 5s
      Duration.seconds(6)
        = 6s
      span == Duration.seconds(6)
        = False
test result: FAILED. 0 passed, 3 failed
`
	got := buf.String()
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(got, "<<") {
		t.Fatalf("a key's bytes reach the report:\n%s", got)
	}
}

// A test a `?` ends early reports the `Err` or `None` it returned under
// `returned:`, rendered as a `values:` row is: a std type and a program's
// own through their written Debug, alone and nested in a tuple, a List, an
// Option and a struct with no written Debug. So is the `Err` a group's boot
// returns, which ends each of its tests the same way. No key's bytes, a
// PrivateKey's DER or a Duration's nanoseconds reach the report.
func TestSecretDebug_ATestEndedByAQuestionMarkReportsThroughWrittenDebug(t *testing.T) {
	t.Setenv("NOMI_COLOR", "never")
	const name = "returned_test.nomi"
	p, err := vmhost.LoadSource(name, `import
    std::duration.Duration
    std::sessions.SessionKey
    std::signatures.PrivateKey
end

struct Secret
    code: Int
end

impl Debug for Secret
    fn inspect(Secret) -> String
        "Secret(<hidden>)"
    end
end

struct Holder
    key: SessionKey
    secret: Secret
end

struct Config
    verbose: Bool
end

fn boot() -> Result<Config, Holder>
    Err(Holder{key: key(), secret: Secret{code: 7}})
end

fn key() -> SessionKey
    SessionKey.derive("a test secret of 32 bytes or more")!
end

fn fail<E>(e: E) -> Result<Int, E>
    Err(e)
end

fn main() end

test "a key"
    _ = fail(key())?
    assert True
end

test "a private key"
    private = PrivateKey.from_ed25519_seed(String.to_bytes("0123456789abcdef0123456789abcdef"))!
    _ = fail(private)?
    assert True
end

test "a duration"
    _ = fail(Duration.seconds(5))?
    assert True
end

test "a program's own written Debug"
    _ = fail(Secret{code: 42})?
    assert True
end

test "nested"
    _ = fail((key(), [Secret{code: 1}], Some(key()), Holder{key: key(), secret: Secret{code: 2}}))?
    assert True
end

test "a None"
    held: Option<SessionKey> = None
    _ = held?
    assert True
end

group "a boot that fails"
    boot boot()

    test "never runs"
        assert Config.verbose
    end
end
`)
	if err != nil {
		t.Fatalf("the front end rejects the tests: %v", err)
	}
	var buf bytes.Buffer
	rep := vmhost.NewTestReport(&buf)
	p.Test(&buf, rep, name, vmhost.TestOptions{}, func(n string) string { return vmhost.TestName(name, n) })
	rep.Summary()
	want := `FAIL returned_test.nomi :: a key
  line 41: test returned early
    fail(key())?
    returned:
      Err(SessionKey(<hidden>))
FAIL returned_test.nomi :: a private key
  line 47: test returned early
    fail(private)?
    returned:
      Err(PrivateKey(Ed25519))
FAIL returned_test.nomi :: a duration
  line 52: test returned early
    fail(Duration.seconds(5))?
    returned:
      Err(5s)
FAIL returned_test.nomi :: a program's own written Debug
  line 57: test returned early
    fail(Secret{code: 42})?
    returned:
      Err(Secret(<hidden>))
FAIL returned_test.nomi :: nested
  line 62: test returned early
    fail((key(), [Secret{code: 1}], Some(key()), Holder{key: key(), secret: Secret{code: 2}}))?
    returned:
      Err((SessionKey(<hidden>), [Secret(<hidden>)], Some(SessionKey(<hidden>)), Holder{key: SessionKey(<hidden>), secret: Secret(<hidden>)}))
FAIL returned_test.nomi :: a None
  line 68: test returned early
    held?
    returned:
      None
FAIL returned_test.nomi :: a boot that fails / never runs
  line 73: test returned early
    boot boot()
    returned:
      Err(Holder{key: SessionKey(<hidden>), secret: Secret(<hidden>)})
test result: FAILED. 0 passed, 7 failed
`
	got := buf.String()
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	for _, leak := range []string{"<<", "der:", "5000000000"} {
		if strings.Contains(got, leak) {
			t.Fatalf("the report shows %q, which a written Debug hides:\n%s", leak, got)
		}
	}
}

// A std::secret Secret prints `Secret(<hidden>)` on every route a program
// prints a value by Debug, alone and in what std holds one in: mail's
// Credentials and SmtpConfig, and the pair `Request.basic_auth` answers.
// Only `Secret.expose` gives its text.
func TestSecretDebug_ASecretRendersNothingOfItsText(t *testing.T) {
	got := runSourceOutput(t, `import
    std::http.Request
    std::io
    std::mail.{Credentials, SmtpConfig}
    std::secret.Secret
    std::url.Url
end

fn main()
    password = Secret.new("hunter2")
    io.print(Debug.inspect(password))
    dbg password
    io.print(Debug.inspect((password, [password], Some(password))))
    io.print(Debug.inspect(Credentials{username: "ann", password:}))
    smtp = SmtpConfig{host: "smtp.example.com", credentials: Some(Credentials{username: "ann", password:})}
    io.print(Debug.inspect(smtp.credentials))
    signed = Request.with_basic_auth(Request{url: Url"https://example.com"}, "ann", password)
    io.print(Debug.inspect(Request.basic_auth(signed)))
    io.print(String.length(Secret.expose(password)))
end
`)
	want := `Secret(<hidden>)
dbg line 12: password = Secret(<hidden>)
(Secret(<hidden>), [Secret(<hidden>)], Some(Secret(<hidden>)))
Credentials{username: "ann", password: Secret(<hidden>)}
Some(Credentials{username: "ann", password: Secret(<hidden>)})
Some(("ann", Secret(<hidden>)))
7
`
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

// A Secret has no Display, so interpolating one is a compile error and no
// text of it reaches a String that way.
func TestSecretDebug_ASecretHasNoDisplay(t *testing.T) {
	_, err := vmhost.LoadSource("main.nomi", `import
    std::io
    std::secret.Secret
end

fn main()
    password = Secret.new("hunter2")
    io.print("${password}")
end
`)
	if err == nil {
		t.Fatal("the front end accepts an interpolated Secret, which has no Display")
	}
	if want := "no impl of `Display` for `Secret`"; !strings.Contains(err.Error(), want) {
		t.Fatalf("the rejection does not say %q:\n%v", want, err)
	}
}

// A Secret has no ToJson, so a derive cannot write a struct holding one
// out; the author writes how.
func TestSecretDebug_ASecretHasNoToJson(t *testing.T) {
	_, err := vmhost.LoadSource("main.nomi", `import
    std::json.ToJson
    std::secret.Secret
end

struct Smtp
    password: Secret
end

derive ToJson for Smtp

fn main() end
`)
	if err == nil {
		t.Fatal("the front end derives ToJson over a Secret field")
	}
	if want := "no impl of `ToJson` for `Secret`"; !strings.Contains(err.Error(), want) {
		t.Fatalf("the rejection does not say %q:\n%v", want, err)
	}
}

// A Secret is opaque: outside std::secret, the pattern that opens a
// distinct type does not reach its text.
func TestSecretDebug_ASecretsTextIsReachedOnlyThroughExpose(t *testing.T) {
	_, err := vmhost.LoadSource("main.nomi", `import
    std::io
    std::secret.Secret
end

fn main()
    Secret(text) = Secret.new("hunter2")
    io.print(text)
end
`)
	if err == nil {
		t.Fatal("the front end accepts a Secret opened by its pattern outside std::secret")
	}
	if want := "opaque"; !strings.Contains(err.Error(), want) {
		t.Fatalf("the rejection does not say %q:\n%v", want, err)
	}
}

// A failed assertion's `values:` rows and a test a `?` ended early show a
// Secret, alone and in std::mail's Credentials, as `Secret(<hidden>)`.
func TestSecretDebug_AFailedTestShowsNoSecretsText(t *testing.T) {
	t.Setenv("NOMI_COLOR", "never")
	const name = "secret_test.nomi"
	p, err := vmhost.LoadSource(name, `import
    std::mail.{Credentials, SmtpConfig}
    std::secret.Secret
end

fn credentials() -> Credentials
    Credentials{username: "ann", password: Secret.new("hunter2")}
end

fn fail<E>(e: E) -> Result<Int, E>
    Err(e)
end

test "values"
    smtp = SmtpConfig{host: "smtp.example.com", credentials: Some(credentials())}
    assert [smtp.credentials] == [] or Secret.new("hunter2") == Secret.new("hunter3")
end

test "returned"
    _ = fail((Secret.new("hunter2"), credentials()))?
    assert True
end
`)
	if err != nil {
		t.Fatalf("the front end rejects the tests: %v", err)
	}
	var buf bytes.Buffer
	rep := vmhost.NewTestReport(&buf)
	p.Test(&buf, rep, name, vmhost.TestOptions{}, func(n string) string { return vmhost.TestName(name, n) })
	rep.Summary()
	got := buf.String()
	for _, want := range []string{
		`[Some(Credentials{password: Secret(<hidden>), username: "ann"})]`,
		"returned:\n      Err((Secret(<hidden>), Credentials{password: Secret(<hidden>), username: \"ann\"}))",
		"test result: FAILED. 0 passed, 2 failed",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the report lacks %q:\n%s", want, got)
		}
	}
	// The source lines the report quotes name the text; no rendered value does.
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "= ") && strings.Contains(line, "hunter") {
			t.Fatalf("a rendered value shows a secret's text: %q\n%s", line, got)
		}
		if strings.HasPrefix(line, "      Err(") && strings.Contains(line, "hunter") {
			t.Fatalf("the returned value shows a secret's text: %q\n%s", line, got)
		}
	}
}

// A std::cipher Key and a std::key_exchange PrivateKey print their
// algorithm or curve and `<hidden>` on every route a program prints a value
// by Debug, alone and nested; a PublicKey prints its bytes in hexadecimal.
// No key's bytes, which here spell "kkkk...", reach the output.
func TestSecretDebug_ACipherKeyRendersNothingOfItsBytes(t *testing.T) {
	got := runSourceOutput(t, `import
    std::cipher.{Algorithm, Key}
    std::io
    std::key_exchange.{Curve, PrivateKey}
end

struct Vault
    key: Key
    private: PrivateKey
end

fn main()
    data = String.to_bytes(String.repeat("k", 32))
    key = Key.from_bytes(Algorithm.XChaCha20Poly1305, data)!
    private = PrivateKey.from_bytes(Curve.X25519, data)!
    io.print(Debug.inspect(key))
    dbg private
    io.print(Debug.inspect((Some(key), [private], {1 => key})))
    io.print(Debug.inspect(Vault{key:, private:}))
    io.print(Debug.inspect(PrivateKey.public_key(private)))
end
`)
	want := `Key(XChaCha20-Poly1305, <hidden>)
dbg line 17: private = PrivateKey(X25519, <hidden>)
(Some(Key(XChaCha20-Poly1305, <hidden>)), [PrivateKey(X25519, <hidden>)], {1 => Key(XChaCha20-Poly1305, <hidden>)})
Vault{key: Key(XChaCha20-Poly1305, <hidden>), private: PrivateKey(X25519, <hidden>)}
PublicKey(X25519, `
	if !strings.HasPrefix(got, want) || strings.Count(got, "\n") != 5 {
		t.Fatalf("output:\n%s\nwant it to start:\n%s", got, want)
	}
	if strings.Contains(got, "<<") || strings.Contains(got, "kkkk") || strings.Contains(got, "6b6b6b6b") {
		t.Fatalf("a key's bytes reach the output:\n%s", got)
	}
}

// A failed assertion's `values:` rows and a test a `?` ended early show a
// Key and a PrivateKey through their written Debug, alone and nested.
func TestSecretDebug_AFailedTestShowsNoCipherKeysBytes(t *testing.T) {
	t.Setenv("NOMI_COLOR", "never")
	const name = "keys_test.nomi"
	p, err := vmhost.LoadSource(name, `import
    std::cipher.{Algorithm, Key}
    std::key_exchange.{Curve, PrivateKey}
end

fn key() -> Key
    Key.from_bytes(Algorithm.Aes256Gcm, String.to_bytes(String.repeat("k", 32)))!
end

fn private() -> PrivateKey
    PrivateKey.from_bytes(Curve.X25519, String.to_bytes(String.repeat("k", 32)))!
end

fn fail<E>(e: E) -> Result<Int, E>
    Err(e)
end

test "values"
    assert [Some(key())] == [] or private() != private()
end

test "returned"
    _ = fail((key(), [private()]))?
    assert True
end
`)
	if err != nil {
		t.Fatalf("the front end rejects the tests: %v", err)
	}
	var buf bytes.Buffer
	rep := vmhost.NewTestReport(&buf)
	p.Test(&buf, rep, name, vmhost.TestOptions{}, func(n string) string { return vmhost.TestName(name, n) })
	rep.Summary()
	got := buf.String()
	for _, want := range []string{
		"= [Some(Key(AES-256-GCM, <hidden>))]",
		"= PrivateKey(X25519, <hidden>)",
		"returned:\n      Err((Key(AES-256-GCM, <hidden>), [PrivateKey(X25519, <hidden>)]))",
		"test result: FAILED. 0 passed, 2 failed",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the report lacks %q:\n%s", want, got)
		}
	}
	for _, line := range strings.Split(got, "\n") {
		trimmed := strings.TrimSpace(line)
		if (strings.HasPrefix(trimmed, "= ") || strings.HasPrefix(trimmed, "Err(")) &&
			(strings.Contains(line, "<<") || strings.Contains(line, "kkkk")) {
			t.Fatalf("a rendered value shows a key's bytes: %q\n%s", line, got)
		}
	}
}

// A Key and a PrivateKey have no Display, so interpolating one is a compile
// error, and no ToJson, so a derive cannot write a struct holding one out.
func TestSecretDebug_ACipherKeyHasNoDisplayAndNoToJson(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"Key in a string", `import
    std::cipher.{Algorithm, Key}
    std::io
end

fn main()
    io.print("${Key.generate(Algorithm.Aes256Gcm)}")
end
`, "no impl of `Display` for `Key`"},
		{"PrivateKey in a string", `import
    std::io
    std::key_exchange.{Curve, PrivateKey}
end

fn main()
    io.print("${PrivateKey.generate(Curve.X25519)}")
end
`, "no impl of `Display` for `key_exchange.PrivateKey`"},
		{"Key in a derived ToJson", `import
    std::cipher.Key
    std::json.ToJson
end

struct Config
    key: Key
end

derive ToJson for Config

fn main() end
`, "no impl of `ToJson` for `Key`"},
		{"PrivateKey in a derived ToJson", `import
    std::json.ToJson
    std::key_exchange.PrivateKey
end

struct Config
    key: PrivateKey
end

derive ToJson for Config

fn main() end
`, "no impl of `ToJson` for `PrivateKey`"},
	} {
		_, err := vmhost.LoadSource("main.nomi", c.src)
		if err == nil {
			t.Errorf("%s: the front end accepts it", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: the rejection does not say %q:\n%v", c.name, c.want, err)
		}
	}
}
