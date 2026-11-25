package main

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/dap"
)

// TestHelp_TheSummaryNamesEachCommandsFlags: every flag and subcommand word
// in a command's usage line is in the lines `nomi help` gives that command
// (the lines that start `nomi <name>` and the description under each). The
// summary is written apart from each usage, so this is what keeps the two
// naming the same words. --offline and --all-calls
// are named once for every command that takes them, from the command table,
// and the summary's line for each names exactly those commands.
func TestHelp_TheSummaryNamesEachCommandsFlags(t *testing.T) {
	summary := usageText()
	lines := strings.Split(summary, "\n")
	flag := regexp.MustCompile(`(?:^|[\s\[|])(--?[a-z][a-z-]*)`)
	placeholder := regexp.MustCompile(`<[^>]*>|[a-z]+\.\.\.|--?[a-z][a-z-]*`)
	word := regexp.MustCompile(`(?:^|[\s\[|(])([a-z][a-z-]*)`)
	var offline, allCalls []string
	for _, c := range commandTable() {
		if c.offline {
			offline = append(offline, c.name)
		}
		if c.allCalls {
			allCalls = append(allCalls, c.name)
		}
		var own strings.Builder
		in := false
		for _, l := range lines {
			switch {
			case strings.HasPrefix(l, "  nomi "+c.name+" ") || l == "  nomi "+c.name:
				in = true
			case !strings.HasPrefix(l, strings.Repeat(" ", 40)):
				in = false
			}
			if in {
				own.WriteString(l + "\n")
			}
		}
		synopsis, _, _ := strings.Cut(c.usage, "\n\n")
		for _, m := range flag.FindAllStringSubmatch(synopsis, -1) {
			f := m[1]
			if (f == "--offline" && c.offline) || (f == "--all-calls" && c.allCalls) {
				continue
			}
			if !strings.Contains(own.String(), f) {
				t.Errorf("nomi %s's usage names %s, and nomi help's lines for it do not:\n%s", c.name, f, own.String())
			}
		}
		// The words of the synopsis that are not flags or placeholders
		// (`<file>`, `paths...`) are the command's own words, a subcommand
		// such as `nomi cache prune`, and the summary names each.
		words := placeholder.ReplaceAllString(strings.TrimPrefix(synopsis, "usage: nomi "+c.name), " ")
		for _, m := range word.FindAllStringSubmatch(words, -1) {
			if !regexp.MustCompile(`\b` + m[1] + `\b`).MatchString(own.String()) {
				t.Errorf("nomi %s's usage names the word %s, and nomi help's lines for it do not:\n%s", c.name, m[1], own.String())
			}
		}
	}
	for _, want := range []string{
		"  --offline                             on " + joinAnd(offline) + ":\n",
		"  --all-calls                           on " + joinAnd(allCalls) + ": ",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("nomi help lacks %q", want)
		}
	}
	if !slices.Contains(offline, "types") || !slices.Contains(allCalls, "build") {
		t.Errorf("the command table's --offline commands are %v and --all-calls commands %v; this test checks the wrong flags", offline, allCalls)
	}
}

// Every command answers `--help` and `-h` with its usage on stdout and
// exits 0, and `nomi help <command>` prints the same; the summary `nomi
// help` prints names every command but the debugger's hidden terminal.
func TestHelp_EveryCommandAnswersHelp(t *testing.T) {
	env := append(os.Environ(), "NOMI_RUNNER_CACHE_ROOT="+t.TempDir())
	dir := t.TempDir()
	for _, c := range commandTable() {
		if !strings.HasPrefix(c.usage, "usage: nomi "+c.name) {
			t.Errorf("nomi %s's usage does not start with its name:\n%s", c.name, c.usage)
		}
		if c.name != dap.TerminalCommand && !strings.Contains(usageText(), "\n  nomi "+c.name) {
			t.Errorf("nomi help does not list nomi %s", c.name)
		}
		for _, args := range [][]string{{c.name, "--help"}, {c.name, "-h"}, {"help", c.name}} {
			o := runProcess(t, env, dir, "", nomiBin, args...)
			if o.exit != 0 || o.stdout != c.usage+"\n" || o.stderr != "" {
				t.Errorf("nomi %s: exit %d\n%s\nwant exit 0 and stdout\n%s", strings.Join(args, " "), o.exit, o.transcript(), c.usage)
			}
		}
	}
	// A run's or a tool's own arguments are its program's.
	if c, _ := lookupCommand("run"); helpRequested(c, []string{"main.nomi", "--help"}) {
		t.Error("nomi run main.nomi --help is taken as the command's --help")
	}
	if c, _ := lookupCommand("tool"); helpRequested(c, []string{"sqlkit", "generate", "-h"}) {
		t.Error("nomi tool sqlkit generate -h is taken as the command's -h")
	}
	for alias, name := range map[string]string{"--help": "help", "-h": "help", "--version": "version"} {
		if c, ok := lookupCommand(alias); !ok || c.name != name {
			t.Errorf("nomi %s is not nomi %s", alias, name)
		}
	}
	o := runProcess(t, env, dir, "", nomiBin, "help", "nosuch")
	if o.exit != 1 || o.stderr != "nomi help: unknown command \"nosuch\"\nrun `nomi help` for usage\n" {
		t.Errorf("nomi help nosuch: exit %d\n%s", o.exit, o.transcript())
	}
}

