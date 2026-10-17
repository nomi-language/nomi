package analysis_test

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/projectgen"
)

// faultProject is a project whose entry can demand an impl no file
// supplies: fault declares its own `Error`, with no Equatable impl, which
// a bare-name lookup would borrow from the stdlib's `Error`s. Only the
// coherence check over every file's demands (FinalizeCoherence) reports it.
func faultProject() incrementalProject {
	return incrementalProject{name: "fault", entry: "main", texts: map[string]string{
		"main": `import {
    std/io
    fault
}

fn main() {
    io.print("start")
}
`,
		"fault": `pub enum Error {
    Bad String
}

pub fn mk(s: String) -> Result<Int, Error> {
    Err(Error.Bad(s))
}
`,
	}}
}

// faultEdits add the unsupplied demand in a body and take it away.
var faultEdits = []editStep{
	replace("body only, an unsupplied demand added", "main", `io.print("start")`, `io.print("equal?: ${Equatable.equal?(fault.mk("a"), fault.mk("b"))}")`),
	restore("body only, the demand removed", "main"),
}

// coherenceChecked is recheck, failing t after each step when a program's
// kept coherence answers differ from MissingImpls worked out from scratch.
func coherenceChecked(t *testing.T, recheck recheckFunc) recheckFunc {
	return func(prev *analysis.CheckedProgram, edit analysis.ProgramEdit) (*analysis.CheckedProgram, analysis.RecheckOutcome) {
		next, outcome := recheck(prev, edit)
		requireKeptCoherence(t, next, fmt.Sprintf("after an edit to %s (recheck %s)", edit.Key, outcome.Path))
		return next, outcome
	}
}

// requireKeptCoherence fails t when p's kept coherence answers differ from
// MissingImpls worked out from scratch, and answers how many diagnostics
// the two views report.
func requireKeptCoherence(t *testing.T, p *analysis.CheckedProgram, when string) int {
	t.Helper()
	kept, fresh, keptEntry, freshEntry := analysis.KeptCoherenceForTest(p)
	if !reflect.DeepEqual(kept, fresh) {
		t.Errorf("%s: the kept coherence check answers %v, MissingImpls over every file %v", when, kept, fresh)
	}
	if !reflect.DeepEqual(keptEntry, freshEntry) {
		t.Errorf("%s: the entry document's kept coherence check answers %v, MissingImpls %v", when, keptEntry, freshEntry)
	}
	keptUnused, freshUnused := analysis.KeptImplImportsForTest(p)
	if !reflect.DeepEqual(keptUnused, freshUnused) {
		t.Errorf("%s: the kept whole-file import check answers %v, CheckImplImports %v", when, keptUnused, freshUnused)
	}
	return len(fresh) + len(freshEntry) + len(freshUnused)
}

// TestKeptCoherenceMatchesMissingImpls: the coherence checks a program keeps
// file by file (FinalizeCoherence, and the language server's entry
// document's) answer what MissingImpls worked out from scratch answers, on
// a full check and after every edit of the incremental harness's
// sequences, diagnostics included.
func TestKeptCoherenceMatchesMissingImpls(t *testing.T) {
	dm := newManager()
	recheck := coherenceChecked(t, dm.RecheckWithOutcome)
	fault := faultProject()
	reported := 0
	for i, s := range faultEdits {
		text, _ := s.change(fault.texts[s.key], fault.texts[s.key])
		if i == 0 {
			texts := map[string]string{"fault": fault.texts["fault"], s.key: text}
			reported += requireKeptCoherence(t, fullCheck(fault, texts), "a full check with the unsupplied demand")
		}
	}
	if reported == 0 {
		t.Fatal("the fault project draws no missing impl: the comparison compares nothing")
	}
	runChecked(t, fault, faultEdits, recheck)
	runChecked(t, layeredProject(), layeredEdits, recheck)
	order := orderIncremental()
	runChecked(t, order, orderEdits, recheck)
	gen := projectgen.Generate(projectgen.Config{Seed: 1, Lines: 1000})
	genProject := incrementalProject{name: "generated", entry: gen.Entry, texts: gen.ProgramTexts()}
	for _, kind := range []projectgen.EditKind{projectgen.BodyLeaf, projectgen.BodyHub, projectgen.BodyEntry, projectgen.SignatureLeaf} {
		key, text := gen.Edit(kind, 0)
		if diff, _ := runEdits(t, genProject, []editStep{setText(string(kind), key, text), restore(string(kind)+" back", key)}, recheck, true); diff != "" {
			t.Error(diff)
		}
	}
	// decls.nomi imports hop.nomi for its impls alone, so every check
	// settles a whole-file import (checkImplImportsKept).
	cycle, err := readProject(filepath.Join("..", "irbuild", "testdata", "sibimpl_cycle"), "sibimpl_cycle")
	if err != nil {
		t.Fatal(err)
	}
	if fa := fullCheck(cycle, cycle.texts).Files["decls"]; fa == nil || len(fa.ImplImports) == 0 {
		t.Fatal("sibimpl_cycle's decls.nomi has no impl-only import")
	}
	for _, key := range []string{"decls", "hop", cycle.entry} {
		if diff, _ := runEdits(t, cycle, genericEdits(key, cycle.texts[key]), recheck, true); diff != "" {
			t.Error(diff)
		}
	}
	projects, err := corpusProjects()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range projects {
		requireKeptCoherence(t, fullCheck(p, p.texts), p.name)
		if line := firstBodyLine(p.texts[p.entry]); line != "" {
			diff, _ := runEdits(t, p, []editStep{
				replace("body only, positions shift", p.entry, line, line+"    // A comment.\n\n"),
				restore("body back", p.entry),
			}, recheck, false)
			if diff != "" {
				t.Error(diff)
			}
		}
	}
}

// TestIncrementalHarnessCatchesStaleDemands: a coherence cache that keeps
// an edited file's old demands (staleDemands) fails the harness: the full
// check reports the missing impl the edit's new demand draws, and the
// recheck does not.
func TestIncrementalHarnessCatchesStaleDemands(t *testing.T) {
	dm := newManager()
	p := faultProject()
	var diff string
	var outcomes []analysis.RecheckOutcome
	analysis.KeepStaleDemandsForTest(func() {
		diff, outcomes = runEdits(t, p, faultEdits[:1], dm.RecheckWithOutcome, true)
	})
	if len(outcomes) != 1 || outcomes[0].Path != analysis.RecheckBodies {
		t.Fatalf("the edit went %v, want the bodies path", outcomes)
	}
	for _, want := range []string{"step 1 (body only, an unsupplied demand added, editing main, recheck bodies)", "full check:", "recheck:", "no impl of `Equatable` for `Error`"} {
		if !strings.Contains(diff, want) {
			t.Errorf("the harness's report on a coherence cache that keeps stale demands has no %q:\n%s", want, diff)
		}
	}
	// The same edit with the cache kept up to date passes.
	if diff, _ := runEdits(t, p, faultEdits, dm.RecheckWithOutcome, true); diff != "" {
		t.Fatal(diff)
	}
}
