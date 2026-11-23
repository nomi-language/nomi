package main

import (
	encjson "encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const effectProgram = `import
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

const effectAllowsIO = "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[effects.self]\nstd = [\"std::io\"]\n"

// The headline and help of the refusal of effectProgram's main.
const effectRefusal = "error: this module's own code has the effect std::fs, and [effects.self] allows it only std::io\n" +
	" --> main.nomi:8:14\n" +
	"  |\n" +
	"8 |     io.print(fs.read_text(\"notes.txt\"))\n" +
	"  |              ^^\n" +
	"  = help: reached from `main`\n" +
	"  = help: add \"std::fs\" to std under [effects.self] in nomi.toml, or remove the call\n"

// effectProject writes effectProgram under manifest and answers its
// directory and entry.
func effectProject(t *testing.T, manifest string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "nomi.toml"), manifest)
	entry := filepath.Join(dir, "main.nomi")
	mustWrite(t, entry, effectProgram)
	return dir, entry
}

// check, run, test and build each refuse a program that reaches what
// nomi.toml's lists do not allow, with the diagnostic at the call and exit
// status 1, and do nothing else: run prints nothing main would print,
// build writes no executable, test runs no case. check --format json
// carries the code and its args.
func TestEffects_EachCommandRefuses(t *testing.T) {
	dir, entry := effectProject(t, effectAllowsIO)
	cache := newCacheDir(t)

	run := runNomiIn(t, dir, cache, "run", "main.nomi")
	if run.exit != 1 || run.stdout != "" || run.stderr != effectRefusal {
		t.Errorf("nomi run: want exit 1, no output and the refusal\n%s", run)
	}
	check := runNomiIn(t, dir, cache, "check", "main.nomi")
	if check.exit != 1 || !strings.HasPrefix(check.stderr, effectRefusal) ||
		!strings.Contains(check.stderr, "error: this module's tests have the effect std::fs, and [effects.self] allows them only std::io\n") {
		t.Errorf("nomi check: want exit 1 and both refusals\n%s", check)
	}
	test := runNomiIn(t, dir, cache, "test", "main.nomi")
	if test.exit != 1 || strings.Contains(test.stdout, "ok ") ||
		!strings.Contains(test.stdout+test.stderr, "this module's tests have the effect std::fs, and [effects.self] allows them only std::io") ||
		!strings.Contains(test.stdout+test.stderr, `add "std::fs" to std_in_tests under [effects.self] in nomi.toml, or remove the call`) {
		t.Errorf("nomi test: want exit 1, no case run and the refusal\n%s", test)
	}
	out := exe(filepath.Join(t.TempDir(), "app"))
	build := runNomiIn(t, dir, cache, "build", "main.nomi", "-o", out)
	if build.exit != 1 || build.stderr != effectRefusal {
		t.Errorf("nomi build: want exit 1 and the refusal\n%s", build)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("nomi build wrote an executable it refused")
	}
	json := runNomiIn(t, dir, cache, "check", "--format", "json", "main.nomi")
	if json.exit != 1 || !strings.Contains(json.stdout, `"code":"effect-not-allowed","args":["std::fs","self","app","std::io"],"message":"this module's own code has the effect std::fs, and [effects.self] allows it only std::io"`) {
		t.Errorf("nomi check --format json: want the code and args\n%s", json)
	}
	if run.kept() || check.kept() || test.kept() {
		t.Errorf("a refused program was kept: %v %v %v", run.report, check.report, test.report)
	}

	// With the name allowed, and std_in_tests for the test, every
	// command passes.
	mustWrite(t, filepath.Join(dir, "nomi.toml"), effectAllowsIO+"std_in_tests = [\"std::fs\"]\n")
	mustWrite(t, entry, strings.Replace(effectProgram, `    io.print(fs.read_text("notes.txt"))`+"\n", "", 1))
	for _, args := range [][]string{{"run", "main.nomi"}, {"check", "main.nomi"}, {"test", "main.nomi"}} {
		if r := runNomiIn(t, dir, cache, args...); r.exit != 0 {
			t.Errorf("nomi %s with the names allowed:\n%s", strings.Join(args, " "), r)
		}
	}
}

