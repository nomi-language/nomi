package analysis_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// bootSignatureMsg is the checker's rejection of an entry boot that takes
// anything but no parameter or one Startup.
const bootSignatureMsg = "boot must be `fn boot() -> Result<App, E>` or `fn boot(startup: Startup) -> Result<App, E>`, taking no parameter or one Startup and returning the application struct in a Result"

func TestScopedValuesRejectInvalidBootSignatures(t *testing.T) {
	for signature, want := range map[string]string{
		"(startup: Startup, other: Startup) -> MyApp":   bootSignatureMsg,
		"(port: Int) -> MyApp":                          bootSignatureMsg,
		"(context: Context) -> MyApp":                   bootSignatureMsg,
		"(startup: Startup, context: Context) -> MyApp": bootSignatureMsg,
		"<T>() -> MyApp":                                bootSignatureMsg,
		"(startup: Startup) -> #(Context, MyApp)":       "boot must return `Result<App, E>`",
	} {
		expectErrorT(t, buildProjectForDefaultConfig("\nstruct MyApp { port: Int }\nfn boot"+signature+" { #MyApp{port: 3} }\nfn main() {}"), want)
	}
}

// A boot that returns the application struct bare is one error, at its
// result annotation, whose help names the Result to declare and the `Ok` to
// wrap; the program's field reads still resolve against the struct, so
// nothing else is reported.
func TestBootReturningTheStructBareIsAnErrorAtItsAnnotation(t *testing.T) {
	errs := buildProjectForDefaultConfig("struct MyApp { port: Int }\nfn boot() -> MyApp { #MyApp{port: 3} }\nfn main() { _ = MyApp.port }")
	if len(errs) != 1 {
		t.Fatalf("want one error, got %d: %v", len(errs), errs)
	}
	e := errs[0]
	if e.Message != analysis.BootResultMsg || e.Line != 2 || e.Col != 14 {
		t.Fatalf("got %v at %d:%d, want %q at 2:14", e.Message, e.Line, e.Col, analysis.BootResultMsg)
	}
	if len(e.Hints) != 1 || !strings.Contains(e.Hints[0], "`-> Result<MyApp, String>`") || !strings.Contains(e.Hints[0], "`Ok(...)`") {
		t.Fatalf("help %q does not name the Result and the Ok", e.Hints)
	}
}

// A boot's application type is its Result's Ok type: field reads resolve
// against it, and `try` in its body propagates to the boot's own Result as
// in any function's.
func TestBootResultsOkTypeIsTheApplicationType(t *testing.T) {
	expectNoErrorsT(t, buildProjectForDefaultConfig(`
struct MyApp { port: Int }
fn port(text: String) -> Result<Int, String> { if text == "" { Err("no port") } else { Ok(3) } }
fn boot(startup: Startup) -> Result<MyApp, String> {
  p = try port(List.head(startup.args) |> Maybe.with_default(""))
  Ok(#MyApp{port: p})
}
fn main() { _ = MyApp.port }`))
	// The try's error must fit the boot's.
	expectErrorT(t, buildProjectForDefaultConfig(`
struct MyApp { port: Int }
fn port() -> Result<Int, Int> { Ok(3) }
fn boot() -> Result<MyApp, String> {
  p = try port()
  Ok(#MyApp{port: p})
}
fn main() { _ = MyApp.port }`), "try error type mismatch: expected String, got Int")
}

// An entry boot takes no parameter, one Startup, or one Startup with a
// default.
func TestScopedValuesAcceptBootSignatures(t *testing.T) {
	for _, boot := range []string{
		"fn boot() -> Result<MyApp, String> { Ok(#MyApp{port: 3}) }",
		"fn boot(startup: Startup) -> Result<MyApp, String> { Ok(#MyApp{port: Iter.count(startup.args)}) }",
		"fn boot(startup: Startup = #Startup{}) -> Result<MyApp, String> { Ok(#MyApp{port: Iter.count(startup.args)}) }",
		"fn boot(startup: Startup = #Startup{args: [\"-v\"]}) -> Result<MyApp, String> { Ok(#MyApp{port: Iter.count(startup.args)}) }",
	} {
		expectNoErrorsT(t, buildProjectForDefaultConfig("\nstruct MyApp { port: Int }\n"+boot+"\nfn main() {}"))
	}
}

// A group's `boot` line is an ordinary call against the boot's signature: no
// argument for a boot with no parameter or a defaulted one, one Startup for a
// boot that takes one.
func TestScopedValuesBootLineChecksTheBootsSignature(t *testing.T) {
	const group = "\ngroup \"g\" {\n  boot boot(%s)\n  test \"reads\" { assert MyApp.port >= 0 }\n}"
	for _, tc := range []struct{ boot, arg, want string }{
		{"fn boot() -> Result<MyApp, String> { Ok(#MyApp{port: 3}) }", "", ""},
		{"fn boot(startup: Startup = #Startup{}) -> Result<MyApp, String> { Ok(#MyApp{port: Iter.count(startup.args)}) }", "", ""},
		{"fn boot(startup: Startup = #Startup{}) -> Result<MyApp, String> { Ok(#MyApp{port: Iter.count(startup.args)}) }", "#Startup{args: [\"a\"]}", ""},
		{"fn boot(startup: Startup) -> Result<MyApp, String> { Ok(#MyApp{port: Iter.count(startup.args)}) }", "#Startup{}", ""},
		{"fn boot(startup: Startup) -> Result<MyApp, String> { Ok(#MyApp{port: Iter.count(startup.args)}) }", "", "expected 1 arguments, got 0"},
		{"fn boot() -> Result<MyApp, String> { Ok(#MyApp{port: 3}) }", "#Startup{}", "expected 0 arguments, got 1"},
		{"fn boot(startup: Startup) -> Result<MyApp, String> { Ok(#MyApp{port: Iter.count(startup.args)}) }", "3", "argument 1: expected Startup, got Int"},
	} {
		src := "\nstruct MyApp { port: Int }\n" + tc.boot + "\nfn main() {}" + fmt.Sprintf(group, tc.arg)
		errs := buildProjectForDefaultConfig(src)
		if tc.want == "" {
			expectNoErrorsT(t, errs)
		} else {
			expectErrorT(t, errs, tc.want)
		}
	}
}

// Startup's fields default to empty.
func TestScopedValuesStartupFieldsDefaultToEmpty(t *testing.T) {
	expectNoErrorsT(t, buildProjectForDefaultConfig(`
fn main() {
  empty = #Startup{}
  _ = Iter.count(empty.args) + Map.size(empty.env)
  verbose = #Startup{args: ["-v"]}
  _ = verbose.env
}`))
}