// `nomi run` and `nomi build` take a project's directory when its
// nomi.toml lists one entry point, and run or build that file; with several
// the error names them, with none it says so, and with no nomi.toml it
// says the directory is no package and ends with the usage. With no path
// the directory is the current one, with the same text as for `.`.
func TestRun_AProjectDirectoryRunsItsOneEntryPoint(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	env := buildEnv(t.TempDir())
	work := t.TempDir()
	one := filepath.Join(work, "one")
	mustWrite(t, filepath.Join(one, "nomi.toml"), "[package]\nname = \"one\"\nentry_points = [\"app\"]\n")
	mustWrite(t, filepath.Join(one, "app.nomi"), "import std::io\n\nfn main()\n    io.print(\"one\")\nend\n")
	two := filepath.Join(work, "two")
	mustWrite(t, filepath.Join(two, "nomi.toml"), "[package]\nname = \"two\"\nentry_points = [\"main\", \"cmd/seed\"]\n")
	mustWrite(t, filepath.Join(two, "main.nomi"), "fn main() end\n")
	mustWrite(t, filepath.Join(two, "cmd", "seed.nomi"), "fn main() end\n")
	none := filepath.Join(work, "none")
	mustWrite(t, filepath.Join(none, "nomi.toml"), "[package]\nname = \"none\"\nentry_points = []\n")
	mustWrite(t, filepath.Join(work, "plain", "main.nomi"), "fn main() end\n")

	if o := runProcess(t, env, work, "", nomiBin, "run", "one"); o.exit != 0 || o.stdout != "one\n" {
		t.Errorf("nomi run one: exit %d\n%s", o.exit, o.transcript())
	}
	if o := runProcess(t, env, one, "", nomiBin, "run", "."); o.exit != 0 || o.stdout != "one\n" {
		t.Errorf("nomi run . in one: exit %d\n%s", o.exit, o.transcript())
	}
	const (
		severalHere = "nomi run: .'s nomi.toml lists 2 entry points, main.nomi and cmd/seed.nomi; name the one to run, such as `nomi run main.nomi`\n"
		noneHere    = "nomi run: . lists no entry point in its nomi.toml; add the file with the program's fn main to entry_points\n"
		plainHere   = "nomi run: . is not a package (it has no nomi.toml), so it has no entry point to run\n" + runUsage + "\n"
	)
	for _, tc := range []struct {
		dir  string
		args []string
		want string
	}{
		{work, []string{"run", "two"}, "nomi run: two's nomi.toml lists 2 entry points, two/main.nomi and two/cmd/seed.nomi; name the one to run, such as `nomi run two/main.nomi`\n"},
		{work, []string{"build", "two"}, "nomi build: two's nomi.toml lists 2 entry points, two/main.nomi and two/cmd/seed.nomi; name the one to build, such as `nomi build two/main.nomi`\n"},
		{work, []string{"run", "none"}, "nomi run: none lists no entry point in its nomi.toml; add the file with the program's fn main to entry_points\n"},
		{work, []string{"build", "none"}, "nomi build: none lists no entry point in its nomi.toml; add the file with the program's fn main to entry_points\n"},
		{work, []string{"run", "plain"}, "nomi run: plain is not a package (it has no nomi.toml), so it has no entry point to run\n" + runUsage + "\n"},
		{work, []string{"build", "plain"}, "nomi build: plain is not a package (it has no nomi.toml), so it has no entry point to build\n" + buildUsage + "\n"},
		// No path is the current directory, in every mode of run, and
		// gives the text `.` gives.
		{two, []string{"run", "."}, severalHere},
		{two, []string{"run"}, severalHere},
		{two, []string{"run", "--watch"}, severalHere},
		{two, []string{"run", "--web", "--port", "0"}, severalHere},
		{two, []string{"run", "--profile"}, severalHere},
		{two, []string{"build"}, "nomi build: .'s nomi.toml lists 2 entry points, main.nomi and cmd/seed.nomi; name the one to build, such as `nomi build main.nomi`\n"},
		{none, []string{"run", "."}, noneHere},
		{none, []string{"run"}, noneHere},
		{none, []string{"build", "--no-vm"}, "nomi build: . lists no entry point in its nomi.toml; add the file with the program's fn main to entry_points\n"},
		{filepath.Join(work, "plain"), []string{"run", "."}, plainHere},
		{filepath.Join(work, "plain"), []string{"run"}, plainHere},
		{filepath.Join(work, "plain"), []string{"build"}, "nomi build: . is not a package (it has no nomi.toml), so it has no entry point to build\n" + buildUsage + "\n"},
	} {
		o := runProcess(t, env, tc.dir, "", nomiBin, tc.args...)
		if o.exit != 1 || o.stdout+o.stderr != tc.want {
			t.Errorf("nomi %s in %s: exit %d\n%s\nwant exit 1 and\n%s", strings.Join(tc.args, " "), filepath.Base(tc.dir), o.exit, o.transcript(), tc.want)
		}
	}
	// Beside the directory, the executable `nomi build one` writes by
	// default would be the directory, except on Windows, where it is
	// one.exe.
	if runtime.GOOS != "windows" {
		o := runProcess(t, env, work, "", nomiBin, "build", "one")
		if want := "nomi build: one is a directory, so the program cannot be written there; name the file to write with -o\n"; o.exit != 1 || o.stderr != want {
			t.Errorf("nomi build one beside one: exit %d\n%s\nwant\n%s", o.exit, o.transcript(), want)
		}
	}
}

