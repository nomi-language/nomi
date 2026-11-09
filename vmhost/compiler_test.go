package vmhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/stdcompilerrun"
)

// TestCompilerRun_NestedProgramIsNoFileOfTheProject: `compiler.run` from a
// test in a project whose nomi.toml lists entry_points runs a source with a
// `fn main`. The source is not one of the project's files, so entry_points
// does not place it, while its imports still resolve against the project
// root and the project files it imports are still held to the manifest.
func TestCompilerRun_NestedProgramIsNoFileOfTheProject(t *testing.T) {
	root := t.TempDir()
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("nomi.toml", "[module]\nname = \"demo\"\nentry_points = [\"app\"]\n")
	write("app.nomi", "fn main() {\n  _ = 1\n}\n")
	write("helper.nomi", "pub fn greet() -> String {\n  \"hi\"\n}\n")
	write("stray.nomi", "pub fn x() -> Int {\n  1\n}\n\nfn main() {\n  _ = x()\n}\n")

	t.Run("a source with fn main runs", func(t *testing.T) {
		src := "import std::io\n\nfn main() {\n  io.print(\"nested\")\n}\n"
		got, err := stdcompilerrun.RunSource(src, root, nil, nil)
		if err != nil || got != "nested\n" {
			t.Fatalf("compiler.run answers %q, %v; want \"nested\\n\"", got, err)
		}
	})
	t.Run("its project imports resolve against the root", func(t *testing.T) {
		src := "import helper\nimport std::io\n\nfn main() {\n  io.print(helper.greet())\n}\n"
		got, err := stdcompilerrun.RunSource(src, root, nil, nil)
		if err != nil || got != "hi\n" {
			t.Fatalf("compiler.run answers %q, %v; want \"hi\\n\"", got, err)
		}
	})
	t.Run("a project file it imports is still placed", func(t *testing.T) {
		src := "import stray\nimport std::io\n\nfn main() {\n  io.print(Debug.inspect(stray.x()))\n}\n"
		got, err := stdcompilerrun.RunSource(src, root, nil, nil)
		if err == nil {
			t.Fatalf("compiler.run ran %q; stray.nomi's fn main is not in entry_points", got)
		}
		if _, unrunnable := err.(*stdcompilerrun.Unrunnable); unrunnable {
			t.Fatalf("the shared front end accepted what the VM refused: %v", err)
		}
		if !strings.Contains(err.Error(), "entry callback in stray but file is not declared in nomi.toml entry_points") {
			t.Fatalf("error %q does not name stray's placement", err)
		}
	})
}
