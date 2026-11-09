package main

import (
	"path/filepath"
	"strings"
	"testing"
)

const adviceProgram = `import std::io

interface Named {
    fn name(value: self) -> String
}

struct Box {
    n: Int
}

impl Named for Box {
    fn name(value: Box) -> String {
        "box ${value.n}"
    }
}

fn label<T>(value: T) -> String where T: Named {
    "a ${T.name(value)}"
}

fn main() {
    io.print(label(Box{n: 1}))
}
`

// `nomi check` reports a type parameter that can be an interface value as
// a warning with its help, and passes the file; `--format json` writes it
// with its code and fix. `nomi run` says nothing of it.
func TestInterfaceValueAdvice_CheckWarnsRunDoesNot(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.nomi")
	mustWrite(t, entry, adviceProgram)
	const warning = "warning: `T` is used only as the type of `value`, under one bound: write `value: Named`"
	const help = "= help: a parameter of type Named takes a value of any implementor, as `T` does; keep `T` when the caller's type matters past the call"

	run := runNomiIn(t, dir, "off", "run", entry)
	if run.exit != 0 || run.stdout != "a box 1\n" || run.stderr != "" {
		t.Fatalf("nomi run: %v", run)
	}

	check := runNomiIn(t, dir, "off", "check", entry)
	if check.exit != 0 || !strings.Contains(check.stderr, warning) || !strings.Contains(check.stderr, "main.nomi:17:10") || !strings.Contains(check.stderr, help) {
		t.Fatalf("nomi check: %v", check)
	}

	jsonCheck := runNomiIn(t, dir, "off", "check", "--format", "json", entry)
	if jsonCheck.exit != 0 || !strings.Contains(jsonCheck.stdout, `"severity":"warning","code":"interface-value-parameter"`) ||
		!strings.Contains(jsonCheck.stdout, "Write `value: Named`") || !strings.Contains(jsonCheck.stdout, `"status":"ok"`) {
		t.Fatalf("nomi check --format json: %v", jsonCheck)
	}
}

// A program with advice is kept, with its advice: a check that reads it
// after a run writes the advice a fresh check writes, as text and as JSON,
// and so does a check that relinks it after an edit of a body, which keeps
// the advice of a file it does not check again and works out that of the
// file it does.
func TestInterfaceValueAdvice_KeptProgramWritesIt(t *testing.T) {
	dir := t.TempDir()
	cache := t.TempDir()
	entry := filepath.Join(dir, "main.nomi")
	named := filepath.Join(dir, "named.nomi")
	mustWrite(t, filepath.Join(dir, "nomi.toml"), "[module]\nname = \"advice\"\nentry_points = [\"main\"]\n")
	mustWrite(t, entry, `import {
    named.{Box, label}
    std::io
}

fn shout<T>(value: T) -> String where T: Display {
    "${value}!"
}

fn main() {
    io.print(label(Box{n: 1}))
    io.print(shout(2))
}
`)
	namedSrc := `pub interface Named {
    fn name(value: self) -> String
}

pub struct Box {
    n: Int
}

impl Named for Box {
    fn name(value: Box) -> String {
        "box ${value.n}"
    }
}

pub fn label<T>(value: T) -> String where T: Named {
    "a ${T.name(value)}"
}
`
	mustWrite(t, named, namedSrc)

	fresh := func(args ...string) cacheRun {
		t.Helper()
		return runNomiIn(t, dir, "off", args...)
	}
	same := func(what string, got, want cacheRun) {
		t.Helper()
		if got.exit != want.exit || got.stdout != want.stdout || got.stderr != want.stderr {
			t.Fatalf("%s writes what a fresh check does not:\n--- got ---\n%v\n--- fresh ---\n%v", what, got, want)
		}
	}

	if run := runNomiIn(t, dir, cache, "run", entry); run.exit != 0 || run.stdout != "a box 1\n2!\n" || run.stderr != "" || !run.kept() {
		t.Fatalf("nomi run: %v\nreport: %v", run, run.report)
	}
	want := fresh("check", entry)
	if want.exit != 0 || strings.Count(want.stderr, "warning: ") != 2 || !strings.Contains(want.stderr, "main.nomi:6:10") || !strings.Contains(want.stderr, "named.nomi:15:14") {
		t.Fatalf("a fresh nomi check does not warn twice: %v", want)
	}
	check := runNomiIn(t, dir, cache, "check", entry)
	if !check.read() {
		t.Fatalf("nomi check after a run did not read the kept program: %v", check.report)
	}
	same("a check of the kept program", check, want)
	jsonWant := fresh("check", "--format", "json", entry)
	jsonCheck := runNomiIn(t, dir, cache, "check", "--format", "json", entry)
	if !jsonCheck.read() {
		t.Fatalf("nomi check --format json did not read the kept program: %v", jsonCheck.report)
	}
	same("a JSON check of the kept program", jsonCheck, jsonWant)

	// A body edit in named.nomi: main.nomi is not checked again, and its
	// advice is the kept one; named.nomi's is worked out again.
	mustWrite(t, named, strings.Replace(namedSrc, `"box ${value.n}"`, `"boxed ${value.n}"`, 1))
	want = fresh("check", entry)
	check = runNomiIn(t, dir, cache, "check", entry)
	if !check.relinked() {
		t.Fatalf("nomi check after a body edit did not relink: %v", check.report)
	}
	same("a check of the relinked program", check, want)
	if check = runNomiIn(t, dir, cache, "check", entry); !check.read() {
		t.Fatalf("nomi check after the relink did not read the kept program: %v", check.report)
	}
	same("a check of the program the relink kept", check, want)

	// An edit that takes named.nomi's advice away.
	rewritten := strings.NewReplacer("label<T>(value: T)", "label(value: Named)", " where T: Named", "", "T.name(value)", "Named.name(value)").Replace(namedSrc)
	mustWrite(t, named, rewritten)
	want = fresh("check", entry)
	if strings.Count(want.stderr, "warning: ") != 1 {
		t.Fatalf("a fresh check after the rewrite does not warn once: %v", want)
	}
	check = runNomiIn(t, dir, cache, "check", entry)
	same("a check after the rewrite", check, want)
}
