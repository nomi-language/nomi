package analysis_test

import (
	"fmt"
	"testing"
)

// bootSignatureMsg is the checker's rejection of an entry boot that takes
// anything but no parameter or one Startup.
const bootSignatureMsg = "boot must be `fn boot(): App` or `fn boot(startup: Startup): App`, taking no parameter or one Startup and returning the application struct"

func TestScopedValuesRejectInvalidBootSignatures(t *testing.T) {
	for signature, want := range map[string]string{
		"(startup: Startup, other: Startup): MyApp":   bootSignatureMsg,
		"(port: Int): MyApp":                          bootSignatureMsg,
		"(context: Context): MyApp":                   bootSignatureMsg,
		"(startup: Startup, context: Context): MyApp": bootSignatureMsg,
		"<T>(): MyApp":                                bootSignatureMsg,
		"(startup: Startup): (Context, MyApp)":        "boot must return a struct",
	} {
		expectErrorT(t, buildProjectForDefaultConfig("\nstruct MyApp { port: Int }\nfn boot"+signature+" { MyApp{port: 3} }\nfn main() {}"), want)
	}
}

// An entry boot takes no parameter, one Startup, or one Startup with a
// default.
func TestScopedValuesAcceptBootSignatures(t *testing.T) {
	for _, boot := range []string{
		"fn boot(): MyApp { MyApp{port: 3} }",
		"fn boot(startup: Startup): MyApp { MyApp{port: Iter.count(startup.args)} }",
		"fn boot(startup: Startup = Startup{}): MyApp { MyApp{port: Iter.count(startup.args)} }",
		"fn boot(startup: Startup = Startup{args: [\"-v\"]}): MyApp { MyApp{port: Iter.count(startup.args)} }",
	} {
		expectNoErrorsT(t, buildProjectForDefaultConfig("\nstruct MyApp { port: Int }\n"+boot+"\nfn main() {}"))
	}
}

// A group's `boot` line is an ordinary call against the boot's signature: no
// argument for a boot with no parameter or a defaulted one, one Startup for a
// boot that takes one.
func TestScopedValuesBootLineChecksTheBootsSignature(t *testing.T) {
	const group = "\ntests \"g\" {\n  boot boot(%s)\n  test \"reads\" { assert MyApp.port >= 0 }\n}"
	for _, tc := range []struct{ boot, arg, want string }{
		{"fn boot(): MyApp { MyApp{port: 3} }", "", ""},
		{"fn boot(startup: Startup = Startup{}): MyApp { MyApp{port: Iter.count(startup.args)} }", "", ""},
		{"fn boot(startup: Startup = Startup{}): MyApp { MyApp{port: Iter.count(startup.args)} }", "Startup{args: [\"a\"]}", ""},
		{"fn boot(startup: Startup): MyApp { MyApp{port: Iter.count(startup.args)} }", "Startup{}", ""},
		{"fn boot(startup: Startup): MyApp { MyApp{port: Iter.count(startup.args)} }", "", "expected 1 arguments, got 0"},
		{"fn boot(): MyApp { MyApp{port: 3} }", "Startup{}", "expected 0 arguments, got 1"},
		{"fn boot(startup: Startup): MyApp { MyApp{port: Iter.count(startup.args)} }", "3", "argument 1: expected Startup, got Int"},
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
  empty = Startup{}
  _ = Iter.count(empty.args) + Map.size(empty.env)
  verbose = Startup{args: ["-v"]}
  _ = verbose.env
}`))
}
