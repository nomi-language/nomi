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

// moduleShows is a module whose User implements a's Show, beside two
// files that each declare an interface named Show.
var moduleShows = map[string]string{
	"a.nomi": `pub interface Show
    fn show(value: self) -> String
end
`,
	"b.nomi": `pub interface Show
    fn show(value: self) -> String
end
`,
	"user.nomi": `import a

pub struct User
    name: String
end

impl a.Show for User
    fn show(value: User) -> String
        "a ${value.name}"
    end
end
`,
}

// runTestFileCases writes files beside one another, runs the test file
// entry's cases on the VM, and answers the report and the exit status.
func runTestFileCases(t *testing.T, files map[string]string, entry string) (string, int) {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, entry)
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
	if module == nil || len(module.Tests()) == 0 {
		t.Fatalf("the test file's cases were not retained")
	}
	var out bytes.Buffer
	exit, reason := vm.NewProgram(module, res.IRModules(), &out).RunTests(path)
	if reason != "" {
		t.Fatalf("the VM could not run the cases: %s", reason)
	}
	return out.String(), exit
}

func withFiles(base map[string]string, more map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range more {
		out[k] = v
	}
	return out
}

// A test file that implements b's Show for its module's User, which
// implements a's, reaches each impl by the interface its call names.
func TestIRIfaceIdentity_ATestFileCallsTheInterfaceItNames(t *testing.T) {
	out, exit := runTestFileCases(t, withFiles(moduleShows, map[string]string{"user_test.nomi": `import a
import b
import user.User

impl b.Show for User
    fn show(value: User) -> String
        "b ${value.name}"
    end
end

test "each Show"
    u = User{name: "ann"}
    assert a.Show.show(u) == "a ann"
    assert b.Show.show(u) == "b ann"
end
`}), "user_test.nomi")
	if exit != 0 || strings.Contains(out, "FAIL") {
		t.Fatalf("the cases fail (exit %d):\n%s", exit, out)
	}
}

// A test file may implement its module's interface for its
// module's type.
func TestIRIfaceIdentity_ATestFileImplementsItsModulesInterface(t *testing.T) {
	out, exit := runTestFileCases(t, map[string]string{
		"shape.nomi": `pub interface Area
    fn area(value: self) -> Int
end

pub struct Square
    side: Int
end
`,
		"shape_test.nomi": `import shape.{Area, Square}

impl Area for Square
    fn area(value: Square) -> Int
        value.side * value.side
    end
end

test "the impl in the test file"
    assert Area.area(Square{side: 3}) == 9
end
`,
	}, "shape_test.nomi")
	if exit != 0 || strings.Contains(out, "FAIL") {
		t.Fatalf("the cases fail (exit %d):\n%s", exit, out)
	}
}

// A test file's call of b's Show on a User that implements only
// a's is the missing impl, not a's answer.
func TestIRIfaceIdentity_ATestFileHasNoImplOfAnotherShow(t *testing.T) {
	dir := t.TempDir()
	files := withFiles(moduleShows, map[string]string{"user_test.nomi": `import b
import user.User

test "b Show without an impl"
    u = User{name: "ann"}
    assert b.Show.show(u) == "a ann"
end
`})
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, err := Analyze(filepath.Join(dir, "user_test.nomi"))
	if err == nil {
		t.Fatal("the front end admits a call of b's Show on a User that implements only a's")
	}
	if !strings.Contains(err.Error(), "no impl of `b.Show` for `User`") {
		t.Fatalf("the front end rejects the call with another error: %v", err)
	}
}
