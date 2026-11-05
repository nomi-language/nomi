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
	test := `import std::fs

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

// A partner test in a subdirectory calls its file's private functions
// through the module name, one with a default left out included, whether
// `nomi test` runs on the directory or on the test file alone, which is
// then the entry and keyed by its name rather than its path.
func TestTestCommand_ANestedPartnerTestCallsPrivateFunctions(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "nomi.toml"), "[module]\nname = \"app\"\n")
	mustWrite(t, filepath.Join(root, "views", "post.nomi"), `fn classes(n: Int, prefix: String = "c") -> String {
    "${prefix}${n}"
}
`)
	mustWrite(t, filepath.Join(root, "views", "post_test.nomi"), `import views::post

test "a partner test calls a private function" {
    assert post.classes(1) == "c1"
    assert post.classes(2, prefix: "x") == "x2"
}
`)
	for _, args := range [][]string{
		{"test", "."},
		{"test", "views/post_test.nomi"},
		{"check", "views/post_test.nomi"},
	} {
		out, err := runNomiInDir(t, t.TempDir(), root, "", args...)
		if err != nil || strings.Contains(out, "BLOCKED") || strings.Contains(out, "not supported") {
			t.Errorf("nomi %v: %v\n%s", args, err, out)
		}
	}
}
