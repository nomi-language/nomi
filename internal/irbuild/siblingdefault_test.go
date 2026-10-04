package irbuild

import (
	"strings"
	"testing"
)

// Calls into another file's function with defaulted parameters run
// (testdata/sibling_defaults): each omitted parameter is filled by the
// declaring file's accessor, so a default may name that file's private
// function and a type the caller never imported, and the written arguments
// are evaluated, in Nomi's order, before any default.
func TestSiblingDefaults_CallsAcrossFilesFillDefaults(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := "" +
		// lib.add, every argument written, a default used, named.
		"3\n" +
		"2\n" +
		"6\n" +
		"8\n" +
		// The bare spelling after `import lib.{add}`.
		"3\n" +
		"5\n" +
		"10\n" +
		// A default naming an earlier parameter.
		"9\n" +
		"4\n" +
		// A default calling the declaring file's private `base`, which
		// prints; a named argument skipping it still evaluates it.
		"base\n" +
		"101\n" +
		"3\n" +
		"base\n" +
		"1\n" +
		// A default naming an earlier parameter, a named argument skipping a
		// default, and the written arguments evaluated positionals-first
		// before the default fills.
		"abc:3!\n" +
		"abc:3?\n" +
		"arg .\n" +
		"arg xy\n" +
		"xy:2.\n" +
		"arg q\n" +
		"q:1!\n" +
		// A generic function's defaults, qualified and bare.
		"[1]\n" +
		"[\"a\", \"b\"]\n" +
		"[1, 1, 2]\n" +
		"[True]\n" +
		// A same-file generic with a default.
		"1\n" +
		"y\n" +
		"y\n"
	got := vmReference(fixture("sibling_defaults/main.nomi"))
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
}

// Literals that omit a defaulted field of a type another file declares run
// (testdata/sibling_field_defaults): each omitted field is filled by the
// declaring file's accessor, so a default may be a constructor (`None`,
// `Some(Item.Key)`, `[]`), a variant, a call to that file's private function,
// or that file's private once, and reads that file's names rather than the
// constructing file's. Covers a generic struct's instances and a record
// variant, bare and inferred.
func TestSiblingDefaults_LiteralsFillForeignFieldDefaults(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := "" +
		"base\n" +
		`Loot{name: "a", drops: None, held: Some(Key), tags: [], item: Coin, hello: "lib", weight: 42, rare: False}` + "\n" +
		`Loot{name: "b", drops: Some(3), held: Some(Key), tags: [], item: Coin, hello: "lib", weight: 7, rare: False}` + "\n" +
		`Loot{name: "c", drops: None, held: Some(Key), tags: [], item: Coin, hello: "lib", weight: 42, rare: False}` + "\n" +
		"Box{value: 1, extra: [], label: None}\n" +
		`Box{value: "s", extra: [], label: Some("x")}` + "\n" +
		"Box{value: Local{x: 1}, extra: [], label: None}\n" +
		`Tagged{name: "t", tags: [], at: None}` + "\n" +
		`Tagged{name: "inferred", tags: [], at: None}` + "\n" +
		"main\n"
	path := fixture("sibling_field_defaults/main.nomi")
	got := vmReference(path)
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
	golden := goldenReference(t, path)
	if golden.stdout != got.stdout || golden.exit != got.exit {
		t.Errorf("golden record differs from the VM (exit %d):\n%s", golden.exit, golden.stdout)
	}
}

// The same calls inside an assertion subject: the passing case runs, and the
// failing report lists the written arguments and the call's value.
func TestSiblingDefaults_AssertionSubjectRecordsWrittenArguments(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	got := vmReference(fixture("sibling_defaults/calls_test.nomi"))
	rows := "    values:\n" +
		"      x\n" +
		"        = 2\n" +
		"      x\n" +
		"        = 2\n" +
		"      lib.add(x, b: x)\n" +
		"        = 4\n"
	if got.exit != 1 || strings.Contains(got.stdout+got.stderr, "BLOCKED") ||
		!strings.Contains(got.stdout, rows) || !strings.Contains(got.stdout, "1 passed, 1 failed") {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want rows ---\n%s",
			got.exit, got.stdout, got.stderr, rows)
	}
}
