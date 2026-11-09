package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// `defer on error` (spec §9 *`defer`*): its release covers the error exits
// of a value the body hands on, and an error exit before a value is
// released or handed on leaks it.

func TestDeferOnErrorCoversTheErrorExitsOfAHandedOnValue(t *testing.T) {
	expectReleaseErrors(t, `struct Store {
    reader: Reader
}

fn open(path: String) -> Result<Store, Error> {
    r = fs.open(path)?
    defer on error Reader.close_quietly(r)
    _ = Reader.read_line(r)?
    Ok(Store{reader: r})
}

fn returned(path: String, early: Bool) -> Result<Reader, Error> {
    r = fs.open(path)?
    defer on error Reader.close_quietly(r)
    if early {
        return Ok(r)
    }
    _ = Reader.read_line(r)?
    Ok(r)
}
`)
}

func TestAnErrorExitBeforeTheHandOnLeaks(t *testing.T) {
	errs := expectReleaseErrors(t, `struct Store {
    reader: Reader
}

fn open(path: String) -> Result<Store, Error> {
    r = fs.open(path)?
    _ = Reader.read_line(r)?
    Ok(Store{reader: r})
}

fn early(path: String, other: String) -> Result<Reader, Error> {
    r = fs.open(path)?
    case fs.read_text(other) {
        Err(e) -> return Err(e)
        Ok(_) -> Unit
    }
    Ok(r)
}
`, at(7, 28, "`r` leaks if this `?` returns an error: it isn't released on that path; add `defer on error Reader.close_quietly(r)` after acquiring it"),
		at(14, 19, "`r` leaks if this `return` runs: it isn't released on that path; add `defer on error Reader.close_quietly(r)` after acquiring it"))
	if errs[0].Code != analysis.ErrorExitLeakCode {
		t.Errorf("code %q, want %q", errs[0].Code, analysis.ErrorExitLeakCode)
	}
	fix := errs[0].Fix
	if fix == nil || len(fix.Edits) != 1 {
		t.Fatalf("fix = %+v, want one edit", fix)
	}
	if e := fix.Edits[0]; e.Start.Line != releaseLine(7) || e.Start.Col != 1 || e.Text != "    defer on error Reader.close_quietly(r)\n" {
		t.Errorf("edit = %+v, want the line after the binding", e)
	}
}

func TestAnErrorExitBeforeTheDeferLeaks(t *testing.T) {
	expectReleaseErrors(t, `fn first(path: String, other: String) -> Result<Maybe<String>, Error> {
    r = fs.open(path)?
    _ = fs.read_text(other)?
    defer Reader.close_quietly(r)
    Reader.read_line(r)
}

fn handed(path: String, other: String) -> Result<Reader, Error> {
    r = fs.open(path)?
    _ = fs.read_text(other)?
    defer on error Reader.close_quietly(r)
    Ok(r)
}
`, at(3, 28, "`r` leaks if this `?` returns an error: its `defer` comes after it; move the `defer` up to right after acquiring `r`"),
		at(10, 28, "`r` leaks if this `?` returns an error: its `defer on error` comes after it; move it up to right after acquiring `r`"))
}

func TestDeferOnErrorAloneLeavesTheSuccessPathUnreleased(t *testing.T) {
	expectReleaseErrors(t, `fn all(path: String) -> Result<String, Error> {
    r = fs.open(path)?
    defer on error Reader.close_quietly(r)
    Reader.read_all_text(r)
}
`, at(2, 5, "`r` is released only if the body fails; write `defer Reader.close_quietly(r)` after it, or return it"))
}

func TestDeferOnErrorNeedsAFunctionThatCanFail(t *testing.T) {
	expectReleaseErrors(t, `fn note(n: Int) {
    io.print(n)
}

fn plain(n: Int) -> Int {
    defer on error note(n)
    n + 1
}

fn fallible(n: Int) -> Result<Int, String> {
    defer on error note(n)
    Ok(n)
}

fn optional(n: Int) -> Maybe<Int> {
    defer on error note(n)
    Some(n)
}
`, at(6, 5, "`defer on error` runs only when `plain` fails, and it returns Int, which carries no failure; write `defer`"))
}
