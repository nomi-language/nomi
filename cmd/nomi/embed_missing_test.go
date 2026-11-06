package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// missingEmbedProgram embeds a frontend's build folder, as a program that
// serves a Vite build does, and reads it through `Files.sub` and a literal
// path under it.
const missingEmbedProgram = `import {
  std::embed.Files
  std::io
}

embed web ` + "`web/dist`" + `

fn main() {
  io.print(Files.sub(web, "web/dist")! |> Files.paths())
  io.print(Files.read_text(web, "web/dist/index.html"))
}
`

const missingEmbedWarning = "warning: the `embed` pattern `web/dist` matches no file: `web/dist` does not exist, so `web` is empty until it does"

// Until `web/dist` exists, `nomi run` and `nomi check` compile the program
// with an empty tree and a warning at the pattern, and the program's read
// is a NotFound; `nomi build` refuses it. Nothing is kept while the warning
// stands, and once the folder appears the next run sees its files and no
// warning, and so does a build.
func TestEmbedMissingDirectory_RunAndCheckWarnBuildRefuses(t *testing.T) {
	dir := t.TempDir()
	cache := t.TempDir()
	mustWrite(t, filepath.Join(dir, "nomi.toml"), "[module]\nname = \"app\"\nentry_points = [\"main\"]\n")
	entry := filepath.Join(dir, "main.nomi")
	mustWrite(t, entry, missingEmbedProgram)

	run := runNomiIn(t, dir, cache, "run", entry)
	if run.exit != 0 || run.stdout != "[]\nErr(web/dist/index.html: not found)\n" {
		t.Fatalf("nomi run with no web/dist: exit %d\nstdout:\n%s\nstderr:\n%s", run.exit, run.stdout, run.stderr)
	}
	if !strings.Contains(run.stderr, missingEmbedWarning) || strings.Contains(run.stderr, "error:") {
		t.Fatalf("nomi run with no web/dist: want the warning and no error, stderr:\n%s", run.stderr)
	}
	if run.kept() {
		t.Fatalf("a program with a warning was kept: %v", run.report)
	}

	check := runNomiIn(t, dir, cache, "check", entry)
	if check.exit != 0 || !strings.Contains(check.stderr, missingEmbedWarning) {
		t.Fatalf("nomi check with no web/dist: exit %d\nstdout:\n%s\nstderr:\n%s", check.exit, check.stdout, check.stderr)
	}

	jsonCheck := runNomiIn(t, dir, cache, "check", "--format", "json", entry)
	if jsonCheck.exit != 0 || !strings.Contains(jsonCheck.stdout, `"severity":"warning","code":"embed-missing"`) ||
		!strings.Contains(jsonCheck.stdout, `"status":"ok"`) || strings.Contains(jsonCheck.stderr, "warning") {
		t.Fatalf("nomi check --format json with no web/dist: exit %d\nstdout:\n%s\nstderr:\n%s", jsonCheck.exit, jsonCheck.stdout, jsonCheck.stderr)
	}

	out := t.TempDir()
	build := runNomiIn(t, dir, cache, "build", entry, "-o", exe(filepath.Join(out, "app")))
	if build.exit == 0 {
		t.Fatalf("nomi build admits an `embed` whose folder does not exist\nstderr:\n%s", build.stderr)
	}
	if !strings.Contains(build.stderr, "error: the `embed` pattern `web/dist` matches no file: `web/dist` does not exist") ||
		strings.Contains(build.stderr, "warning:") {
		t.Fatalf("nomi build: want the error at the pattern, stderr:\n%s", build.stderr)
	}
	if _, err := os.Stat(exe(filepath.Join(out, "app"))); err == nil {
		t.Fatal("nomi build wrote an executable it refused")
	}

	// The folder appears, as `npm run build` writes it.
	mustWrite(t, filepath.Join(dir, "web", "dist", "index.html"), "<div id=root></div>")
	after := runNomiIn(t, dir, cache, "run", entry)
	if after.exit != 0 || after.stdout != "[index.html]\nOk(<div id=root></div>)\n" || after.stderr != "" || after.read() {
		t.Fatalf("nomi run once web/dist exists: exit %d, read a kept program %t\nstdout:\n%s\nstderr:\n%s\ncache: %v",
			after.exit, after.read(), after.stdout, after.stderr, after.report)
	}
	if b := runNomiIn(t, dir, cache, "build", entry, "-o", exe(filepath.Join(out, "app"))); b.exit != 0 {
		t.Fatalf("nomi build once web/dist exists: exit %d\nstderr:\n%s", b.exit, b.stderr)
	}
}

// A pattern that matches nothing in a folder that exists stays an error in
// `nomi run`: only a pattern whose base does not exist is a warning.
func TestEmbedMissingDirectory_AGlobInAnExistingFolderStaysAnError(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "nomi.toml"), "[module]\nname = \"app\"\nentry_points = [\"main\"]\n")
	mustWrite(t, filepath.Join(dir, "web", "dist", "index.html"), "x")
	entry := filepath.Join(dir, "main.nomi")
	mustWrite(t, entry, strings.Replace(missingEmbedProgram, "`web/dist`", "`web/dist/*.css`", 1))
	run := runNomiIn(t, dir, "off", "run", entry)
	if run.exit == 0 {
		t.Fatalf("nomi run admits a glob that matches nothing in an existing folder\nstdout:\n%s", run.stdout)
	}
	if !strings.Contains(run.stderr, "error: the `embed` pattern `web/dist/*.css` matches no file") {
		t.Fatalf("want the pattern's error, stderr:\n%s", run.stderr)
	}
}
