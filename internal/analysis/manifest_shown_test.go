package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/parser"
)

// The error in a nomi.toml that stops a build is shown in the file it
// stops at that file's start, its message led by the nomi.toml's path and
// line, with that place as a related location and its fix naming the
// nomi.toml, and in the nomi.toml as it is. An error in another file that
// stops nothing is not shown.
func TestErrorsInFile_ABuildStoppedByAManifestShowsItsError(t *testing.T) {
	stop := analysis.TypeError{File: "/p/nomi.toml", Line: 3, Col: 1, EndLine: 3, EndCol: 10, Code: analysis.ManifestCode,
		Message: "[package] has no key `bogus`", Fix: &parser.Fix{Title: "Remove `bogus`", Edits: []parser.Edit{{
			Start: parser.Pos{Line: 3, Col: 1}, End: parser.Pos{Line: 4, Col: 1}}}}}
	shown := analysis.ErrorsInFile("/p/main.nomi", []analysis.TypeError{stop})
	if len(shown) != 1 {
		t.Fatalf("main.nomi shows %+v; want one error", shown)
	}
	e := shown[0]
	if e.File != "" || e.Line != 1 || e.Col != 1 || e.HasEnd() || e.Message != "/p/nomi.toml:3: [package] has no key `bogus`" || e.Code != analysis.ManifestCode {
		t.Errorf("main.nomi shows %+v; want the error at 1:1, led by /p/nomi.toml:3", e)
	}
	if len(e.Related) != 1 || e.Related[0].File != "/p/nomi.toml" || e.Related[0].Line != 3 || e.Related[0].EndCol != 10 {
		t.Errorf("the error's related locations are %+v; want its place in nomi.toml", e.Related)
	}
	if e.Fix == nil || e.Fix.Path != "/p/nomi.toml" || e.Fix.Title != stop.Fix.Title || stop.Fix.Path != "" {
		t.Errorf("the shown fix is %+v and the error's %+v; want the shown one to edit /p/nomi.toml, and the error's unchanged", e.Fix, stop.Fix)
	}
	if got := analysis.ErrorsInFile("/p/nomi.toml", []analysis.TypeError{stop}); len(got) != 1 || got[0].Message != stop.Message || got[0].Line != 3 {
		t.Errorf("nomi.toml shows %+v; want the error as it is", got)
	}
	other := stop
	other.Code = ""
	if got := analysis.ErrorsInFile("/p/main.nomi", []analysis.TypeError{other}); len(got) != 0 {
		t.Errorf("main.nomi shows %+v of an error in nomi.toml that stops nothing; want none", got)
	}
}
