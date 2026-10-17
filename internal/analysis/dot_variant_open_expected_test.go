package analysis_test

import (
	"strings"
	"testing"
)

// A `.Variant` whose expected type is a type parameter of the called
// function that nothing solved determines no enum. `Maybe.to_result`'s E is
// fixed only by the later `try`, which variant resolution does not look at
// (spec, "Variant resolution"). The error says no enum is known here and
// names the qualified variant; it never names std's `E` as if it were the
// program's type.
func TestDotVariant_OpenExpectedTypeAsksForTheQualifiedVariant(t *testing.T) {
	const decls = "enum Error {\n  SyntaxError\n  UnknownOperation\n}\n\n"
	src := decls + "fn parse(first: String) -> Result<Int, Error> {\n" +
		"  total = String.to_int(first) |> Maybe.to_result(.SyntaxError) |> try\n" +
		"  Ok(total)\n}\n"
	_, errs := checkSourceWithStdlib(src)
	if len(errs) == 0 {
		t.Fatal("the checker admits `.SyntaxError` where nothing determines its enum")
	}
	want := ".SyntaxError cannot be resolved here: nothing at this position determines its enum\n" +
		"help: qualify the variant with its enum: `Error.SyntaxError`"
	found := false
	for _, e := range errs {
		text := diagText(e)
		if strings.Contains(text, "position is E") {
			t.Errorf("the error names the callee's type parameter: %q", text)
		}
		if text == want {
			found = true
		}
	}
	if !found {
		expectStdlibError(t, errs, want)
	}

	qualified := strings.Replace(src, "(.SyntaxError)", "(Error.SyntaxError)", 1)
	if _, errs := checkSourceWithStdlib(qualified); len(errs) != 0 {
		t.Fatalf("the qualified variant is rejected: %v", errs)
	}
}

// The enclosing function's own type parameter is a type the program wrote,
// so the message still names it.
func TestDotVariant_OwnTypeParameterIsNamed(t *testing.T) {
	src := "enum Color {\n  Red\n}\n\nfn pick<T>(x: T) -> T {\n  _ = x\n  .Red\n}\n"
	_, errs := checkSourceWithStdlib(src)
	expectStdlibError(t, errs, ".Red resolves only to enum variants, but the expected type at this position is T")
}
