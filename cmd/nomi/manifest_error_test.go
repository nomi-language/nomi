package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// TestManifest_AManifestInErrorIsTheOneError: a project whose nomi.toml
// cannot be read as a manifest, whose files import each other by the
// package's name, gets from `nomi check`, `run`, `test` and `build` that one
// error, with its code, and none of the errors that would follow from the
// package's name being unknown. The error is at its line in the file it is
// in, the project's nomi.toml, the nomi.toml of a `dir` dependency or the
// workspace's nomi-workspace.toml, in the text output, with that line as its
// snippet, and in the JSON output, as an error in a file other than the
// entry is. With a good nomi.toml the project checks.
func TestManifest_AManifestInErrorIsTheOneError(t *testing.T) {
	c := newModuleCLI(t)
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lox := filepath.Join(work, "lox")
	mustWrite(t, filepath.Join(lox, "main.nomi"), "import\n  std::io\n  lox::interpreter\n  lox::value.Value\nend\n\nfn main()\n  v: Value = interpreter.run(\"1\")\n  io.print(v.text)\nend\n")
	mustWrite(t, filepath.Join(lox, "interpreter.nomi"), "import lox::value.Value\n\npub fn run(src: String) -> Value\n  Value{text: src}\nend\n\ntest \"run\"\n  assert run(\"1\").text == \"1\"\nend\n")
	mustWrite(t, filepath.Join(lox, "value.nomi"), "pub struct Value\n  text: String\nend\n")

	mustWrite(t, filepath.Join(lox, "nomi.toml"), "[package]\nname = \"lox\"\nentry_points = [\"main\"]\n")
	if out := c.must(lox, "run", "main.nomi"); out != "1" {
		t.Fatalf("with a good nomi.toml, nomi run printed %q, want 1", out)
	}

	// checkPlaced runs `nomi check` in dir on main.nomi, as text and as
	// JSON, and wants one error, with code manifest, at line of the file
	// at path, which the text shows as written (shown) with that line as
	// its snippet.
	checkPlaced := func(name, dir, path, shown string, line int) {
		t.Helper()
		lines := strings.Split(mustRead(t, path), "\n")
		stdout, stderr, err := c.run(dir, "check", "main.nomi")
		text := stdout + stderr
		for _, want := range []string{fmt.Sprintf(" --> %s:%d:", shown, line), fmt.Sprintf("%d | %s", line, lines[line-1])} {
			if err == nil || strings.Count(text, "error:") != 1 || !strings.Contains(text, want) {
				t.Errorf("%s: nomi check: %v\n%s\nwant one error, with %q", name, err, text, want)
			}
		}
		out, _, err := c.run(dir, "check", "--format", "json", "main.nomi")
		var diags []map[string]any
		for _, l := range strings.Split(out, "\n") {
			var m map[string]any
			if json.Unmarshal([]byte(l), &m) == nil && m["type"] == "diagnostic" {
				diags = append(diags, m)
			}
		}
		if err == nil || len(diags) != 1 || diags[0]["code"] != "manifest" || diags[0]["file"] != path || diags[0]["line"] != float64(line) {
			t.Errorf("%s: nomi check --format json: %v\n%s\nwant one diagnostic, with code manifest, in %s at line %d", name, err, out, path, line)
		}
	}

	for _, m := range []struct {
		name, manifest string
		line           int
	}{
		{"a misspelled table", "[module]\nname = \"lox\"\nentry_points = [\"main\"]\n", 1},
		{"a syntax error", "[package\nname = \"lox\"\n", 2},
		{"no name", "[package]\nentry_points = [\"main\"]\n", 1},
		{"a misspelled key", "[package]\nname = \"lox\"\nentry_point = [\"main\"]\n", 3},
	} {
		path := filepath.Join(lox, "nomi.toml")
		mustWrite(t, path, m.manifest)
		checkPlaced(m.name, lox, path, "nomi.toml", m.line)
		for _, args := range [][]string{
			{"run", "main.nomi"},
			{"test", "interpreter.nomi"},
			{"build", "-o", filepath.Join(work, "out"), "main.nomi"},
		} {
			stdout, stderr, err := c.run(lox, args...)
			if err == nil || strings.Count(stdout+stderr, "error:") != 1 || !strings.Contains(stdout+stderr, fmt.Sprintf(" --> nomi.toml:%d:", m.line)) {
				t.Errorf("%s: nomi %s: %v\n%s\n%s\nwant one error, at nomi.toml:%d", m.name, strings.Join(args, " "), err, stdout, stderr, m.line)
			}
		}
	}

	app, dep := filepath.Join(work, "app"), filepath.Join(work, "dep")
	mustWrite(t, filepath.Join(app, "nomi.toml"), "[package]\nname = \"app\"\nentry_points = [\"main\"]\n\n[dependencies]\ndep = { dir = \"../dep\" }\n")
	mustWrite(t, filepath.Join(app, "main.nomi"), "import\n  std::io\n  dep::util\nend\n\nfn main()\n  io.print(util.one())\nend\n")
	mustWrite(t, filepath.Join(dep, "nomi.toml"), "[package]\nname = \"dep\"\nbogus = 1\n")
	mustWrite(t, filepath.Join(dep, "util.nomi"), "pub fn one() -> String\n  \"1\"\nend\n")
	depToml := filepath.Join(dep, "nomi.toml")
	checkPlaced("a dependency's nomi.toml in error", app, depToml, depToml, 3)
	// The entry that names the package is a note, at its line.
	_, stderr, _ := c.run(app, "check", "main.nomi")
	if want := "= note: [dependencies] dep of " + filepath.Join(app, "nomi.toml") + " names this package: nomi.toml:6:1"; !strings.Contains(stderr, want) {
		t.Errorf("a dependency's nomi.toml in error: nomi check printed\n%s\nwant the note %q", stderr, want)
	}

	ws := filepath.Join(work, "ws")
	wsToml := filepath.Join(ws, "nomi-workspace.toml")
	mustWrite(t, wsToml, "members = [\"shop\"]\nbogus = 1\n")
	mustWrite(t, filepath.Join(ws, "shop", "nomi.toml"), "[package]\nname = \"shop\"\nentry_points = [\"main\"]\n")
	mustWrite(t, filepath.Join(ws, "shop", "main.nomi"), "import std::io\n\nfn main()\n  io.print(\"1\")\nend\n")
	checkPlaced("a workspace file in error", filepath.Join(ws, "shop"), wsToml, wsToml, 2)
}
