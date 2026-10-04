package irbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Program boot staging. Without it, the app-field reads in
// `15-app-and-defer/effects/main.nomi` would read a zero value where the
// program answers real config (`deployment=dev port=3000 ...` against
// `deployment=  port=0 ...`). That failure is a wrong answer, so the check is
// absolute text: the golden record holds the printed values, and a change
// that zeroed the cell fails there.

// TestBootProgram_TheFrontEndPinsBootToTheEntryWithMain asserts the premise
// that makes entry-boot staging unconditional.
//
// The builder lowers a unit's top-level `fn boot` as a boot whenever that unit
// declares `fn main` (appfield.go's isProgramBoot). That is sound only because
// the front end rejects a top-level `fn boot` in a file with no `fn main`:
//
//	`boot` belongs in an entry file, one that defines `fn main`
//
// If that rule were relaxed, a boot in a library file would be an ordinary
// function to the builder and an application root to the checker.
//
// Three readings, and the third is what keeps the first two from being vacuous:
// each bad shape must be a FRONT-END error, and the good shape must analyze
// clean. Without the positive control, a fixture that stopped parsing for any
// unrelated reason would pass both negatives.
func TestBootProgram_TheFrontEndPinsBootToTheEntryWithMain(t *testing.T) {
	const decls = `

pub struct Cfg {
  context: Context

  name: String = "dev"
}

fn boot(): Cfg {
  Cfg{context: Context.root(), }
}
`
	// THE POSITIVE CONTROL, first: boot plus main in the entry analyzes clean.
	// If this fails, the two negatives below measure nothing.
	if _, err := AnalyzeSource("bcbootmain", decls+"\nfn main() {\n  _ = Cfg.name\n}\n"); err != nil {
		t.Fatalf("`fn boot` beside `fn main` in the entry file does not analyze, so the negatives "+
			"below establish nothing about the checker rule: %v", err)
	}
	// NEGATIVE 1: a `boot` with no `main` in the entry file.
	_, err := AnalyzeSource("bcbootnomain", decls)
	if err == nil {
		t.Error("a program with `fn boot` and no `fn main` ANALYZES CLEAN. " +
			"The builder stages every `fn boot` it sees on the strength of the front end " +
			"rejecting this, so such a program reaches irbuild and gets an app value no " +
			"`fn main` asks for. Program boot " +
			"staging needs its `fn main` gate back (see appfield.go's bootDecl)")
	} else if !strings.Contains(err.Error(), "`boot` belongs in an entry file, one that defines `fn main`") {
		// A DIFFERENT error is not a pass: the fixture may have stopped
		// exercising the rule for an unrelated reason, which is the silent
		// false reading this whole file is written against.
		t.Errorf("a program with `fn boot` and no `fn main` was rejected for a different reason, "+
			"so this reading is not about the boot rule: %v", err)
	}
	// NEGATIVE 2: a `boot` in an imported file with no `main`. Spelled as a
	// two-file project, because "an imported file" is not expressible in one
	// source and a single-source fixture would silently check negative 1 twice.
	dir := t.TempDir()
	write := func(name, src string) {
		if werr := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); werr != nil {
			t.Fatal(werr)
		}
	}
	write("cfg.nomi", decls)
	write("main.nomi", "import cfg.Cfg\n\nfn main() {\n  _ = Cfg{context: Context.root()}\n}\n")
	_, err = Analyze(filepath.Join(dir, "main.nomi"))
	if err == nil {
		t.Error("a `fn boot` declared in an imported file with no `fn main` analyzes clean. " +
			"The builder lowers such a boot as an ordinary function. See appfield.go's " +
			"isProgramBoot")
	} else if !strings.Contains(err.Error(), "`boot` belongs in an entry file, one that defines `fn main`") {
		t.Errorf("a `fn boot` outside the entry file was rejected for a different reason, so this "+
			"reading is not about the boot rule: %v", err)
	}
}