// `nomi build <dir>` builds the project's one entry point under the
// directory's name, and `nomi build` with no path builds the current
// directory's, to the same bytes.
func TestBuild_AProjectDirectoryBuildsItsOneEntryPoint(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	env := buildEnv(t.TempDir())
	one := filepath.Join(t.TempDir(), "one")
	mustWrite(t, filepath.Join(one, "nomi.toml"), "[package]\nname = \"one\"\nentry_points = [\"app\"]\n")
	mustWrite(t, filepath.Join(one, "app.nomi"), "import std::io\n\nfn main()\n    io.print(\"one\")\nend\n")
	if o := runProcess(t, env, one, "", nomiBin, "build", ".", "--no-vm"); o.exit != 0 {
		t.Fatalf("nomi build . --no-vm: exit %d\n%s", o.exit, o.transcript())
	}
	if o := runProcess(t, env, one, "", nomiBin, "run", "one.nomic"); o.exit != 0 || o.stdout != "one\n" {
		t.Errorf("nomi run one.nomic: exit %d\n%s", o.exit, o.transcript())
	}
	named := mustRead(t, filepath.Join(one, "one.nomic"))
	if err := os.Remove(filepath.Join(one, "one.nomic")); err != nil {
		t.Fatal(err)
	}
	if o := runProcess(t, env, one, "", nomiBin, "build", "--no-vm"); o.exit != 0 {
		t.Fatalf("nomi build --no-vm: exit %d\n%s", o.exit, o.transcript())
	}
	if here := mustRead(t, filepath.Join(one, "one.nomic")); here != named {
		t.Errorf("nomi build --no-vm wrote %d bytes to one.nomic, and nomi build . --no-vm %d others", len(here), len(named))
	}
	bin := exe(filepath.Join(one, "one"))
	if o := runProcess(t, env, one, "", nomiBin, "build", "."); o.exit != 0 {
		t.Fatalf("nomi build .: exit %d\n%s", o.exit, o.transcript())
	}
	if o := runProcess(t, env, one, "", bin); o.exit != 0 || o.stdout != "one\n" {
		t.Errorf("the executable nomi build . wrote: exit %d\n%s", o.exit, o.transcript())
	}
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	if o := runProcess(t, env, one, "", nomiBin, "build"); o.exit != 0 {
		t.Fatalf("nomi build: exit %d\n%s", o.exit, o.transcript())
	}
	if o := runProcess(t, env, one, "", bin); o.exit != 0 || o.stdout != "one\n" {
		t.Errorf("the executable nomi build wrote: exit %d\n%s", o.exit, o.transcript())
	}
	other := exe(filepath.Join(t.TempDir(), "other"))
	if o := runProcess(t, env, one, "", nomiBin, "build", "-o", other); o.exit != 0 {
		t.Fatalf("nomi build -o: exit %d\n%s", o.exit, o.transcript())
	}
	if o := runProcess(t, env, one, "", other); o.exit != 0 || o.stdout != "one\n" {
		t.Errorf("the executable nomi build -o wrote: exit %d\n%s", o.exit, o.transcript())
	}
}

