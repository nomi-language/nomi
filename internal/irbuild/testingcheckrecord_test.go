package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// `testing.check` is the call the checker recorded as std/testing's check,
// whatever it is spelled: a local named `testing` holding a struct with a
// `check` field is called through the field (613571fd8's program), the
// module's check gives an assertion-style failure, and `check` imported
// under another name over a pipe-bound name prints the pipe's stages. The
// builder matched the spelling `testing.check` or a bare `check`, so the
// aliased check's pipe binding recorded no stages and its test declined.
func TestTestingCheck_TheRecordedCalleeIsStdTestingsCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	src := `import std::testing.{self, check as verify}

struct Checker
    check: (Bool) -> Bool
end

fn yes(_b: Bool) -> Bool
    True
end

test "a local named testing calls its field"
    testing = Checker{check: yes}
    b: Bool = testing.check(False)
    assert b
end

test "a parameter named testing calls its field"
    use = fn testing: Checker -> testing.check(False)
    assert use(Checker{check: yes})
end

test "std's check fails as an assertion"
    assert testing.check(1 == 2)
end

test "an aliased check over a pipe-bound name"
    done = [1, 2] |> Iter.any_match(fn n -> n > 5)
    assert verify(done)
end
`
	path := filepath.Join(t.TempDir(), "check_test.nomi")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	declined := map[string]string{}
	p.Declines.Observed = func(fn, reason string) { declined[fn] = reason }
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	var module *ir.Module
	for _, m := range res.IR {
		if m.Name() == path {
			module = m
		}
	}
	if module == nil || len(module.Tests()) != 4 {
		t.Fatalf("want 4 retained cases (declines: %v)", declined)
	}
	var out bytes.Buffer
	exit, reason := vm.NewProgram(module, res.IRModules(), &out).RunTests(path)
	if reason != "" {
		t.Fatalf("the VM could not run the cases: %s", reason)
	}
	got := out.String()
	for _, want := range []string{
		"ok " + path + " :: a local named testing calls its field\n",
		"ok " + path + " :: a parameter named testing calls its field\n",
		"FAIL " + path + " :: std's check fails as an assertion\n",
		"FAIL " + path + " :: an aliased check over a pipe-bound name\n",
		`keyword: "check", line: 23, reason: "check failed"`,
		`name: "done", pipeline: [AssertionPipelineStage{expression: "[1, 2]", value: "[1, 2]"}`,
		"test result: FAILED. 2 passed, 2 failed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the report lacks %q:\n%s", want, got)
		}
	}
	if exit == 0 {
		t.Errorf("exit 0 with two failing cases:\n%s", got)
	}
}

// std's `check` is the one std/testing declares, reached through the
// module by any name or imported by name, and a project's own module named
// `testing` keeps its `check`: the checker decides by what the qualifier
// is bound to, not by its spelling. Each file holds a passing case and a
// failing one, so a file whose check stops being std's, or starts being
// it, changes its report.
func TestTestingCheck_AProjectModuleNamedTestingKeepsItsCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir := t.TempDir()
	files := map[string]string{
		"testing.nomi": `pub fn check(b: Bool) -> Bool
    not b
end
`,
		"own_test.nomi": `import testing

test "the project's check"
    b: Bool = testing.check(False)
    assert b
end

test "the project's check is false"
    assert testing.check(True)
end
`,
		"alias_test.nomi": `import std::testing as t

test "std's check under an alias"
    assert t.check(1 == 1) == Ok(True)
end

test "std's check under an alias fails as an assertion"
    assert t.check(1 == 2)
end
`,
		"by_name_test.nomi": `import std::testing.check

test "std's check by name"
    assert check(1 == 1) == Ok(True)
end

test "std's check by name fails as an assertion"
    assert check(1 == 2)
end
`,
		"both_test.nomi": `import
    std::testing
    testing as mine
end

test "both checks in one file"
    b: Bool = mine.check(False)
    assert testing.check(b) == Ok(True)
end

test "std's check beside the project's fails as an assertion"
    assert testing.check(mine.check(True))
end
`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"own_test.nomi", "alias_test.nomi", "by_name_test.nomi", "both_test.nomi"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name)
			p, err := Analyze(path)
			if err != nil {
				t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
			}
			res, _, err := GenerateIR(p)
			if err != nil {
				t.Fatal(err)
			}
			var module *ir.Module
			for _, m := range res.IR {
				if m.Name() == path {
					module = m
				}
			}
			if module == nil || len(module.Tests()) != 2 {
				t.Fatalf("want 2 retained cases in %s", name)
			}
			var out bytes.Buffer
			if _, reason := vm.NewProgram(module, res.IRModules(), &out).RunTests(path); reason != "" {
				t.Fatalf("the VM could not run the cases: %s", reason)
			}
			got := out.String()
			if !strings.Contains(got, "test result: FAILED. 1 passed, 1 failed") {
				t.Errorf("want one pass and one failure:\n%s", got)
			}
			// std's check fails with the assertion's own report, naming
			// the `check` keyword; the project's fails as a plain assert.
			if std := name != "own_test.nomi"; std != strings.Contains(got, `keyword: "check"`) {
				t.Errorf("std's check reports keyword check: %v, want %v:\n%s", !std, std, got)
			}
		})
	}
}
