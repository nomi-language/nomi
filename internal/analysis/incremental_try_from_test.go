package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// tryFromProject is a project whose `?` in one file converts its error
// through a `From` impl another file declares (spec §11 *Propagating with
// `?`*): work's `load` propagates a `Result<_, DiskError>` out of a function
// returning `Result<_, AppError>`. The impl starts absent, so the `?` is an
// error. errs declares both error types and work imports it; convert
// imports errs and is not imported by work; greet imports neither, so an
// edit to errs checks its dependents rather than the whole program.
func tryFromProject() incrementalProject {
	return incrementalProject{name: "try from", entry: "main", texts: map[string]string{
		"main": `import
    std::io
    work
    convert
    greet
end

fn main()
    io.print(greet.hello())
    io.print(work.run())
    io.print(convert.describe())
end
`,
		"greet": `pub fn hello() -> String
    "hello"
end
`,
		"errs": `pub struct DiskError
    path: String
end

pub struct AppError
    message: String
end
`,
		"convert": `import errs.{DiskError, AppError}

pub fn describe() -> String
    e = AppError{message: DiskError{path: "b.txt"}.path}
    e.message
end
`,
		"work": `import errs.{DiskError, AppError}

fn read(path: String) -> Result<Int, DiskError>
    if path == "" then Err(DiskError{path:}) else Ok(String.length(path))
end

fn load(path: String) -> Result<Int, AppError>
    n = read(path)?
    Ok(n + 1)
end

pub fn run() -> String
    case load("a.txt")
        Ok(n) -> "read ${n}"
        Err(e) -> e.message
    end
end
`,
	}}
}

const tryFromImpl = `
impl From<DiskError> for AppError
    fn from(e: DiskError) -> AppError
        AppError{message: "disk: ${e.path}"}
    end
end
`

// A second `?` in load's body, converting too.
var tryFromSecondTry = replace("body only, a second converting `?`", "work", "    Ok(n + 1)", "    m = read(\"\")?\n    Ok(n + m)")

// tryFromEdits add and remove the impl a `?` in work converts through, in
// the file that declares the error types and in one work does not import,
// and edit work's body while the impl is in each.
var tryFromEdits = []editStep{
	appendText("impl added beside the error types [dependents]", "errs", tryFromImpl),
	replace("impl body only", "errs", `"disk: ${e.path}"`, `"disk error: ${e.path}"`),
	tryFromSecondTry,
	restore("body back", "work"),
	restore("impl removed beside the error types [dependents]", "errs"),
	tryFromSecondTry,
	restore("body back", "work"),
	appendText("impl added in a file the `?`'s file does not import", "convert", tryFromImpl),
	tryFromSecondTry,
	restore("body back", "work"),
	restore("impl removed from a file the `?`'s file does not import", "convert"),
}

// TestIncrementalMatchesFullForATryFromImpl: a `?` whose conversion starts
// or stops with an impl another file adds or removes is checked again, to
// the program a full check answers, the `?`'s TryOpInfo.Convert and
// DispatchImpl included (the dump holds the `?`'s reference). An impl in a
// file the `?`'s file does not import is seen by a file the edit's
// dependents leave out, so adding or removing it checks the whole program.
func TestIncrementalMatchesFullForATryFromImpl(t *testing.T) {
	t.Parallel()
	p := tryFromProject()
	// The fixture holds what the steps compare: without the impl the `?` is
	// an error, and with it, in either file, the program checks.
	if errs := fullCheck(p, p.texts).Errors["work"]; len(errs) != 1 || !strings.Contains(errs[0].Message, "`?` error type mismatch") {
		t.Fatalf("without the impl, want work's `?` to be the one error, got %v", errs)
	}
	for _, key := range []string{"errs", "convert"} {
		texts := map[string]string{}
		for k, v := range p.texts {
			texts[k] = v
		}
		texts[key] += tryFromImpl
		prog := fullCheck(p, texts)
		for file, errs := range prog.Errors {
			if len(errs) > 0 {
				t.Fatalf("with the impl in %s, %s does not check: %v", key, file, errs)
			}
		}
		if dump := analysis.DumpProgramForTest(prog); !strings.Contains(dump, " converts DiskError to AppError") {
			t.Fatalf("with the impl in %s, the dump shows no conversion at the `?`:\n%s", key, dump)
		}
	}
	runChecked(t, p, tryFromEdits, newManager().RecheckWithOutcome)
}
