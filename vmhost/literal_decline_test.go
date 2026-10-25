package vmhost_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/vmhost"
)

// literalDeclineProgram writes an entry whose one body is declined, so the
// entry retains nothing and has no module, beside a helper file holding a
// backtick typed literal, and answers the entry's path.
func literalDeclineProgram(t *testing.T, literal string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, src string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write("helper.nomi", `import std/regex.Regex

pub fn planted_decline() -> Int {
    1
}

pub fn digits() -> Regex {
    `+literal+`
}
`)
	return write("main.nomi", `import {
    helper
    helper.planted_decline
    std/io
}

fn main() {
    io.print(String.find_all("a1", helper.digits()))
    io.inspect(planted_decline())
}
`)
}

// A program whose entry file lowers nothing has no entry module, and a
// backtick typed literal in another file still has its compile-time check.
// Loading it must not crash running the check: the decline is the program's
// error, as `nomi check` and `nomi run` report every other one.
func TestBacktickLiteral_BesideAnEntryThatLowersNothing(t *testing.T) {
	plantDecline(t)
	path := literalDeclineProgram(t, "Regex`\\d+`")
	want := "this call to `planted_decline` is not supported yet, so `fn main` cannot run"
	check := func(what string, err error) {
		t.Helper()
		var ds frontend.Diagnostics
		if !errors.As(err, &ds) || len(ds) != 1 {
			t.Fatalf("%s answered %v; want one diagnostic", what, err)
		}
		if d := ds[0]; d.Line != 9 || d.Message != want {
			t.Errorf("%s: got %d:%d %q; want line 9 %q", what, d.Line, d.Col, d.Message, want)
		}
	}
	check("Check", vmhost.Check(path))
	p, err := vmhost.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	check("Unsupported", p.Unsupported())
}

// The literal there is still checked: an invalid one is the load's error.
func TestBacktickLiteral_InvalidBesideAnEntryThatLowersNothing(t *testing.T) {
	plantDecline(t)
	path := literalDeclineProgram(t, "Regex`[`")
	_, err := vmhost.Load(path)
	var ds frontend.Diagnostics
	if !errors.As(err, &ds) || len(ds) != 1 {
		t.Fatalf("Load answered %v; want the literal's diagnostic", err)
	}
	if d := ds[0]; !strings.HasSuffix(d.Path, "helper.nomi") || d.Line != 8 || !strings.Contains(d.Message, "typed literal Regex`[` is invalid") {
		t.Errorf("got %s:%d:%d %q; want helper.nomi:8 and the literal's message", d.Path, d.Line, d.Col, d.Message)
	}
}
