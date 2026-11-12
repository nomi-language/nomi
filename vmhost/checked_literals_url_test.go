package vmhost

import (
	"errors"
	"strings"
	"testing"
)

// A Url literal is checked when the program is checked: a slot in the
// scheme, user or host is a compile error at its `${`, and a static that is
// not a URL is one at the literal.
func TestCheckedLiteral_UrlRefusesUnescapableSlots(t *testing.T) {
	for _, c := range []struct {
		line, message string
		col           int
	}{
		{`    _ = Url"https://${v}/greet"`, "typed literal Url\"https://${v}/greet\" is invalid: a slot in the scheme, user or host cannot be escaped", 21},
		{`    _ = Url"${v}://example.com/"`, "typed literal Url\"${v}://example.com/\" is invalid: a slot in the scheme, user or host cannot be escaped", 13},
		{`    _ = Url"https://example.com${v}/a"`, "typed literal Url\"https://example.com${v}/a\" is invalid: a slot in the scheme, user or host cannot be escaped", 32},
	} {
		src := "import\n    std::io\n    std::url.Url\nend\n\nfn main()\n    v = \"evil.org\"\n    io.print(v)\n" + c.line + "\nend\n"
		ds := literalDiagnostics(t, src)
		if len(ds) != 1 {
			t.Fatalf("%s: %d diagnostics, want 1: %v", c.line, len(ds), ds)
		}
		if ds[0].Message != c.message {
			t.Errorf("%s: message = %q, want %q", c.line, ds[0].Message, c.message)
		}
		if ds[0].Line != 9 || ds[0].Col != c.col {
			t.Errorf("%s: at %d:%d, want 9:%d", c.line, ds[0].Line, ds[0].Col, c.col)
		}
	}
	// A slot takes any Display value, and the place refuses it whatever its
	// type: an Int, and a type of the program's own.
	for _, c := range []struct{ name, decl, value string }{
		{"an Int", "", "8080"},
		{"a user type", "struct Host\n    name: String\nend\n\nimpl Display for Host\n    fn to_string(h: Host) -> String\n        h.name\n    end\nend\n\n", `Host{name: "evil.org"}`},
	} {
		src := "import\n    std::io\n    std::url.Url\nend\n\n" + c.decl + "fn main()\n    v = " + c.value + "\n    io.print(\"${v}\")\n    _ = Url\"https://${v}/greet\"\nend\n"
		ds := literalDiagnostics(t, src)
		want := "typed literal Url\"https://${v}/greet\" is invalid: a slot in the scheme, user or host cannot be escaped"
		if len(ds) != 1 || ds[0].Message != want {
			t.Errorf("%s in the host: %v, want one error %q", c.name, ds, want)
		}
	}
	src := "import std::url.Url\n\nfn main()\n    _ = Url`https://exa mple.com/`\nend\n"
	ds := literalDiagnostics(t, src)
	if len(ds) != 1 || !strings.HasPrefix(ds[0].Message, "typed literal Url`https://exa mple.com/` is invalid: ") || ds[0].Col != 9 {
		t.Errorf("a static host with a space: %v, want one error at 4:9 naming the literal", ds)
	}
}

// Code written for a Url literal that was a Result is refused with a hint
// that says the literal is a Url: a `!`, a `?` and a case on Ok and Err.
func TestCheckedLiteral_ResultHabitsGetAHint(t *testing.T) {
	for _, c := range []struct{ body, message, hint string }{
		{"fn f(v: String) -> Url\n    Url\"https://example.com/${v}\"!\nend\n",
			"`!` unwraps a Result or a Maybe, and `Url\"https://example.com/${v}\"` is Url",
			"remove the `!`: the value is not wrapped"},
		{"fn f(v: String) -> Result<Url, String>\n    Ok(Url\"https://example.com/${v}\"?)\nend\n",
			"`?` requires a Result or Maybe, got Url",
			"a `Url` literal is checked when the program is checked, so this one is already a Url: remove the `?`"},
		{"fn f(v: String) -> Bool\n    case Url\"https://example.com/${v}\"\n        Ok(_) -> True\n        Err(_) -> False\n    end\nend\n",
			"enum pattern requires an enum type, got Url",
			"a `Url` literal is a Url, not a Result: it is checked when the program is checked"},
	} {
		src := "import std::url.Url\n\n" + c.body + "\nfn main()\n    _ = f(\"x\")\nend\n"
		_, err := LoadSource("main", src)
		if err == nil {
			t.Fatalf("the front end admits:\n%s", c.body)
		}
		var ds Diagnostics
		if !errors.As(err, &ds) {
			t.Fatalf("the load failed with %T %v; want diagnostics", err, err)
		}
		found := false
		for _, d := range ds {
			if d.Message == c.message && len(d.Hints) > 0 && d.Hints[0] == c.hint {
				found = true
			}
		}
		if !found {
			t.Errorf("no %q with the hint %q in %v", c.message, c.hint, ds)
		}
	}
}
