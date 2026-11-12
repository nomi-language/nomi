package lsp

import (
	"strings"
	"testing"
)

// A missing impl's notes point at the program's own code, never into
// std: a program's `Error` shares its bare name with std's, whose
// comparisons in std/json's bodies and doc prompts demand the same pair,
// and those sites are no note of the program's error. The program's Error
// holds a function, so it has no equality and misses Equatable.
func TestPublishedDiagnostics_MissingImplNotesStayInTheProgram(t *testing.T) {
	src := "import std::io\nimport std::json.Json\n\nstruct Error\n    run: (Int) -> Int\nend\n\nfn main()\n    e = Error{run: fn v -> v}\n    io.inspect(e in [e])\n    io.inspect(e in [e, e])\n    io.inspect(Json.String(\"a\"))\nend\n"
	d := findDiag(t, publishedFor(t, NewServer(), src, true), "no impl of `Equatable` for `main.Error` (required via membership test `in`)")
	if got := rangeText(d.Range); got != "9:20-9:21" {
		t.Errorf("range %s, want the first `in` at 9:20-9:21", got)
	}
	if len(d.RelatedInformation) != 1 {
		t.Fatalf("related = %+v, want only the second `in`", d.RelatedInformation)
	}
	r := d.RelatedInformation[0]
	if r.Location.URI != spanURI || rangeText(r.Location.Range) != "10:20-10:20" || r.Message != "also required here via membership test `in`" {
		t.Errorf("related = %+v, want the second `in` in main.nomi", r)
	}
	for _, r := range d.RelatedInformation {
		if strings.Contains(string(r.Location.URI), "/std/") {
			t.Errorf("a note points into std: %+v", r)
		}
	}
}

// A built-in type has no declaration the program could derive on or
// implement for, so its missing impl says only std can write it.
func TestPublishedDiagnostics_MissingImplOfABuiltinTypeSaysOnlyStdCanWriteIt(t *testing.T) {
	src := "import std::io\n\nfn main()\n    io.print(\"${Unit}\")\n    io.inspect(\"a\" % \"b\")\nend\n"
	diags := publishedFor(t, NewServer(), src, true)
	for _, c := range []struct{ headline, help string }{
		{"no impl of `Display` for `Unit` (required via call site)",
			"help: only std can implement `Display` for `Unit`; wrap it in a type of your own that implements `Display`"},
		{"no impl of `Remainder` for `String` (required via remainder operator `%`)",
			"help: only std can implement `Remainder` for `String`; wrap it in a type of your own that implements `Remainder`"},
	} {
		d := findDiag(t, diags, c.headline)
		if !strings.HasSuffix(d.Message, "\n"+c.help) {
			t.Errorf("%s: message %q, want the help %q", c.headline, d.Message, c.help)
		}
	}
}
