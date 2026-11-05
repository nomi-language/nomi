package vmhost_test

import (
	"io"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/std"
	"github.com/nomi-language/nomi/vmhost"
)

// A reference editor checks and lowers its test alone: the module's source
// is not parsed or checked again, for a shared module (calendar), a listed
// one (http) and a shared module's test that imports a listed one. The
// shared analysis of the module is as it was afterwards: the test's scope is
// taken out of the module scope again.
func TestStdlibReference_ChecksTheTestAlone(t *testing.T) {
	for _, c := range []struct{ module, body string }{
		{"calendar", "dt = DateTime`2026-10-20T09:30:00+02:00[Europe/Warsaw]`\nassert DateTime.to_string_with(dt, DateTimePattern`yyyyMMdd'T'HHmmss`) == \"20261020T093000\""},
		{"http", "assert Method.to_string(Method.Get) == \"GET\""},
		{"strings", "import std::uuid.Uuid\nassert Uuid.version(Uuid`0190A0B2-7F3C-7C3E-8A1B-1F2E3D4C5B6A`) == 7"},
	} {
		lib := std.Shared()
		if err := lib.Bodies(c.module); err != nil {
			t.Fatal(err)
		}
		scope := lib.Files[c.module].ModuleScope
		children, refs := len(scope.Children), len(lib.Files[c.module].References)
		parsed := frontend.FilesParsed()
		cases, err := vmhost.StdlibReference(c.module, c.body, "", io.Discard)
		if err != nil {
			t.Fatalf("%s: the front end rejects the editor, so nothing below is tested: %v", c.module, err)
		}
		if len(cases) != 1 || cases[0].Err != nil || cases[0].Blocked != nil {
			t.Fatalf("%s: want one passing case, got %+v", c.module, cases)
		}
		if n := frontend.FilesParsed() - parsed; n != 0 {
			t.Errorf("%s: the editor parsed %d files; the module's source should not be read again", c.module, n)
		}
		if len(scope.Children) != children || len(lib.Files[c.module].References) != refs {
			t.Errorf("%s: the shared analysis changed: %d scopes and %d references, was %d and %d",
				c.module, len(scope.Children), len(lib.Files[c.module].References), children, refs)
		}
	}
}

// An editor's error is the one the module's source checked afresh with the
// test appended reports, at the same place.
func TestStdlibReference_ReportsTheTestsErrorAsAFreshCheckDoes(t *testing.T) {
	for _, c := range []struct{ module, body string }{
		{"calendar", "assert Date.days_between(1, 2) == 3"},
		{"http", "assert missing == 1"},
	} {
		_, err := vmhost.StdlibReference(c.module, c.body, "", io.Discard)
		if err == nil {
			t.Fatalf("%s: %q is admitted", c.module, c.body)
		}
		src, _ := std.ReadFile(c.module)
		wrapped, _ := vmhost.ReferenceTestSource(string(src), c.body, "")
		_, fresh := vmhost.LoadStdlibSource(c.module, wrapped)
		if fresh == nil {
			t.Fatalf("%s: %q is admitted afresh", c.module, c.body)
		}
		if err.Error() != fresh.Error() {
			t.Errorf("%s: the editor reports\n%v\nand a fresh check\n%v", c.module, err, fresh)
		}
	}
}

// An edit that is more than one test, which closes the test and opens
// another, is checked with the whole module, as `nomi test` checks an edited
// stdlib file.
func TestStdlibReference_AnEditBeyondTheTestChecksTheModule(t *testing.T) {
	body := "assert Date.days_between(Date`2026-05-04`, Date`2026-05-07`) == 3\n}\n\ntest \"second\" {\nassert 1 == 1"
	parsed := frontend.FilesParsed()
	cases, err := vmhost.StdlibReference("calendar", body, "", io.Discard)
	if err != nil {
		t.Fatalf("the front end rejects the edit: %v", err)
	}
	if len(cases) != 1 || cases[0].Err != nil || cases[0].Blocked != nil || !strings.Contains(cases[0].Name, "reference") {
		t.Fatalf("want the reference case passing, got %+v", cases)
	}
	if frontend.FilesParsed() == parsed {
		t.Error("the edit was not checked with the module's source")
	}
}
