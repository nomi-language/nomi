package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const capabilityProgram = `import
    std::fs
    std::io
end

fn main()
    io.print("started")
    io.print(fs.read_text("notes.txt"))
end

test "reads"
    _ = fs.read_text("x")
    assert 1 == 1
end
`

const capabilityAllowsIO = "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[capabilities]\nallow = [\"std::io\"]\n"

// The headline and help of the refusal of capabilityProgram's main.
const capabilityRefusal = "error: this program reaches std::fs, and nomi.toml allows only std::io\n" +
	" --> main.nomi:8:14\n" +
	"  |\n" +
	"8 |     io.print(fs.read_text(\"notes.txt\"))\n" +
	"  |              ^^\n" +
	"  = help: reached from `main`\n" +
	"  = help: add \"std::fs\" to allow under [capabilities] in nomi.toml, or remove the call\n"

// capabilityProject writes capabilityProgram under manifest and answers its
// directory and entry.
func capabilityProject(t *testing.T, manifest string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "nomi.toml"), manifest)
	entry := filepath.Join(dir, "main.nomi")
	mustWrite(t, entry, capabilityProgram)
	return dir, entry
}

// check, run, test and build each refuse a program that reaches what
// nomi.toml's lists do not allow, with the diagnostic at the call and exit
// status 1, and do nothing else: run prints nothing main would print,
// build writes no executable, test runs no case. check --format json
// carries the code and its args.
func TestCapabilities_EachCommandRefuses(t *testing.T) {
	dir, entry := capabilityProject(t, capabilityAllowsIO)
	cache := newCacheDir(t)

	run := runNomiIn(t, dir, cache, "run", "main.nomi")
	if run.exit != 1 || run.stdout != "" || run.stderr != capabilityRefusal {
		t.Errorf("nomi run: want exit 1, no output and the refusal\n%s", run)
	}
	check := runNomiIn(t, dir, cache, "check", "main.nomi")
	if check.exit != 1 || !strings.HasPrefix(check.stderr, capabilityRefusal) ||
		!strings.Contains(check.stderr, "error: this program's tests reach std::fs, and nomi.toml allows them only std::io\n") {
		t.Errorf("nomi check: want exit 1 and both refusals\n%s", check)
	}
	test := runNomiIn(t, dir, cache, "test", "main.nomi")
	if test.exit != 1 || strings.Contains(test.stdout, "ok ") ||
		!strings.Contains(test.stdout+test.stderr, "this program's tests reach std::fs, and nomi.toml allows them only std::io") ||
		!strings.Contains(test.stdout+test.stderr, `add "std::fs" to allow_in_tests in nomi.toml, or remove the call`) {
		t.Errorf("nomi test: want exit 1, no case run and the refusal\n%s", test)
	}
	out := exe(filepath.Join(t.TempDir(), "app"))
	build := runNomiIn(t, dir, cache, "build", "main.nomi", "-o", out)
	if build.exit != 1 || build.stderr != capabilityRefusal {
		t.Errorf("nomi build: want exit 1 and the refusal\n%s", build)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("nomi build wrote an executable it refused")
	}
	json := runNomiIn(t, dir, cache, "check", "--format", "json", "main.nomi")
	if json.exit != 1 || !strings.Contains(json.stdout, `"code":"capability-not-allowed","args":["std::fs","","std::io"],"message":"this program reaches std::fs, and nomi.toml allows only std::io"`) {
		t.Errorf("nomi check --format json: want the code and args\n%s", json)
	}
	if run.kept() || check.kept() || test.kept() {
		t.Errorf("a refused program was kept: %v %v %v", run.report, check.report, test.report)
	}

	// With the name allowed, and allow_in_tests for the test, every
	// command passes.
	mustWrite(t, filepath.Join(dir, "nomi.toml"), capabilityAllowsIO+"allow_in_tests = [\"std::fs\"]\n")
	mustWrite(t, entry, strings.Replace(capabilityProgram, `    io.print(fs.read_text("notes.txt"))`+"\n", "", 1))
	for _, args := range [][]string{{"run", "main.nomi"}, {"check", "main.nomi"}, {"test", "main.nomi"}} {
		if r := runNomiIn(t, dir, cache, args...); r.exit != 0 {
			t.Errorf("nomi %s with the names allowed:\n%s", strings.Join(args, " "), r)
		}
	}
}

// A program with no [capabilities] table is held to nothing.
func TestCapabilities_NoTableEnforcesNothing(t *testing.T) {
	dir, _ := capabilityProject(t, "[module]\nname = \"app\"\nentry_points = [\"main\"]\n")
	mustWrite(t, filepath.Join(dir, "notes.txt"), "hello")
	run := runNomiIn(t, dir, "off", "run", "main.nomi")
	if run.exit != 0 || run.stdout != "started\nOk(hello)\n" {
		t.Errorf("nomi run with no table:\n%s", run)
	}
}
