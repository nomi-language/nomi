package vmhost

import (
	"context"
	"strings"
	"testing"
)

// An untagged string literal written where a type with a CheckedLiteral impl
// is expected is that type's literal: in an annotated binding, an argument, a
// struct field, a returned value and a list element, with and without slots.
func TestCheckedLiteral_UntaggedTakesItsPositionsType(t *testing.T) {
	const src = `import
    std::io
    std::url.Url
end

struct Site
    home: Url
end

fn show(u: Url) -> String
    "${u}"
end

fn make() -> Url
    "https://example.com/made"
end

fn main()
    a: Url = "https://example.com/a"
    name = "ann b"
    b: Url = "https://example.com/greet/${name}"
    io.print(a)
    io.print(b)
    io.print(show("https://example.com/arg"))
    io.print(Site{home: "https://example.com/field"}.home)
    io.print(make())
    us: List<Url> = ["https://example.com/1", "https://example.com/2"]
    io.print(us)
    plain = "https://example.com/plain"
    io.print(String.length(plain))
end
`
	var out strings.Builder
	p, err := LoadSource("main", src)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	want := "https://example.com/a\nhttps://example.com/greet/ann%20b\nhttps://example.com/arg\nhttps://example.com/field\n" +
		"https://example.com/made\n[https://example.com/1, https://example.com/2]\n25\n"
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

// Only a literal takes its type from the position: a String value where a
// Url is expected is the ordinary mismatch. A literal whose check refuses it
// is an error at the place the check names, labelled with the type the
// position gave it.
func TestCheckedLiteral_UntaggedErrors(t *testing.T) {
	for _, c := range []struct {
		name, body, message string
		line, col           int
	}{
		{"a String value", "    s = \"https://example.com/\"\n    show(s)\n", "argument 1: expected Url, got String", 18, 10},
		{"a slot in the host", "    v = \"evil.org\"\n    u: Url = \"https://${v}/greet\"\n    io.print(u)\n",
			"`Url` literal \"https://${v}/greet\" is invalid: a slot in the scheme, user or host cannot be escaped", 18, 23},
		{"a cron field out of range", "    c: Cron = \"0 25 * * *\"\n    io.print(c)\n",
			"`Cron` literal \"0 25 * * *\" is invalid: hour 25 is out of range 0-23", 17, 18},
	} {
		src := "import\n    std::io\n    std::jobs.Cron\n    std::url.Url\nend\n\nfn show(u: Url)\n    io.print(u)\nend\n\nfn tick() -> Cron\n    \"0 0 * * *\"\nend\n\nfn main()\n    io.print(tick())\n" + c.body + "end\n"
		ds := literalDiagnostics(t, src)
		if len(ds) != 1 {
			t.Fatalf("%s: %d diagnostics, want 1: %v", c.name, len(ds), ds)
		}
		if ds[0].Message != c.message {
			t.Errorf("%s: message = %q, want %q", c.name, ds[0].Message, c.message)
		}
		if ds[0].Line != c.line || ds[0].Col != c.col {
			t.Errorf("%s: at %d:%d, want %d:%d", c.name, ds[0].Line, ds[0].Col, c.line, c.col)
		}
	}
}
