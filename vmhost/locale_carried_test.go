package vmhost

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/vmrunner"
)

// loadManifest loads src under the nomi.toml text toml.
func loadManifest(t *testing.T, src, toml string) (*Program, error) {
	t.Helper()
	m, err := analysis.ParseManifestData([]byte(toml), "<virtual nomi.toml>")
	if err != nil {
		t.Fatal(err)
	}
	return LoadSource("main", src, WithVirtualManifest(m))
}

const carriedToml = "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[i18n]\nlocales = [\"en\", \"de\"]\n"

// localeLiteral is a program that formats in the locale tag names, written
// as a literal.
func localeLiteral(tag string) string {
	return "import {\n    std/io\n    std/locale.Locale\n}\n\nfn main() {\n" +
		"    io.print(String.join_list([\"a\", \"b\"], Locale`" + tag + "`))\n}\n"
}

// A Locale literal naming a locale whose chain reaches none of the listed
// ones is a compile error at the literal, naming the list.
func TestLocaleLiteral_NotCarriedIsACompileError(t *testing.T) {
	_, err := loadManifest(t, localeLiteral("fr"), carriedToml)
	if err == nil {
		t.Fatal("the program loads; want Locale`fr` to be a compile error, since it lists en and de")
	}
	var ds Diagnostics
	if !errors.As(err, &ds) || len(ds) != 1 {
		t.Fatalf("the load failed with %T %v; want one diagnostic", err, err)
	}
	d := ds[0]
	want := "typed literal Locale`fr`: `fr` is not one of this program's locales (`en`, `de` in nomi.toml's [i18n]): add it there, or format in one of them"
	if d.Message != want || d.Line != 7 || d.Code != InvalidLiteralCode {
		t.Errorf("diagnostic at %d:%d (%s): %q\nwant at line 7: %q", d.Line, d.Col, d.Code, d.Message, want)
	}
	if len(d.Hints) != 1 || !strings.Contains(d.Hints[0], "Locale.parse") {
		t.Errorf("hints %q, want one naming Locale.parse", d.Hints)
	}
	// The language server's lowering reports it as nomi check does.
	problems, err := CheckLowering(context.Background(), "main.nomi", localeLiteral("fr"),
		WithVirtualManifest(mustManifest(t, carriedToml)))
	if err != nil {
		t.Fatal(err)
	}
	if problems == nil || !strings.Contains(problems.Error(), "`fr` is not one of this program's locales") {
		t.Errorf("CheckLowering answers %v, want the literal's diagnostic", problems)
	}
}

// A locale whose chain reaches a listed one, and any locale in a program
// that lists none, is no error.
func TestLocaleLiteral_CarriedOrUnlistedIsNoError(t *testing.T) {
	for _, c := range []struct{ tag, toml, want string }{
		{"de", carriedToml, "a und b\n"},
		{"de-CH", carriedToml, "a und b\n"},
		{"en-GB", carriedToml, "a and b\n"},
		{"fr", "[module]\nname = \"app\"\nentry_points = [\"main\"]\n", "a et b\n"},
	} {
		p, err := loadManifest(t, localeLiteral(c.tag), c.toml)
		if err != nil {
			t.Errorf("Locale`%s`: %v", c.tag, err)
			continue
		}
		var out bytes.Buffer
		if err := p.Run(context.Background(), &out, nil, false); err != nil || out.String() != c.want {
			t.Errorf("Locale`%s` prints %q (%v), want %q", c.tag, out.String(), err, c.want)
		}
	}
}

func mustManifest(t *testing.T, toml string) *analysis.Manifest {
	t.Helper()
	m, err := analysis.ParseManifestData([]byte(toml), "<virtual nomi.toml>")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// A locale the program does not carry, read while it runs, formats as the
// source locale: dates, money and lists in English, in process and in a
// built image alike. Locale.negotiate answers as it did.
func TestNotCarriedLocale_FormatsAsTheSource(t *testing.T) {
	const src = `import {
    std/io
    std/locale.Locale
    std/calendar.Date
    std/currency.Currency
    std/money.Money
}

fn main() {
    fr = Result.with_default(Locale.parse("fr"), Locale` + "`en`" + `)
    io.print(Date.format(Date` + "`2026-10-20`" + `, fr, style: .Long))
    io.print(Money.format(#Money{amount: 12.5d, currency: Currency` + "`EUR`" + `}, fr))
    io.print(String.join_list(["a", "b", "c"], fr))
    io.print(Date.format(Date` + "`2026-10-20`" + `, Locale` + "`de-CH`" + `, style: .Long))
    io.print(Locale.negotiate([fr], Locale.available(), Locale` + "`de`" + `))
    io.print(Locale.negotiate([Locale` + "`de-AT`" + `], Locale.available(), Locale` + "`en`" + `))
    io.print(Locale.fallbacks(fr))
}
`
	for _, c := range []struct{ name, toml, want string }{
		{"source en", carriedToml,
			"October 20, 2026\n€12.50\na, b, and c\n20. Oktober 2026\nde\nde\n[fr]\n"},
		{"source de", carriedToml + "source_locale = \"de\"\n",
			"20. Oktober 2026\n12,50 €\na, b und c\n20. Oktober 2026\nde\nde\n[fr]\n"},
	} {
		p, err := loadManifest(t, src, c.toml)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var out bytes.Buffer
		if err := p.Run(context.Background(), &out, nil, false); err != nil || out.String() != c.want {
			t.Errorf("%s: in process:\n%s(%v)\nwant:\n%s", c.name, out.String(), err, c.want)
		}
		image, _, err := p.BuildImage()
		if err != nil {
			t.Fatal(err)
		}
		var built, stderr bytes.Buffer
		if code := vmrunner.Run(image, &built, &stderr, nil, nil, false); code != 0 || built.String() != c.want {
			t.Errorf("%s: the built image exits %d and prints:\n%s%s\nwant:\n%s", c.name, code, built.String(), stderr.String(), c.want)
		}
	}
}