// `nomi run`, `check`, `build`, `test` and `effects` with no path act on the
// current directory: each one's parser gives `.` for no path and the word
// for one, and those that take one path refuse a second.
func TestNoPath_EveryParserNamesTheCurrentDirectory(t *testing.T) {
	parsers := map[string]func(path ...string) (string, error){
		"check": func(path ...string) (string, error) { return parseCheckArgs(path) },
		"build": func(path ...string) (string, error) {
			b, err := parseBuildArgs(append([]string{"--no-vm"}, path...))
			return b.entry, err
		},
		"test": func(path ...string) (string, error) {
			parsed, err := parseTestArgs(append([]string{"--all"}, path...))
			return parsed.root, err
		},
		"effects": func(path ...string) (string, error) {
			parsed, err := parseEffectsArgs(append([]string{"--check"}, path...))
			return parsed.path, err
		},
		"run --watch": func(path ...string) (string, error) {
			_, entry, _, err := parseRunWatchArgs(append([]string{"--watch"}, path...))
			return entry, err
		},
		"run --web": func(path ...string) (string, error) {
			r, err := parseRunWebArgs(append([]string{"--web"}, path...))
			return r.entry, err
		},
	}
	for name, parse := range parsers {
		if got, err := parse(); err != nil || got != "." {
			t.Errorf("nomi %s with no path: %q, %v; want the current directory", name, got, err)
		}
		if got, err := parse("app"); err != nil || got != "app" {
			t.Errorf("nomi %s app: %q, %v; want app", name, got, err)
		}
		if name == "run --watch" {
			// The words after its file are the program's.
			continue
		}
		if _, err := parse(".", "app"); err == nil || !strings.Contains(err.Error(), `unexpected argument "app"`) {
			t.Errorf("nomi %s . app: %v; want its second path refused", name, err)
		}
	}
}

// `nomi run` with no word but its own flags runs the current directory's
// package, whose program reads the command's standard input. A first word
// that is no flag is the path, so the program's arguments follow one.
func TestRun_NoPathRunsTheCurrentPackage(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	env := buildEnv(t.TempDir())
	one := filepath.Join(t.TempDir(), "one")
	mustWrite(t, filepath.Join(one, "nomi.toml"), "[package]\nname = \"one\"\nentry_points = [\"app\"]\n")
	mustWrite(t, filepath.Join(one, "app.nomi"), imagePrograms["echo.nomi"])
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"run"}, "args: []\nread: typed\n"},
		{[]string{"run", "."}, "args: []\nread: typed\n"},
		{[]string{"run", "--offline"}, "args: []\nread: typed\n"},
		{[]string{"run", "--all-calls", "--profile"}, "args: []\nread: typed\n"},
		{[]string{"run", ".", "a", "--watch"}, "args: [a, --watch]\nread: typed\n"},
		{[]string{"run", "app.nomi", "a"}, "args: [a]\nread: typed\n"},
	} {
		o := runProcess(t, env, one, "typed\n", nomiBin, tc.args...)
		if o.exit != 0 || o.stdout != tc.want {
			t.Errorf("nomi %s: exit %d\n%s\nwant exit 0 and stdout\n%s", strings.Join(tc.args, " "), o.exit, o.transcript(), tc.want)
		}
	}
	// A word that is no flag is the file or directory, never an argument
	// of the current directory's program.
	o := runProcess(t, env, one, "typed\n", nomiBin, "run", "a")
	if want := "nomi run: a: no such file or directory\n"; o.exit != 1 || o.stdout+o.stderr != want {
		t.Errorf("nomi run a: exit %d\n%s\nwant exit 1 and\n%s", o.exit, o.transcript(), want)
	}
	// With no path, a directory inside the package runs the package's
	// program (programOrHere); named, a directory is itself and no other.
	deep := filepath.Join(one, "src", "deep")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	o = runProcess(t, env, deep, "typed\n", nomiBin, "run")
	if want := "args: []\nread: typed\n"; o.exit != 0 || o.stdout != want {
		t.Errorf("nomi run inside the package: exit %d\n%s\nwant exit 0 and stdout\n%s", o.exit, o.transcript(), want)
	}
	o = runProcess(t, env, deep, "", nomiBin, "run", ".")
	if !strings.HasPrefix(o.stderr, "nomi run: . is not a package (it has no nomi.toml)") || o.exit != 1 {
		t.Errorf("nomi run . inside the package: exit %d\n%s\nwant exit 1 and that . is not a package", o.exit, o.transcript())
	}
	o = runProcess(t, env, deep, "", nomiBin, "build")
	if _, err := os.Stat(filepath.Join(deep, exe("one"))); err != nil || o.exit != 0 {
		t.Errorf("nomi build inside the package: exit %d, %v\n%s\nwant the package's executable in the current directory", o.exit, err, o.transcript())
	}
}