// A program with no [effects] tables is held to nothing.
func TestEffects_NoTableEnforcesNothing(t *testing.T) {
	dir, _ := effectProject(t, "[module]\nname = \"app\"\nentry_points = [\"main\"]\n")
	mustWrite(t, filepath.Join(dir, "notes.txt"), "hello")
	run := runNomiIn(t, dir, "off", "run", "main.nomi")
	if run.exit != 0 || run.stdout != "started\nOk(hello)\n" {
		t.Errorf("nomi run with no table:\n%s", run)
	}
}

// The project's own path, by its [module].name when it has no go.mod, is
// no key under [effects.deps.modules]: check refuses it at its line,
// saying the project's own code is [effects.self]'s.
func TestEffects_TheProjectsPathIsNoEntry(t *testing.T) {
	dir, _ := effectProject(t, "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[effects.self]\nstd = [\"std::io\", \"std::fs\"]\n\n"+
		"[effects.deps.modules]\n\"app\" = [\"std::io\"]\n")
	check := runNomiIn(t, dir, "off", "check", "main.nomi")
	if check.exit != 1 || !strings.Contains(check.stderr, "[effects.deps.modules] names app, which is this project") ||
		!strings.Contains(check.stderr, "help: [effects.deps] holds the modules it depends on; its own code is held by [effects.self]") {
		t.Errorf("nomi check: want the project's path refused as an entry\n%s", check)
	}
}

// A dependency that reaches more than its own [effects.self]
// declares is refused by check, with code effect-not-declared and args
// naming the call, the list, the module and what its own list allows,
// though the project allows the call; with the project's
// [effects.deps] gone, nothing is held, the dependency's table
// included.
func TestEffects_ADependencyOverItsOwnList(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	mustWrite(t, filepath.Join(app, "nomi.toml"), effectAllowsIO+"\n[effects.deps]\nstd = [\"std::io\", \"std::fs\"]\n\n[effects.deps.modules]\n\"pricing\" = [\"std::fs\"]\n"+"\n[dependencies]\npricing = { dir = \"../pricing\" }\n")
	mustWrite(t, filepath.Join(app, "main.nomi"), "import\n    std::io\n    pricing::rules\nend\n\nfn main()\n    io.print(rules.load(\"prices.txt\"))\nend\n")
	mustWrite(t, filepath.Join(dir, "pricing", "nomi.toml"), "[module]\nname = \"pricing\"\n\n[effects.self]\nstd = [\"std::io\"]\n")
	mustWrite(t, filepath.Join(dir, "pricing", "rules.nomi"), "import std::fs\n\npub fn load(path: String) -> String\n    fs.read_text(path) |> Result.with_default(\"\")\nend\n")

	json := runNomiIn(t, app, "off", "check", "--format", "json", "main.nomi")
	if json.exit != 1 || !strings.Contains(json.stdout, `"code":"effect-not-declared","args":["std::fs","self","pricing","std::io"],"message":"pricing has the effect std::fs, and its own [effects.self] allows it only std::io"`) {
		t.Errorf("nomi check --format json: want the dependency's own list refused\n%s", json)
	}

	for _, manifest := range []string{"[module]\nname = \"app\"\nentry_points = [\"main\"]\n", effectAllowsIO} {
		mustWrite(t, filepath.Join(app, "nomi.toml"), manifest+"\n[dependencies]\npricing = { dir = \"../pricing\" }\n")
		if check := runNomiIn(t, app, "off", "check", "main.nomi"); check.exit != 0 {
			t.Errorf("nomi check with no project [effects.deps]:\n%s", check)
		}
	}
}

