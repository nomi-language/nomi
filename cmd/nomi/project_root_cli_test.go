package main

import (
	"fmt"
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
		"greeting.nomi": "pub fn text() -> String {\n  \"hello from the project root\"\n}\n",
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

// A file outside any project is its own module, rooted at its directory
// (spec, "Project root discovery"), so the orphan rule holds it as it holds
// a project's files: `impl Debug for Bool` and `impl Debug for List<Int>`
// implement a std interface for std types, and `nomi check` and `nomi run`
// both refuse them rather than run the program. An impl of the file's own
// interface for a std type is no orphan.
func TestLoneFile_TheOrphanRuleHoldsIt(t *testing.T) {
	dir := t.TempDir()
	orphans := filepath.Join(dir, "orphans.nomi")
	mustWrite(t, orphans, `import std/io

impl Debug for Bool {
    fn inspect(_b: Bool) -> String {
        "mine"
    }
}

impl Debug for List<Int> {
    fn inspect(_xs: List<Int>) -> String {
        "mine"
    }
}

fn main() {
    io.print(Debug.inspect(True))
    io.inspect([1, 2])
}
`)
	for _, sub := range []string{"check", "run"} {
		cmd := exec.Command(nomiBin, sub, orphans)
		cmd.Env = append(os.Environ(), "NOMI_FFIRUN_CACHE_ROOT="+t.TempDir(), "NO_COLOR=1")
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("nomi %s accepted orphan impls in a file outside any project:\n%s", sub, out)
		}
		text := string(out)
		if strings.Count(text, "error: orphan impl: `Debug`") != 2 || strings.Contains(text, "mine\n") {
			t.Fatalf("nomi %s: want an orphan impl error for each impl and no run, got:\n%s", sub, text)
		}
		if !strings.Contains(text, fmt.Sprintf("cannot be implemented in module %q", dir)) {
			t.Fatalf("nomi %s: want the file's directory named as its module, got:\n%s", sub, text)
		}
	}

	own := filepath.Join(t.TempDir(), "own.nomi")
	mustWrite(t, own, `import std/io

interface Show {
    fn show(value: self) -> String
}

impl Show for Bool {
    fn show(_b: Bool) -> String {
        "mine"
    }
}

fn main() {
    io.print(Show.show(True))
}
`)
	cmd := exec.Command(nomiBin, "run", own)
	cmd.Env = append(os.Environ(), "NOMI_FFIRUN_CACHE_ROOT="+t.TempDir(), "NO_COLOR=1")
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "mine\n" {
		t.Fatalf("nomi run of an impl of the file's own interface: %v\n%s", err, out)
	}
}