// `nomi check` with no path checks the current directory, as `nomi check .`
// does: a package's entry points and the files nothing imports, and outside
// a package every program in the directory. (At a workspace's root it is
// every member: TestPackages_WorkspaceRootCommandsVisitEachMember.)
func TestCheck_NoPathChecksTheCurrentDirectory(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	env := buildEnv(t.TempDir())
	work := t.TempDir()
	one := filepath.Join(work, "one")
	mustWrite(t, filepath.Join(one, "nomi.toml"), "[package]\nname = \"one\"\nentry_points = [\"app\"]\n")
	mustWrite(t, filepath.Join(one, "app.nomi"), "fn main() end\n")
	two := filepath.Join(work, "two")
	mustWrite(t, filepath.Join(two, "nomi.toml"), "[package]\nname = \"two\"\nentry_points = [\"main\", \"cmd/seed\"]\n")
	mustWrite(t, filepath.Join(two, "main.nomi"), "fn main() end\n")
	mustWrite(t, filepath.Join(two, "cmd", "seed.nomi"), "fn main() end\n")
	none := filepath.Join(work, "none")
	mustWrite(t, filepath.Join(none, "nomi.toml"), "[package]\nname = \"none\"\nentry_points = []\n")
	mustWrite(t, filepath.Join(none, "util.nomi"), "pub fn two() -> Int\n    2\nend\n")
	plain := filepath.Join(work, "plain")
	mustWrite(t, filepath.Join(plain, "main.nomi"), "fn main() end\n")
	bad := filepath.Join(work, "bad")
	mustWrite(t, filepath.Join(bad, "nomi.toml"), "[package]\nname = \"bad\"\nentry_points = [\"main\"]\n")
	mustWrite(t, filepath.Join(bad, "main.nomi"), "fn main()\n    1 + \"x\"\nend\n")
	for _, tc := range []struct {
		dir  string
		args []string
		exit int
		out  string
	}{
		{one, []string{"check"}, 0, "ok app.nomi\n"},
		{one, []string{"check", "--format", "short", "--hints"}, 0, "ok app.nomi\n"},
		{two, []string{"check"}, 0, "ok cmd/seed.nomi\nok main.nomi\n"},
		{none, []string{"check"}, 0, "ok util.nomi\n"},
		{plain, []string{"check"}, 0, "ok main.nomi\n"},
		{bad, []string{"check", "--format", "short"}, 1, ""},
	} {
		here := runProcess(t, env, tc.dir, "", nomiBin, tc.args...)
		if here.exit != tc.exit || here.stdout != tc.out {
			t.Errorf("nomi %s in %s: exit %d\n%s\nwant exit %d and stdout\n%s", strings.Join(tc.args, " "), filepath.Base(tc.dir), here.exit, here.transcript(), tc.exit, tc.out)
		}
		dot := runProcess(t, env, tc.dir, "", nomiBin, append(tc.args, ".")...)
		if dot.exit != here.exit || dot.transcript() != here.transcript() {
			t.Errorf("nomi %s in %s printed\n%s\nand with . printed\n%s", strings.Join(tc.args, " "), filepath.Base(tc.dir), here.transcript(), dot.transcript())
		}
	}
	if o := runProcess(t, env, bad, "", nomiBin, "check", "--format", "short"); !strings.Contains(o.stderr, "main.nomi:2:") {
		t.Errorf("nomi check --format short of a program with an error: exit %d\n%s\nwant its error at main.nomi:2", o.exit, o.transcript())
	}
	o := runProcess(t, env, one, "", nomiBin, "check", ".", "app.nomi")
	if want := "nomi check: unexpected argument \"app.nomi\"\n" + checkUsage + "\n"; o.exit != 1 || o.stderr != want {
		t.Errorf("nomi check . app.nomi: exit %d\n%s\nwant exit 1 and\n%s", o.exit, o.transcript(), want)
	}
}
