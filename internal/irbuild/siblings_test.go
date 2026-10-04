package irbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stagedProgram writes a multi-file Nomi program into a temp directory and
// returns the entry's path.
//
// Not testdata/, and the reason is a property of testdata/ worth keeping: the
// LSP's workspace scan analyzes every `.nomi` in the repository except five
// named directories, and one of its tests asserts the whole workspace
// publishes NO diagnostics. Every file in testdata/ is a differential fixture
// — a program that runs both ways — so "testdata/ is diagnostic-free" is true
// today and worth not spending. A program the ANALYZER rejects, and a program
// carrying a deliberate unused import, both belong to a single test rather
// than to the fixture tree. The repo's blessing mechanism for a noisy fixture
// (`// expect type-error:` plus a path allowlist in lsp/diagnostics.go) exists
// and was deliberately not extended for these: widening it is an LSP change,
// and these programs need no file on disk to outlive the test.
func stagedProgram(t *testing.T, entry string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("staging %s: %v", name, err)
		}
	}
	return filepath.Join(dir, entry)
}

// Visibility. `pub` controls cross-file visibility, so a non-`pub` top-level
// declaration is FILE-PRIVATE — but every function the builder lowers links by
// symbol into one program, so nothing below the front end stops a sibling file
// from naming a private function.
//
// Both halves are asserted, and the reason the second one exists is that the
// first makes the builder's path UNREACHABLE for this program: the analyzer
// does not put a private declaration in the file's qualified member scope at
// all, so `helpers.secret(2)` fails analysis and Generate never runs. That is
// the front end rejecting it, not the builder being checked — and it is exactly
// the case an output comparison cannot cover, because a program that never
// runs has no recorded output to disagree with. So the
// builder's own answer for the declaration is asserted directly, against an
// index built from real analysis of a program that DOES pass.
func TestSiblingFile_PrivateDeclarationIsNotCallable(t *testing.T) {
	// `secret` carries no `pub` and sits beside a `pub` neighbour on purpose:
	// the file is importable and one of its two functions is reachable, so a
	// check that looked at the FILE rather than the DECLARATION would pass.
	private := stagedProgram(t, "main.nomi", map[string]string{
		"main.nomi": "import {\n  std/io\n  helpers\n}\n\nfn main() {\n  io.print(\"${helpers.secret(2)}\")\n}\n",
		"helpers.nomi": "pub fn public_one(x: Int): Int {\n  x + 1\n}\n\n" +
			"fn secret(x: Int): Int {\n  x * 100\n}\n",
	})
	if _, err := Analyze(private); err == nil {
		t.Fatal("the analyzer accepted a reference to a non-pub sibling declaration; " +
			"this test no longer pins what it was written for")
	} else if !strings.Contains(err.Error(), "has no member 'secret'") {
		t.Fatalf("analyzer rejected it for an unexpected reason: %v", err)
	}

	p, err := Analyze(fixture("siblings/main.nomi"))
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	x := buildFileIndex(p, buildTypeRegistry(p))
	var helpers *fileUnit
	for _, u := range x.units {
		if u.key == "helpers" {
			helpers = u
		}
	}
	if helpers == nil {
		t.Fatal("no unit for helpers.nomi")
	}
	pub := helpers.funcs["double"]
	if pub == nil || !pub.lowerable() {
		t.Fatalf("a pub sibling function must be callable, got %#v", pub)
	}
	priv := helpers.funcs["scaled"]
	if priv == nil {
		t.Fatal("a private sibling declaration must be FOUND, so a call site refuses " +
			"under the reason it was refused for rather than under 'no such function'")
	}
	if priv.lowerable() {
		t.Fatal("a non-pub sibling declaration is file-private and must not be callable across files")
	}
	if priv.why != "private sibling file function" {
		t.Fatalf("refusal name is the tally key and must be stable; got %q", priv.why)
	}
}
