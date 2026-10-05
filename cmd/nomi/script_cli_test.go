package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const shebangScript = `#!/usr/bin/env nomi
import std/io

struct App {
    context: Context
}

fn boot(startup: Startup): App {
    io.print(startup.args)
    App{context: Context.root()}
}

fn main() {
    io.print("hi")
}
`

// `nomi <file>.nomi args...` is `nomi run <file>.nomi args...`: the program
// gets the arguments after the file, flags included.
func TestScriptMode_RunsFileWithArgs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	script := filepath.Join(t.TempDir(), "hi.nomi")
	mustWrite(t, script, shebangScript)
	want := "[a, b c, --flag]\nhi\n"
	for _, args := range [][]string{
		{script, "a", "b c", "--flag"},
		{"run", script, "a", "b c", "--flag"},
	} {
		out, err := exec.Command(nomiBin, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("nomi %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		if string(out) != want {
			t.Errorf("nomi %s: got %q, want %q", strings.Join(args, " "), out, want)
		}
	}
}

// The kernel runs `./hi.nomi a b` as `nomi ./hi.nomi a b` when the file is
// executable and starts with `#!/usr/bin/env nomi`. A lone script in an
// arbitrary directory runs in file mode, as `nomi run` would run it.
func TestScriptMode_ExecutableShebangScript(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	if runtime.GOOS == "windows" {
		t.Skip("no shebang execution on Windows")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "hi.nomi")
	mustWrite(t, script, shebangScript)
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("./hi.nomi", "a", "b")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(nomiBin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("./hi.nomi a b: %v\n%s", err, out)
	}
	if want := "[a, b]\nhi\n"; string(out) != want {
		t.Fatalf("./hi.nomi a b: got %q, want %q", out, want)
	}
}

// An extensionless file that starts with `#!` is a script too: on PATH it
// runs as `hi a b`, it imports `.nomi` files beside it, and `nomi run`,
// `nomi check` and `nomi test` take it. A file with neither the extension
// nor a `#!` is still not a program.
func TestScriptMode_ExtensionlessScript(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	if runtime.GOOS == "windows" {
		t.Skip("no shebang execution on Windows")
	}
	bin := t.TempDir()
	script := filepath.Join(bin, "hi")
	mustWrite(t, script, strings.Replace(shebangScript, "import std/io\n", "import std/io\nimport greet\n", 1)+
		"\ntest \"greets\" {\n    assert greet.hello() == \"hello\"\n}\n")
	mustWrite(t, filepath.Join(bin, "greet.nomi"), "pub fn hello(): String {\n    \"hello\"\n}\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", "hi a b")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+filepath.Dir(nomiBin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hi a b on PATH: %v\n%s", err, out)
	}
	if want := "[a, b]\nhi\n"; string(out) != want {
		t.Fatalf("hi a b: got %q, want %q", out, want)
	}
	for _, args := range [][]string{{"run", script, "x"}, {script, "x"}} {
		if out, err := exec.Command(nomiBin, args...).CombinedOutput(); err != nil || string(out) != "[x]\nhi\n" {
			t.Errorf("nomi %s: %v %q", strings.Join(args, " "), err, out)
		}
	}
	if out, err := exec.Command(nomiBin, "check", script).CombinedOutput(); err != nil {
		t.Errorf("nomi check <script>: %v\n%s", err, out)
	}
	if out, err := exec.Command(nomiBin, "test", script).CombinedOutput(); err != nil || !strings.Contains(string(out), "1 passed") {
		t.Errorf("nomi test <script>: %v\n%s", err, out)
	}

	plain := filepath.Join(bin, "notes")
	mustWrite(t, plain, "fn main() {}\n")
	if out, err := exec.Command(nomiBin, plain).CombinedOutput(); err == nil || !strings.Contains(string(out), "unknown command") {
		t.Errorf("nomi <file without #!>: want unknown command, got %v:\n%s", err, out)
	}
	if out, err := exec.Command(nomiBin, "run", plain).CombinedOutput(); err == nil ||
		!strings.Contains(string(out), "is not a .nomi file and does not start with a #! line") {
		t.Errorf("nomi run <file without #!>: got %v:\n%s", err, out)
	}
}

// An extensionless script's project root is its own directory. In a module
// subdirectory it is therefore not one of the module's entries and needs no
// entry_points line, where a `.nomi` file there would.
func TestScriptMode_ExtensionlessScriptIsRootedAtItsDirectory(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	mod := t.TempDir()
	mustWrite(t, filepath.Join(mod, "nomi.toml"), "[module]\nname = \"m\"\nentry_points = [\"main\"]\n")
	mustWrite(t, filepath.Join(mod, "main.nomi"), "fn main() {}\n")
	body := "#!/usr/bin/env nomi\nimport std/io\n\nfn main() {\n    io.print(\"deploy\")\n}\n"
	mustWrite(t, filepath.Join(mod, "scripts", "deploy"), body)
	mustWrite(t, filepath.Join(mod, "scripts", "deploy2.nomi"), body)

	out, err := exec.Command(nomiBin, filepath.Join(mod, "scripts", "deploy")).CombinedOutput()
	if err != nil || string(out) != "deploy\n" {
		t.Errorf("extensionless script in a module subdirectory: %v %q", err, out)
	}
	out, err = exec.Command(nomiBin, filepath.Join(mod, "scripts", "deploy2.nomi")).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "not declared in nomi.toml entry_points") {
		t.Errorf("a .nomi script there is still a module entry: %v\n%s", err, out)
	}
}

// A first argument ending in `.nomi` is a file even when it is missing, so
// the error names the file. Subcommand names never end in `.nomi`, so
// `nomi test` stays the command; a diagnostic after the shebang keeps its
// source line.
func TestScriptMode_Precedence(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir := t.TempDir()
	missing := filepath.Join(dir, "test.nomi")
	out, err := exec.Command(nomiBin, missing).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "nomi run: "+missing+": no such file or directory") {
		t.Errorf("nomi <missing>.nomi: want a missing-file error, got %v:\n%s", err, out)
	}

	bad := filepath.Join(dir, "bad.nomi")
	mustWrite(t, bad, "#!/usr/bin/env nomi\nimport std/io\n\nfn main() {\n    io.print(1 + \"x\")\n}\n")
	out, err = exec.Command(nomiBin, bad).CombinedOutput()
	if err == nil || !strings.Contains(string(out), bad+":5:") {
		t.Errorf("nomi bad.nomi: want a diagnostic at line 5, got %v:\n%s", err, out)
	}

	help, err := exec.Command(nomiBin, "help").Output()
	if err != nil || !strings.Contains(string(help), "nomi <file> [args]") {
		t.Errorf("nomi help does not list script mode: %v\n%s", err, help)
	}
}
