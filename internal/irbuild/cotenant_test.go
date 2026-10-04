package irbuild

import (
	"strings"
	"testing"
)

// TestCoTenant_APrivateIfaceFieldOfAPublicStructIsAFrontEndError asserts that
// a co-tenant's private interface cannot reach another file through a field
// of a public struct.
//
// `pub struct App { name: String  logger: Logger }` with `Logger` non-`pub` is
// a front-end error, "public struct field exposes private type Logger"
// (`checkPublicTypeExpr`), reported at the sibling's own position. The other
// routes are closed too:
//
//   - Make `App` private as well and wire.nomi's `core.{self, App}` import is
//     rejected ("'App' is private and cannot be imported"), as is any
//     file-qualified spelling of it ("file 'core' has no member 'App'").
//   - Every other cross-file position for the interface is checked by
//     `checkPublicTypeExpr`: a `pub fn` parameter or return, a `pub` alias, a
//     `pub once`, an enum payload, a generic argument inside any of them.
//
// The IR builder does not refuse a private interface itself: privacy is a rule
// about names, and the analyzer owns it.
func TestCoTenant_APrivateIfaceFieldOfAPublicStructIsAFrontEndError(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	entry := stagedProgram(t, "main.nomi", map[string]string{
		"main.nomi": "import {\n  std/io\n  core.{self}\n  wire.{self}\n}\n\n" +
			"fn main() {\n  io.print(wire.relay(core.loud()))\n}\n",
		"core.nomi": "import wire\n\n" +
			"interface Logger {\n  fn log(value: self, message: String): String\n}\n\n" +
			"pub struct App {\n  name: String\n  logger: Logger\n}\n\n" +
			"pub struct Prod {\n  prefix: String\n}\n\n" +
			"impl Logger for Prod {\n  fn log(p: Prod, message: String): String { _ = message;\n    p.prefix\n  }\n}\n\n" +
			"pub fn loud(): App {\n  App{name: \"loud\", logger: Prod{prefix: \"P\"}}\n}\n\n" +
			"pub fn origin(): String {\n  wire.tag\n}\n",
		"wire.nomi": "import core.{self, App}\n\n" +
			"pub once tag: String = \"wire\"\n\n" +
			"pub fn relay(a: App): String {\n  \"${a.name}/${tag}\"\n}\n",
	})
	_, err := Analyze(entry)
	if err == nil {
		t.Fatal("a public struct field of a private interface type is accepted, " +
			"so another file can reach a private interface through it")
	}
	// The position matters as much as the rejection: `core.nomi` is a sibling,
	// and a sibling's diagnostics must reach the caller rather than be dropped
	// on the way out of the front end.
	if !strings.Contains(err.Error(), "core.nomi:") {
		t.Errorf("rejected, but not at the sibling's own position: %v", err)
	}
	if !strings.Contains(err.Error(), "exposes private type Logger") {
		t.Errorf("rejected for some other reason, so this does not pin the visibility "+
			"rule: %v", err)
	}
}
