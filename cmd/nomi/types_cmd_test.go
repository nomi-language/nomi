package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// typesFixture is a module whose models.nomi and api/ declare types that
// cross as JSON, from the repository root, where TestMain runs the tests.
var typesFixture = filepath.Join("cmd", "nomi", "testdata", "types", "shop")

// typesGolden is what `nomi types models.nomi api` writes in typesFixture:
// structs, enums with and without payloads, a generic type, Maybe, lists,
// sets, vectors, maps, a distinct type, a type alias, std types, a
// reference across files, two Users of different files, and a hand-written
// ToJson. NOMI_REGENERATE_EXPECTATIONS=1 writes it.
var typesGolden = filepath.Join("cmd", "nomi", "testdata", "types", "shop.d.ts.golden")

func TestTypesCommand_WritesTheFixturesDeclarations(t *testing.T) {
	dir, err := filepath.Abs(typesFixture)
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runNomiSplit(t, t.TempDir(), dir, "types", "models.nomi", "api")
	if err != nil || stderr != "" {
		t.Fatalf("nomi types: %v\n%s%s", err, stdout, stderr)
	}
	if os.Getenv("NOMI_REGENERATE_EXPECTATIONS") == "1" {
		if err := os.WriteFile(typesGolden, []byte(stdout), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(typesGolden)
	if err != nil {
		t.Fatal(err)
	}
	if stdout != string(want) {
		t.Errorf("nomi types wrote:\n%s\nwant (%s):\n%s", stdout, typesGolden, want)
	}

	// -o writes the same text to a file, and nothing to stdout.
	out := filepath.Join(t.TempDir(), "api.ts")
	stdout, stderr, err = runNomiSplit(t, t.TempDir(), dir, "types", "models.nomi", "api", "-o", out)
	if err != nil || stdout != "" || stderr != "" {
		t.Fatalf("nomi types -o: %v\n%s%s", err, stdout, stderr)
	}
	if got, _ := os.ReadFile(out); string(got) != string(want) {
		t.Errorf("-o wrote:\n%s", got)
	}

	// The output is a valid TypeScript module, when a tsc is at hand.
	if tsc, err := exec.LookPath("tsc"); err == nil {
		got, err := exec.Command(tsc, "--noEmit", "--strict", out).CombinedOutput()
		if err != nil {
			t.Errorf("tsc --strict: %v\n%s", err, got)
		}
	} else {
		t.Log("no tsc on PATH: the output is not type-checked")
	}
}

// A named path whose files declare no pub type that crosses as JSON is an
// error, and nothing is written.
func TestTypesCommand_APathWithNoJSONTypesIsAnError(t *testing.T) {
	dir, err := filepath.Abs(typesFixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"views/plain.nomi"}, "nomi types: views/plain.nomi declares no pub type that crosses as JSON\nhelp: derive ToJson or FromJson"},
		{[]string{"models.nomi", "views"}, "nomi types: no file under views declares a pub type that crosses as JSON"},
		{[]string{"nomi.toml"}, "nomi types: nomi.toml is not a .nomi file or a directory"},
		{[]string{"missing"}, "nomi types: missing: no such file or directory"},
		{nil, "usage: nomi types <paths...> [-o <file>]"},
	} {
		stdout, stderr, err := runNomiSplit(t, t.TempDir(), dir, append([]string{"types"}, c.args...)...)
		if err == nil || stdout != "" || !strings.Contains(stderr, c.want) {
			t.Errorf("nomi types %v: %v\nstdout:\n%s\nstderr:\n%s\nwant stderr to hold %q", c.args, err, stdout, stderr, c.want)
		}
	}
}
