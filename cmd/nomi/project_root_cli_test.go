package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunFile_ASubdirectoryFileResolvesFromMainNomi runs a file one directory
// below a file-mode project's main.nomi. Its import names a file beside
// main.nomi, which resolves only when the project root is main.nomi's
// directory (spec, "Project root discovery", step 2). The LSP resolves the
// same file from the same root, through the same analysis.ProjectRoot.
func TestRunFile_ASubdirectoryFileResolvesFromMainNomi(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"main.nomi":     "fn main() {\n}\n",
		"greeting.nomi": "pub fn text(): String {\n  \"hello from the project root\"\n}\n",
		"tools/say.nomi": "import {\n  std/io\n  greeting\n}\n\n" +
			"fn main() {\n  io.print(greeting.text())\n}\n",
	}
	for rel, src := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command(nomiBin, "run", filepath.Join("tools", "say.nomi"))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NOMI_FFIRUN_CACHE_ROOT="+t.TempDir(), "NO_COLOR=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("nomi run tools/say.nomi: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "hello from the project root" {
		t.Fatalf("nomi run tools/say.nomi printed %q, want the greeting from ../greeting.nomi", got)
	}
}
