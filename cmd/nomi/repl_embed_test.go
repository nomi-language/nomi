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

// An `embed` of a folder that does not exist yet is a warning at the input
// that declares it, and its tree is empty.
func TestReplVM_AnEmbedOfAMissingFolderWarns(t *testing.T) {
	t.Chdir(t.TempDir())
	replExpect(t, "import std::embed.Files\nembed notes `notes`\nFiles.paths(notes)\n", "[]\n",
		"warning: the `embed` pattern `notes` matches no file: `notes` does not exist, so `notes` is empty until it does")
}