// A refused call in a dependency's file is one error at the call, and
// names the project's own call that leads there: as a note in check's
// text, with a count of the other places, and as each place under
// `related` in check --format json, with its file, span and message.
func TestEffects_ADependencysRefusalNamesTheProjectsCall(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	mustWrite(t, filepath.Join(app, "nomi.toml"), effectAllowsIO+"\n[effects.deps]\n\n[effects.deps.modules]\n\"pricing\" = []\n"+"\n[dependencies]\npricing = { dir = \"../pricing\" }\n")
	mustWrite(t, filepath.Join(app, "main.nomi"), "import\n    std::io\n    pricing::rules\nend\n\nfn main()\n    io.print(rules.load(\"a.txt\"))\n    io.print(rules.load(\"b.txt\"))\nend\n")
	mustWrite(t, filepath.Join(dir, "pricing", "nomi.toml"), "[module]\nname = \"pricing\"\n")
	mustWrite(t, filepath.Join(dir, "pricing", "rules.nomi"), "import std::fs\n\npub fn load(path: String) -> String\n    fs.read_text(path) |> Result.with_default(\"\")\nend\n")

	check := runNomiIn(t, app, "off", "check", "main.nomi")
	note := "  = note: your code has the effect std::fs in pricing here: `main` calls `load`; and 1 more place in your code; --all-calls lists it: main.nomi:7:14\n"
	if check.exit != 1 || strings.Count(check.stderr, "error:") != 1 || !strings.HasSuffix(check.stderr, note) {
		t.Errorf("nomi check: want exit 1 and one error ending with\n%s\ngot:\n%s", note, check)
	}
	json := runNomiIn(t, app, "off", "check", "--format", "json", "main.nomi")
	// main.nomi as check names it in its file record: through the
	// symlinks a macOS temporary directory has, and on Windows with the
	// short name (RUNNER~1) the directory was given by, which
	// EvalSymlinks would write out long.
	var main string
	for _, line := range strings.Split(json.stdout, "\n") {
		var rec struct{ Type, File string }
		if err := encjson.Unmarshal([]byte(line), &rec); err == nil && rec.Type == "file" {
			main = rec.File
		}
	}
	if filepath.Base(main) != "main.nomi" {
		t.Fatalf("nomi check --format json names no file record for main.nomi:\n%s", json)
	}
	for _, line := range []int{7, 8} {
		related := fmt.Sprintf(`{"file":%q,"line":%d,"col":14,"end_line":%d,"end_col":24,`, main, line, line)
		if json.exit != 1 || !strings.Contains(json.stdout, `"related":[`) || !strings.Contains(json.stdout, related) ||
			!strings.Contains(json.stdout, "\"message\":\"your code has the effect std::fs in pricing here: `main` calls `load`\"") {
			t.Errorf("nomi check --format json: want a related location starting %s\n%s", related, json)
		}
	}
	if n := strings.Count(json.stdout, `"type":"diagnostic"`); n != 1 {
		t.Errorf("nomi check --format json gives %d diagnostics; want one\n%s", n, json)
	}
}

// A Display body counts toward a list only where a value of its type may be
// rendered: a function that renders a Point in a tuple and a list reaches
// Point's Display alone, so Job's, which prints, does not make the project
// reach std::io. Rendering a Job in the same tuple does, and check refuses
// it at the print.
func TestEffects_ADisplayBodyCountsWhereItsTypeIsRendered(t *testing.T) {
	const decls = `import std::io

struct Job
    name: String
end

impl Display for Job
    fn to_string(j: Job) -> String
        io.print("shown")
        j.name
    end
end

struct Point
    x: Int
    y: Int
end

derive Display for Point

`
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "nomi.toml"), "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[effects.self]\nstd = []\n")
	mustWrite(t, filepath.Join(dir, "main.nomi"), decls+"fn pair(p: Point) -> String\n    \"${(p, [p])}\"\nend\n\nfn main()\n    _ = pair(Point{x: 1, y: 2})\nend\n")
	if check := runNomiIn(t, dir, "off", "check", "main.nomi"); check.exit != 0 {
		t.Errorf("nomi check with Job never rendered: want exit 0\n%s", check)
	}

	mustWrite(t, filepath.Join(dir, "main.nomi"), decls+"fn pair(p: Point) -> String\n    \"${(p, [Job{name: \"a\"}])}\"\nend\n\nfn main()\n    _ = pair(Point{x: 1, y: 2})\nend\n")
	check := runNomiIn(t, dir, "off", "check", "main.nomi")
	if check.exit != 1 || !strings.Contains(check.stderr, "this module's own code has the effect std::io, and [effects.self] allows it no effects\n --> main.nomi:9:9\n") {
		t.Errorf("nomi check with Job rendered: want exit 1 and the refusal at its print\n%s", check)
	}
}
