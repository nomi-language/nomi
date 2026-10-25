package vmhost

import (
	"context"
	"strings"
	"testing"
)

// A backtick Uuid literal is the UUID its handler parses, checked when the
// program loads.
func TestUuidLiteral_IsTheParsedUuid(t *testing.T) {
	const src = "import {\n" +
		"    std/io\n" +
		"    std/uuid.Uuid\n" +
		"}\n\n" +
		"once dns = Uuid`6BA7B810-9DAD-11D1-80B4-00C04FD430C8`\n\n" +
		"fn main() {\n" +
		"    io.print(dns)\n" +
		"    io.print(Debug.inspect(Uuid`00000000-0000-0000-0000-000000000000`))\n" +
		"    io.print(dns == Uuid`6ba7b810-9dad-11d1-80b4-00c04fd430c8`)\n" +
		"}\n"
	p, err := LoadSource("main", src)
	if err != nil {
		t.Fatalf("the front end rejects a valid Uuid literal: %v", err)
	}
	var out strings.Builder
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	if want := "6ba7b810-9dad-11d1-80b4-00c04fd430c8\nUuid`00000000-0000-0000-0000-000000000000`\nTrue\n"; out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

// A malformed backtick Uuid is a compile error at the literal, worded by
// uuid.Error's Display.
func TestUuidLiteral_InvalidTextIsACompileError(t *testing.T) {
	for _, c := range []struct{ literal, message string }{
		{"Uuid`6ba7b810`", "typed literal Uuid`6ba7b810` is invalid: a UUID is 36 characters or 16 bytes, not 8"},
		{"Uuid`6ba7b810+9dad-11d1-80b4-00c04fd430c8`", "typed literal Uuid`6ba7b810+9dad-11d1-80b4-00c04fd430c8` is invalid: expected `-` at index 8 of a UUID, found `+`"},
		{"Uuid`6ba7b81g-9dad-11d1-80b4-00c04fd430c8`", "typed literal Uuid`6ba7b81g-9dad-11d1-80b4-00c04fd430c8` is invalid: expected a hexadecimal digit at index 7 of a UUID, found `g`"},
	} {
		src := "import std/uuid.Uuid\n\nfn main() {\n    _ = " + c.literal + "\n}\n"
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
