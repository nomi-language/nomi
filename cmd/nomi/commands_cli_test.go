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

// TestHelp_TheSummaryNamesEachCommandsFlags: every flag in a command's usage
// line is in the lines `nomi help` gives that command (the lines that start
// `nomi <name>` and the description under each). --offline and --all-calls
// are named once for every command that takes them, from the command table,
// and the summary's line for each names exactly those commands.
func TestHelp_TheSummaryNamesEachCommandsFlags(t *testing.T) {
	summary := usageText()
	lines := strings.Split(summary, "\n")
	flag := regexp.MustCompile(`(?:^|[\s\[|])(--?[a-z][a-z-]*)`)
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
// the error names them, and with none, or no nomi.toml, it asks for the
// file.
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
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"run", "two"}, "nomi run: two's nomi.toml lists 2 entry points, two/main.nomi and two/cmd/seed.nomi; name the one to run, such as `nomi run two/main.nomi`\n"},
		{[]string{"build", "two"}, "nomi build: two's nomi.toml lists 2 entry points, two/main.nomi and two/cmd/seed.nomi; name the one to build, such as `nomi build two/main.nomi`\n"},
		{[]string{"run", "none"}, "nomi run: none is a directory; name the program's .nomi file\n"},
		{[]string{"build", "none"}, "nomi build: none is a directory; name the program's .nomi file\n"},
		{[]string{"run", "plain"}, "nomi run: plain is a directory; name the program's .nomi file\n"},
	} {
		o := runProcess(t, env, work, "", nomiBin, tc.args...)
		if o.exit != 1 || o.stdout+o.stderr != tc.want {
			t.Errorf("nomi %s: exit %d\n%s\nwant exit 1 and\n%s", strings.Join(tc.args, " "), o.exit, o.transcript(), tc.want)
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
// directory's name.
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
	bin := exe(filepath.Join(one, "one"))
	if o := runProcess(t, env, one, "", nomiBin, "build", "."); o.exit != 0 {
		t.Fatalf("nomi build .: exit %d\n%s", o.exit, o.transcript())
	}
	if o := runProcess(t, env, one, "", bin); o.exit != 0 || o.stdout != "one\n" {
		t.Errorf("the executable nomi build . wrote: exit %d\n%s", o.exit, o.transcript())
	}
}
