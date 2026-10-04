package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Front-end diagnostics print each error with its source line and its span
// underlined, then its hints and related locations, with the path relative to
// the working directory. They name the file each error is in: an error in an
// imported file names that file. `nomi check --format short` and
// NOMI_DIAGNOSTICS=short print the one-line `path:line:col: message` form.
func TestDiagnostics_NameTheirFile(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	env := append(os.Environ(), "NOMI_FFIRUN_CACHE_ROOT="+t.TempDir(), "NOMI_COLOR=never", "NOMI_DIAGNOSTICS=")
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "main.nomi"),
		"import std/io\nimport other\n\nfn main() {\n  x: Int = \"s\"\n  io.print(other.f())\n}\n")
	mustWrite(t, filepath.Join(dir, "other.nomi"), "pub fn f(): String {\n  1\n}\n")

	want := `error: binding 'x' is never read
 --> main.nomi:5:3
  |
5 |   x: Int = "s"
  |   ^
  = help: prefix it with '_' if the value is intentionally ignored

error: type mismatch: expected Int, got String
 --> main.nomi:5:12
  |
5 |   x: Int = "s"
  |            ^^^

error: return type mismatch: expected String, got Int
 --> other.nomi:2:3
  |
2 |   1
  |   ^
  = note: the return type is declared here: other.nomi:1:13
`
	for _, cmd := range []string{"run", "check", "build"} {
		o := runProcess(t, env, dir, "", nomiBin, cmd, "main.nomi")
		if o.exit != 1 || o.stdout != "" || o.stderr != want {
			t.Errorf("nomi %s: exit %d\nstdout:\n%s\nstderr:\n%s\nwant stderr:\n%s", cmd, o.exit, o.stdout, o.stderr, want)
		}
	}

	wantShort := "main.nomi:5:3: binding 'x' is never read\n" +
		"main.nomi:5:3: help: prefix it with '_' if the value is intentionally ignored\n" +
		"main.nomi:5:12: type mismatch: expected Int, got String\n" +
		"other.nomi:2:3: return type mismatch: expected String, got Int\n" +
		"other.nomi:1:13: note: the return type is declared here\n"
	for _, args := range [][]string{{"check", "--format", "short", "main.nomi"}, {"check", "--format=short", "main.nomi"}} {
		o := runProcess(t, env, dir, "", nomiBin, args...)
		if o.exit != 1 || o.stderr != wantShort {
			t.Errorf("nomi %v: exit %d\nstderr:\n%s\nwant stderr:\n%s", args, o.exit, o.stderr, wantShort)
		}
	}
	shortEnv := append(append([]string(nil), env...), "NOMI_DIAGNOSTICS=short")
	if o := runProcess(t, shortEnv, dir, "", nomiBin, "run", "main.nomi"); o.exit != 1 || o.stderr != wantShort {
		t.Errorf("NOMI_DIAGNOSTICS=short nomi run: exit %d\nstderr:\n%s\nwant stderr:\n%s", o.exit, o.stderr, wantShort)
	}
	if o := runProcess(t, env, dir, "", nomiBin, "check", "--format", "long", "main.nomi"); o.exit != 1 || !strings.Contains(o.stderr, `unknown format "long"`) {
		t.Errorf("nomi check --format long: exit %d\nstderr:\n%s", o.exit, o.stderr)
	}

	// On a terminal, or with NOMI_COLOR=always, the labels, gutter and
	// underline are coloured.
	colourEnv := append(append([]string(nil), env...), "NOMI_COLOR=always")
	o := runProcess(t, colourEnv, dir, "", nomiBin, "check", "main.nomi")
	for _, frag := range []string{"\x1b[1m\x1b[31merror:\x1b[0m", "\x1b[36m-->\x1b[0m main.nomi:5:12", "\x1b[1m\x1b[31m^^^\x1b[0m"} {
		if !strings.Contains(o.stderr, frag) {
			t.Errorf("NOMI_COLOR=always nomi check lacks %q:\n%q", frag, o.stderr)
		}
	}

	// `nomi test` names the file that failed to load, then its diagnostics.
	mustWrite(t, filepath.Join(dir, "main_test.nomi"), "import other\n\ntest \"f\" {\n  assert other.f() == \"x\"\n}\n")
	o = runProcess(t, env, dir, "", nomiBin, "test", "main_test.nomi")
	wantTest := "FAIL main_test.nomi\n" + `error: return type mismatch: expected String, got Int
 --> other.nomi:2:3
  |
2 |   1
  |   ^
  = note: the return type is declared here: other.nomi:1:13
` + "test result: FAILED. 0 passed, 1 failed\n"
	if o.exit != 1 || o.stdout != wantTest {
		t.Errorf("nomi test: exit %d\nstdout:\n%s\nwant:\n%s\nstderr:\n%s", o.exit, o.stdout, wantTest, o.stderr)
	}
}

