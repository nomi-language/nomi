package main

import (
	"path/filepath"
	"testing"
)

// An `embed` typed at the REPL reads its files from the working directory,
// and a later input reads the value.
func TestReplVM_AnEmbedReadsTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "notes", "a.txt"), "alpha\n")
	t.Chdir(dir)
	replExpect(t, "import std::embed.Files\nembed notes `notes`\nFiles.paths(notes)\nFiles.read_text(notes, \"notes/a.txt\")\n",
		"[\"notes/a.txt\"]\nOk(\"alpha\\n\")\n")
}
