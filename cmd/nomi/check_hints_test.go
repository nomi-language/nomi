package main

import (
	"path/filepath"
	"strings"
	"testing"
)

const hintsProgram = `import std::io

struct A
    street: String
end

struct P
    address: A
end

fn main()
    ada = P{address: A{street: "1"}}
    moved = {..ada, address: {.., street: ada.address.street + "!"}}
    n = 1 as Int
    io.print("${moved.address.street} ${n}")
end
`

// `nomi check` says nothing of the hints the language server shows, and
// `nomi check --hints` reports them, as text and as JSON records of
// severity "hint", and passes the file. A check that reads the kept
// program reports them as a fresh one does.
func TestCheckHints_ReportedOnlyWithTheFlag(t *testing.T) {
	dir := t.TempDir()
	cache := t.TempDir()
	entry := filepath.Join(dir, "main.nomi")
	mustWrite(t, entry, hintsProgram)
	const restated = "hint: this reads the value `street` already holds here, which `..` writes"
	const ascription = "hint: `as Int` is redundant: the value already has type `Int`"

	plain := runNomiIn(t, dir, "off", "check", entry)
	if plain.exit != 0 || plain.stderr != "" || !strings.Contains(plain.stdout, "ok ") {
		t.Fatalf("nomi check: %v", plain)
	}
	plainJSON := runNomiIn(t, dir, "off", "check", "--format", "json", entry)
	if plainJSON.exit != 0 || strings.Contains(plainJSON.stdout, `"severity"`) {
		t.Fatalf("nomi check --format json: %v", plainJSON)
	}

	text := runNomiIn(t, dir, "off", "check", "--hints", entry)
	if text.exit != 0 || !strings.Contains(text.stdout, "ok ") ||
		!strings.Contains(text.stderr, restated) || !strings.Contains(text.stderr, "main.nomi:13:43") ||
		!strings.Contains(text.stderr, ascription) || !strings.Contains(text.stderr, "main.nomi:14:11") {
		t.Fatalf("nomi check --hints: %v", text)
	}
	if strings.Index(text.stderr, restated) > strings.Index(text.stderr, ascription) {
		t.Errorf("the hints are not in the order of their lines:\n%s", text.stderr)
	}

	jsonCheck := runNomiIn(t, dir, "off", "check", "--hints", "--format", "json", entry)
	for _, want := range []string{
		`"line":13,"col":43,"end_line":13,"end_col":61`,
		`"severity":"hint","code":"restated-current-value"`,
		`"line":14,"col":11,"end_line":14,"end_col":17`,
		`"severity":"hint","code":"redundant-ascription"`,
		`"status":"ok"`,
		`{"type":"summary","files":1,"failed":0,"diagnostics":0}`,
	} {
		if jsonCheck.exit != 0 || !strings.Contains(jsonCheck.stdout, want) {
			t.Fatalf("nomi check --hints --format json lacks %s: %v", want, jsonCheck)
		}
	}

	if run := runNomiIn(t, dir, cache, "run", entry); run.exit != 0 || run.stdout != "1! 1\n" || run.stderr != "" || !run.kept() {
		t.Fatalf("nomi run: %v\nreport: %v", run, run.report)
	}
	kept := runNomiIn(t, dir, cache, "check", "--hints", entry)
	if !kept.read() {
		t.Fatalf("nomi check --hints after a run did not read the kept program: %v", kept.report)
	}
	if kept.exit != text.exit || kept.stdout != text.stdout || kept.stderr != text.stderr {
		t.Fatalf("a check of the kept program writes what a fresh one does not:\n--- got ---\n%v\n--- fresh ---\n%v", kept, text)
	}
	if again := runNomiIn(t, dir, cache, "check", entry); again.stderr != "" {
		t.Fatalf("nomi check of the kept program without --hints: %v", again)
	}
}
