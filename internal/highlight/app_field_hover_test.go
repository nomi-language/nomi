package highlight_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/highlight"
	"github.com/nomi-language/nomi/internal/hoverdoc"
	"github.com/nomi-language/nomi/std"
)

// Tour and editor hover resolve application-field reads to their declared field types.
const appFieldHoverSrc = `import {
  std/io
}

struct Cfg {
  context: Context

  tag: String
}

fn boot(): Cfg {
  Cfg{context: Context.root(), tag: "hi"}
}

fn main() {
  show("label")
}

fn show(label: String) {
  io.print("${label}: ${Cfg.tag}")
}
`

// appFieldCol returns the 1-based (line, col) of the idx'th (1-based)
// occurrence of needle in src. It fails the test when the needle is absent, so
// a source edit cannot silently turn a position assertion into a probe of the
// wrong column.
func appFieldCol(t *testing.T, src, needle string, idx int) analysis.Pos {
	t.Helper()
	seen := 0
	for i, line := range strings.Split(src, "\n") {
		from := 0
		for {
			at := strings.Index(line[from:], needle)
			if at < 0 {
				break
			}
			seen++
			col := from + at + 1
			from = from + at + len(needle)
			if seen == idx {
				return analysis.Pos{Line: i + 1, Col: col}
			}
		}
	}
	t.Fatalf("occurrence %d of %q not found in source", idx, needle)
	return analysis.Pos{}
}

// lspAnalysisFor runs the SAME source through the LSP's own per-document
// analysis, so the assertions below compare the tour against the editor
// instead of against a hardcoded string that could drift from both.
func lspAnalysisFor(t *testing.T, lib *std.StdLib, src string) *analysis.FileAnalysis {
	t.Helper()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
	doc := dm.Open("file:///app_field_hover.nomi", src)
	if doc == nil || doc.Analysis == nil {
		t.Fatal("the LSP document manager produced no analysis for the fixture")
	}
	return doc.Analysis
}

func TestAnalyze_AppFieldReadHovers(t *testing.T) {
	lib := std.Load()
	fa := highlight.Analyze(appFieldHoverSrc, lib)
	if fa == nil {
		t.Fatal("highlight.Analyze returned nil")
	}

	// `Cfg.tag` records the field's reference at the field name.
	tagPos := appFieldCol(t, appFieldHoverSrc, "Cfg.tag", 1)
	tagPos.Col += len("Cfg.")
	// THE CONTROL, and the reason this test is worth having. `label` is an
	// ordinary parameter read on the SAME LINE as `Cfg.tag`. It hovers
	// whether or not the app machinery works, so if a future position error
	// walks the probe off the line the control fails too and the `Cfg.tag`
	// assertion cannot pass vacuously by finding nothing where nothing is
	// expected.
	labelPos := appFieldCol(t, appFieldHoverSrc, "${label}", 1)
	labelPos.Col += len("${")

	if sym := fa.SymbolAt(labelPos); sym == nil {
		t.Fatalf("CONTROL FAILED: no symbol at the `label` read %v; the probe is measuring nothing and the `Cfg.tag` result below is worthless", labelPos)
	}
	if got := hoverdoc.At(fa, labelPos); got != "```nomi\nlabel: String\n```" {
		t.Fatalf("CONTROL FAILED: hover on `label` = %q, want %q; the probe is off-position", got, "```nomi\nlabel: String\n```")
	}

	if sym := fa.SymbolAt(tagPos); sym == nil {
		t.Fatalf("no symbol at the `Cfg.tag` read %v, while the control on the same line resolved", tagPos)
	}
	got := hoverdoc.At(fa, tagPos)
	if want := "```nomi\ntag: String\n```\n\n*app field of* `Cfg`"; got != want {
		t.Errorf("hover on `Cfg.tag` = %q, want %q", got, want)
	}
	// The type name in the read hovers as the type's declaration does.
	ownerPos := appFieldCol(t, appFieldHoverSrc, "Cfg.tag", 1)
	cfgPos := appFieldCol(t, appFieldHoverSrc, "): Cfg", 1)
	cfgPos.Col += len("): ")
	wantCfg := "```nomi\nstruct Cfg {\n    context: Context\n    tag: String\n}\n```"
	if typeName := hoverdoc.At(fa, cfgPos); typeName != wantCfg {
		t.Fatalf("CONTROL: hover on the type name `Cfg` = %q, want %q", typeName, wantCfg)
	}
	if owner := hoverdoc.At(fa, ownerPos); owner != wantCfg {
		t.Errorf("hover on the `Cfg` of `Cfg.tag` = %q, want the type name's hover %q", owner, wantCfg)
	}

	// AND IT MATCHES THE EDITOR. `hoverdoc.At`'s contract is that the tour
	// shows what Zed shows; asserting the string alone would let the two
	// drift apart while both stayed non-empty.
	lspFA := lspAnalysisFor(t, lib, appFieldHoverSrc)
	for _, c := range []struct {
		what string
		pos  analysis.Pos
	}{
		{"Cfg.tag", tagPos},
		{"Cfg of Cfg.tag", ownerPos},
		{"label (control)", labelPos},
	} {
		tour := hoverdoc.At(fa, c.pos)
		editor := hoverdoc.At(lspFA, c.pos)
		if editor == "" {
			t.Fatalf("the LSP itself hovers nothing at %s %v; the fixture, not the tour, is wrong", c.what, c.pos)
		}
		if tour != editor {
			t.Errorf("hover at %s diverges from the LSP:\n tour:   %q\n editor: %q", c.what, tour, editor)
		}
	}
}

// A read in a helper that a program and a `tests` group both run names its
// type, so it hovers as that type's field whichever boot runs it.
const twoBootHoverSrc = `import std/io

struct App {
  logger: String
  context: Context
}

fn boot(): App {
  App{logger: "app", context: Context.root()}
}

fn label(): String {
  App.logger
}

fn main() {
  io.print(label())
}

tests "labels" {
  boot boot()

  test "reads the logger" {
    assert label() == "app"
  }
}
`

func TestAnalyze_AppFieldReadUnderTwoBootsHovers(t *testing.T) {
	lib := std.Load()
	fa := highlight.Analyze(twoBootHoverSrc, lib)
	if fa == nil {
		t.Fatal("highlight.Analyze returned nil")
	}
	name := appFieldCol(t, twoBootHoverSrc, "App.logger", 1)
	name.Col += len("App.")
	if got, want := hoverdoc.At(fa, name), "```nomi\nlogger: String\n```\n\n*app field of* `App`"; got != want {
		t.Errorf("tour hover on `App.logger` = %q, want %q", got, want)
	}
	lspFA := lspAnalysisFor(t, lib, twoBootHoverSrc)
	if tour, editor := hoverdoc.At(fa, name), hoverdoc.At(lspFA, name); tour != editor {
		t.Errorf("hover diverges from the LSP:\n tour:   %q\n editor: %q", tour, editor)
	}
}
