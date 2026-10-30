package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// A test file's std/fs calls take a relative path under its module root,
// the directory of the nearest nomi.toml, wherever `nomi test` runs: from
// the module root, from above it, and on one file of a subdirectory.
func TestTestCommand_RelativePathsAreUnderTheModuleRoot(t *testing.T) {
	outer := t.TempDir()
	root := filepath.Join(outer, "app")
	mustWrite(t, filepath.Join(root, "nomi.toml"), "[module]\nname = \"app\"\n")
	mustWrite(t, filepath.Join(root, "data", "greeting.txt"), "hello\n")
	test := `import std/fs

test "a relative path is under the module root" {
    assert fs.read_text("data/greeting.txt") == Ok("hello\n")
    assert Result.map(fs.current_dir(), fn d -> String.ends_with(d, "/app")) == Ok(True)
}
`
	mustWrite(t, filepath.Join(root, "greeting_test.nomi"), test)
	mustWrite(t, filepath.Join(root, "sub", "deeper_test.nomi"), test)
	for _, run := range []struct {
		dir  string
		args []string
	}{
		{root, []string{"test", "."}},
		{outer, []string{"test", "app"}},
		{filepath.Join(root, "sub"), []string{"test", "deeper_test.nomi"}},
	} {
		out, err := runNomiInDir(t, t.TempDir(), run.dir, "", run.args...)
		if err != nil || !strings.Contains(out, "0 failed") {
			t.Errorf("nomi %v in %s: %v\n%s", run.args, run.dir, err, out)
		}
	}
}