func TestDiagnostics_ImportedFileBuildErrors(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	// The short form, which names each file on each line.
	env := append(os.Environ(), "NOMI_FFIRUN_CACHE_ROOT="+t.TempDir(), "NOMI_DIAGNOSTICS=short")
	const manifest = "[module]\nname = \"app\"\nentry_points = [\"main\"]\n"
	const main = "import std/io\nimport helper\n\nfn main() {\n  io.print(helper.greet())\n}\n"
	const test = "import helper\n\ntest \"greet\" {\n  assert helper.greet() == \"hi\"\n}\n"
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name: "unused import",
			files: map[string]string{
				"helper.nomi": "import std/regex\n\npub fn greet(): String {\n  \"hi\"\n}\n",
			},
			want: "helper.nomi:1:12: imported module 'regex' is unused — remove the import\n",
		},
		{
			name: "struct and enum of one name",
			files: map[string]string{
				"helper.nomi": "struct A {\n  x: Int\n}\n\nenum A {\n  One\n}\n\npub fn greet(): String {\n  \"hi\"\n}\n",
			},
			want: "helper.nomi:5:6: 'A' is already defined in this scope as a type\n" +
				"helper.nomi:5:6: help: pick a different name\n" +
				"helper.nomi:1:8: note: 'A' is first defined here\n",
		},
		{
			name: "a file two files import",
			files: map[string]string{
				"helper.nomi": "import left\nimport right\n\npub fn greet(): String {\n  \"${left.f()}${right.f()}\"\n}\n",
				"left.nomi":   "import shared\n\npub fn f(): String {\n  shared.s()\n}\n",
				"right.nomi":  "import shared\n\npub fn f(): String {\n  shared.s()\n}\n",
				"shared.nomi": "import std/regex\n\npub fn s(): String {\n  \"h\"\n}\n",
			},
			want: "shared.nomi:1:12: imported module 'regex' is unused — remove the import\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mustWrite(t, filepath.Join(dir, "nomi.toml"), manifest)
			mustWrite(t, filepath.Join(dir, "main.nomi"), main)
			for name, src := range tc.files {
				mustWrite(t, filepath.Join(dir, name), src)
			}
			for _, cmd := range []string{"run", "check"} {
				o := runProcess(t, env, dir, "", nomiBin, cmd, "main.nomi")
				if o.exit != 1 || o.stdout != "" || o.stderr != tc.want {
					t.Errorf("nomi %s: exit %d\nstdout:\n%s\nstderr:\n%s\nwant stderr:\n%s", cmd, o.exit, o.stdout, o.stderr, tc.want)
				}
			}
			mustWrite(t, filepath.Join(dir, "main_test.nomi"), test)
			o := runProcess(t, env, dir, "", nomiBin, "test", "main_test.nomi")
			wantTest := "FAIL main_test.nomi\n" + tc.want + "test result: FAILED. 0 passed, 1 failed\n"
			if o.exit != 1 || o.stdout != wantTest {
				t.Errorf("nomi test: exit %d\nstdout:\n%s\nwant:\n%s\nstderr:\n%s", o.exit, o.stdout, wantTest, o.stderr)
			}
		})
	}
}

// A path a command cannot use is reported as the path the user typed, not as
// the Go error of the call that failed on it.
func TestCommands_ReportAnUnusablePathPlainly(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	env := append(os.Environ(), "NOMI_FFIRUN_CACHE_ROOT="+t.TempDir())
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "notes.txt"), "hello\n")
	mustWrite(t, filepath.Join(dir, "bad.nomi"), "fn f( {\n")
	if err := os.Mkdir(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		exit int
		out  string
	}{
		{[]string{"run", "missing.nomi"}, 1, "nomi run: missing.nomi: no such file or directory\n"},
		{[]string{"run", "empty"}, 1, "nomi run: empty is a directory; name the program's .nomi file\n"},
		{[]string{"run", "notes.txt"}, 1, "nomi run: notes.txt is not a .nomi file\n"},
		{[]string{"build", "missing.nomi"}, 1, "nomi build: missing.nomi: no such file or directory\n"},
		{[]string{"check", "missing.nomi"}, 1, "nomi check: missing.nomi: no such file or directory\n"},
		{[]string{"test", "missing"}, 1, "nomi test: missing: no such file or directory\n"},
		{[]string{"test", "empty"}, 0, "no test files found in empty\n"},
		{[]string{"fmt", "missing.nomi"}, 1, "nomi fmt: missing.nomi: no such file or directory\n"},
		// A file fmt cannot parse is reported once, as a diagnostic.
		{[]string{"fmt", "bad.nomi"}, 1, "bad.nomi:1:1: expected '}' in struct pattern\n"},
	} {
		o := runProcess(t, env, dir, "", nomiBin, tc.args...)
		if got := o.stdout + o.stderr; o.exit != tc.exit || got != tc.out {
			t.Errorf("nomi %v: exit %d, output:\n%s\nwant exit %d, output:\n%s", tc.args, o.exit, got, tc.exit, tc.out)
		}
	}
}

// A syntax error in an imported file is reported against that file. The
// loader used to recover past it silently, and the program failed later with
// a confusing BLOCKED line or a check that passed.
func TestDiagnostics_ASyntaxErrorInAnImportedFileNamesIt(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	env := append(os.Environ(), "NOMI_FFIRUN_CACHE_ROOT="+t.TempDir(), "NOMI_DIAGNOSTICS=short")
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "main.nomi"),
		"import std/io\nimport other\n\nfn main() {\n  io.print(other.f())\n}\n")
	mustWrite(t, filepath.Join(dir, "other.nomi"), "pub fn f(): String -> {\n  \"x\"\n}\n")

	want := "other.nomi:1:5: expected '{' for function body\n"
	for _, cmd := range []string{"run", "check"} {
		o := runProcess(t, env, dir, "", nomiBin, cmd, "main.nomi")
		if o.exit != 1 || o.stderr != want {
			t.Errorf("nomi %s: exit %d\nstdout:\n%s\nstderr:\n%s\nwant stderr:\n%s", cmd, o.exit, o.stdout, o.stderr, want)
		}
	}
}
