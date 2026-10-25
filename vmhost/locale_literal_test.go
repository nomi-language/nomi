package vmhost

import (
	"context"
	"strings"
	"testing"
)

// A backtick Locale literal is the canonical Locale its handler parses,
// checked when the program loads.
func TestLocaleLiteral_IsItsCanonicalLocale(t *testing.T) {
	const src = "import {\n" +
		"    std/io\n" +
		"    std/locale.Locale\n" +
		"}\n\n" +
		"once polish = Locale`pl-PL`\n\n" +
		"fn main() {\n" +
		"    io.print(Locale`EN-us`)\n" +
		"    io.print(Debug.inspect(Locale`iw`))\n" +
		"    io.print(polish == Locale`pl-pl`)\n" +
		"}\n"
	p, err := LoadSource("main", src)
	if err != nil {
		t.Fatalf("the front end rejects a valid Locale literal: %v", err)
	}
	var out strings.Builder
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	if want := "en-US\nLocale`he`\nTrue\n"; out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

// A malformed backtick Locale is a compile error at the literal, worded by
// locale.Error's Display.
func TestLocaleLiteral_InvalidTagIsACompileError(t *testing.T) {
	for _, c := range []struct{ literal, message string }{
		{"Locale`pt_BR`", "typed literal Locale`pt_BR` is invalid: a locale tag separates subtags with `-`, not `_`: write `pt-BR` (Locale.from_posix reads a POSIX value)"},
		{"Locale`en--US`", "typed literal Locale`en--US` is invalid: `en--US` is not a well-formed BCP 47 tag"},
		{"Locale`xx`", "typed literal Locale`xx` is invalid: `xx` is not a known language, script or region subtag"},
		{"Locale``", "typed literal Locale`` is invalid: a locale tag cannot be empty"},
	} {
		src := "import std/locale.Locale\n\nfn main() {\n    _ = " + c.literal + "\n}\n"
		ds := literalDiagnostics(t, src)
		if len(ds) != 1 {
			t.Fatalf("%s: %d diagnostics, want 1: %v", c.literal, len(ds), ds)
		}
		if ds[0].Message != c.message {
			t.Errorf("%s: message = %q, want %q", c.literal, ds[0].Message, c.message)
		}
		if ds[0].Code != InvalidLiteralCode {
			t.Errorf("%s: code = %q, want %q", c.literal, ds[0].Code, InvalidLiteralCode)
		}
	}
}
