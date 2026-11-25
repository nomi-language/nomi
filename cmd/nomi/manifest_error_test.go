package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestManifest_AManifestInErrorIsTheOneError: a project whose nomi.toml
// cannot be read as a manifest, whose files import each other by the
// package's name, gets from `nomi check`, `run`, `test` and `build` that one
// error, with its code, and none of the errors that would follow from the
// package's name being unknown. A `dir` dependency whose nomi.toml is not
// TOML gives the file that imports it one error the same way. With a good
// nomi.toml the project checks.
func TestManifest_AManifestInErrorIsTheOneError(t *testing.T) {
	c := newModuleCLI(t)
	work := t.TempDir()
	lox := filepath.Join(work, "lox")
	mustWrite(t, filepath.Join(lox, "main.nomi"), "import\n  std::io\n  lox::interpreter\n  lox::value.Value\nend\n\nfn main()\n  v: Value = interpreter.run(\"1\")\n  io.print(v.text)\nend\n")
	mustWrite(t, filepath.Join(lox, "interpreter.nomi"), "import lox::value.Value\n\npub fn run(src: String) -> Value\n  Value{text: src}\nend\n\ntest \"run\"\n  assert run(\"1\").text == \"1\"\nend\n")
	mustWrite(t, filepath.Join(lox, "value.nomi"), "pub struct Value\n  text: String\nend\n")

	mustWrite(t, filepath.Join(lox, "nomi.toml"), "[package]\nname = \"lox\"\nentry_points = [\"main\"]\n")
	if out := c.must(lox, "run", "main.nomi"); out != "1" {
		t.Fatalf("with a good nomi.toml, nomi run printed %q, want 1", out)
	}

	for _, m := range []struct{ name, manifest string }{
		{"a misspelled table", "[module]\nname = \"lox\"\nentry_points = [\"main\"]\n"},
		{"a syntax error", "[package\nname = \"lox\"\n"},
		{"no name", "[package]\nentry_points = [\"main\"]\n"},
	} {
		mustWrite(t, filepath.Join(lox, "nomi.toml"), m.manifest)
		out, _, err := c.run(lox, "check", "--format", "json", "main.nomi")
		if err == nil || strings.Count(out, `"type":"diagnostic"`) != 1 || !strings.Contains(out, `"code":"manifest"`) {
			t.Errorf("%s: nomi check: %v\n%s\nwant one diagnostic, with code manifest", m.name, err, out)
		}
		for _, args := range [][]string{
			{"run", "main.nomi"},
			{"test", "interpreter.nomi"},
			{"build", "-o", filepath.Join(work, "out"), "main.nomi"},
		} {
			stdout, stderr, err := c.run(lox, args...)
			if err == nil || strings.Count(stdout+stderr, "error:") != 1 {
				t.Errorf("%s: nomi %s: %v\n%s\n%s\nwant one error", m.name, strings.Join(args, " "), err, stdout, stderr)
			}
		}
	}

	app, dep := filepath.Join(work, "app"), filepath.Join(work, "dep")
	mustWrite(t, filepath.Join(app, "nomi.toml"), "[package]\nname = \"app\"\nentry_points = [\"main\"]\n\n[dependencies]\ndep = { dir = \"../dep\" }\n")
	mustWrite(t, filepath.Join(app, "main.nomi"), "import\n  std::io\n  dep::util\nend\n\nfn main()\n  io.print(util.one())\nend\n")
	mustWrite(t, filepath.Join(dep, "nomi.toml"), "[package\nname = \"dep\"\n")
	mustWrite(t, filepath.Join(dep, "util.nomi"), "pub fn one() -> String\n  \"1\"\nend\n")
	out, _, err := c.run(app, "check", "--format", "json", "main.nomi")
	if err == nil || strings.Count(out, `"type":"diagnostic"`) != 1 || !strings.Contains(out, `"code":"manifest"`) {
		t.Errorf("a dependency's nomi.toml in error: nomi check: %v\n%s\nwant one diagnostic, with code manifest", err, out)
	}
}
