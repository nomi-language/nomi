package main

// `nomi build --web` of a project with Go bindings: the wrapper lowers the
// program and the runner links the bound packages, built for js/wasm
// (build_web.go). See build_web_test.go for the folder itself.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// webFFIEnv is buildEnv with an isolated managed toolchain root, as every
// test that builds Go bindings sets.
func webFFIEnv(t *testing.T) []string {
	t.Helper()
	return append(buildEnv(t.TempDir()), "NOMI_TOOLCHAIN_ROOT="+t.TempDir())
}

// repoGoLine is the root go.mod's `go` line, which a test project's go.mod
// repeats.
func repoGoLine(t *testing.T) string {
	t.Helper()
	goMod, err := os.ReadFile(filepath.Join(repoRoot(t), "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	return regexp.MustCompile(`(?m)^go .*$`).FindString(string(goMod))
}

// TestBuildWeb_GoBindingsRunInTheBrowser builds two projects with Go
// bindings for --web: testdata/go_bindings, whose adapter package is the
// project's own Go, and a file that binds Go standard library functions
// with no go.mod. Each folder, run under Node through nomi.js, prints what
// `nomi run` prints.
func TestBuildWeb_GoBindingsRunInTheBrowser(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds js/wasm runners with Go bindings; -short")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on PATH")
	}
	if major := nodeMajor(t, node); major < 22 {
		t.Skipf("node %d; the harness needs 22 or newer", major)
	}
	env := webFFIEnv(t)
	stdDir := t.TempDir()
	stdEntry := filepath.Join(stdDir, "main.nomi")
	mustWrite(t, stdEntry, `import std/io

gopkg "strings"

gopkg "strconv"

fn upper(s: String) -> String go strings.ToUpper

fn parse_int(s: String) -> Result<Int, String> go strconv.Atoi

fn main() {
    io.print(upper("hi"))
    io.print(parse_int("42"))
    io.print(parse_int("x"))
}
`)
	entries := []string{
		filepath.Join(repoRoot(t), "cmd", "nomi", "testdata", "go_bindings", "main.nomi"),
		stdEntry,
	}
	type webRun struct {
		Folder string `json:"folder"`
	}
	type webResult struct {
		Stdout, Stderr, Fatal string
		ExitCode              int
	}
	var runs []webRun
	for _, entry := range entries {
		folder := filepath.Join(t.TempDir(), "web")
		if b := runProcess(t, env, filepath.Dir(entry), "", nomiBin, "build", "--web", entry, "-o", folder); b.exit != 0 || b.stderr != "" {
			t.Fatalf("nomi build --web %s: exit %d\n%s", entry, b.exit, b.transcript())
		}
		decls, err := os.ReadFile(filepath.Join(folder, "nomi.d.ts"))
		if err != nil || !strings.Contains(string(decls), "export function load(") {
			t.Fatalf("%s: nomi.d.ts: %v\n%s", entry, err, decls)
		}
		runs = append(runs, webRun{Folder: folder})
	}
	spec, _ := json.Marshal(map[string]any{"runs": runs})
	harness := filepath.Join(repoRoot(t), "cmd", "nomi", "testdata", "web", "harness.mjs")
	got := runNode(t, env, t.TempDir(), "", node, harness, string(spec))
	if got.exit != 0 {
		t.Fatalf("the harness: exit %d\n%s", got.exit, got.transcript())
	}
	var results []webResult
	if err := json.Unmarshal([]byte(got.stdout), &results); err != nil || len(results) != len(runs) {
		t.Fatalf("the harness printed %q (%v)", got.stdout, err)
	}
	for i, entry := range entries {
		want := runProcess(t, env, filepath.Dir(entry), "", nomiBin, "run", entry)
		r := results[i]
		if r.Stdout != want.stdout || r.Stderr != want.stderr || r.ExitCode != want.exit {
			t.Fatalf("%s under nomi.js differs from nomi run\n--- nomi run (exit %d) ---\n%s\n--- web (exit %d, fatal %q) ---\nstdout: %q\nstderr: %q",
				entry, want.exit, want.transcript(), r.ExitCode, r.Fatal, r.Stdout, r.Stderr)
		}
	}
	if results[1].Stdout != "HI\nOk(42)\nErr(strconv.Atoi: parsing \"x\": invalid syntax)\n" ||
		!strings.Contains(results[0].Stdout, "sha256(nomi) = 0f937e60") {
		t.Fatalf("the programs ran as %+v; this test compares the wrong thing", results)
	}
}

// TestBuildWeb_AGoPackageThatDoesNotBuildForTheTargetIsRefused binds a
// package that imports one with no Go files for js/wasm or wasip1. The
// build is refused at the `gopkg`, naming the package, the chain to the one
// that does not build and the target, and not with the compiler's output.
func TestBuildWeb_AGoPackageThatDoesNotBuildForTheTargetIsRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds a Go-FFI wrapper; -short")
	}
	env := webFFIEnv(t)
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "go.mod"), "module nativeonly\n\n"+repoGoLine(t)+"\n")
	mustWrite(t, filepath.Join(dir, "nativeonly.go"), "package nativeonly\n\nimport \"nativeonly/native\"\n\n"+
		"func Answer() int64 {\n\treturn native.Answer()\n}\n")
	mustWrite(t, filepath.Join(dir, "native", "native.go"), "//go:build !js && !wasip1\n\npackage native\n\n"+
		"func Answer() int64 {\n\treturn 42\n}\n")
	entry := filepath.Join(dir, "main.nomi")
	mustWrite(t, entry, "import std/io\n\ngopkg \"nativeonly\"\n\nfn answer() -> Int go nativeonly.Answer\n\nfn main() {\n    io.print(answer())\n}\n")
	// It builds and runs natively.
	if r := runProcess(t, env, dir, "", nomiBin, "run", entry); r.exit != 0 || r.stdout != "42\n" {
		t.Fatalf("nomi run: exit %d\n%s", r.exit, r.transcript())
	}
	for _, c := range []struct {
		target string
		args   []string
	}{
		{"js/wasm", []string{"build", "--web", entry, "-o", filepath.Join(t.TempDir(), "web")}},
		{"js/wasm", []string{"build", "--target", "js/wasm", entry, "-o", filepath.Join(t.TempDir(), "app.wasm")}},
		{"wasip1/wasm", []string{"build", "--target", "wasip1/wasm", entry, "-o", filepath.Join(t.TempDir(), "app.wasm")}},
	} {
		got := runProcess(t, env, dir, "", nomiBin, c.args...)
		want := "nomi build: the project's Go bindings do not build for " + c.target + ":\n" +
			"  - main.nomi:3:8: Go package \"nativeonly\" does not build for " + c.target + ": it imports nativeonly/native " +
			"(nativeonly → nativeonly/native), which has no Go files for " + c.target + " (build constraints exclude every one)\n"
		if got.exit == 0 || got.stderr != want {
			t.Fatalf("nomi %v: exit %d, want\n%s\ngot:\n%s", c.args, got.exit, want, got.transcript())
		}
	}
}
