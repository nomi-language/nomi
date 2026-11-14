package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The names other languages give a List's length point at `List.length`,
// written type-qualified or as a field, and `Set.add` given one element
// points at `Set.insert`.
func TestCheckCollectionHints_LengthAndSetInsert(t *testing.T) {
	env := append(os.Environ(), "NOMI_FFIRUN_CACHE_ROOT="+t.TempDir(), "NOMI_COLOR=never", "NOMI_DIAGNOSTICS=", "NOMI_ARTIFACT_CACHE=off")
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "main.nomi"), `import std::io

fn main()
    xs = [1, 2, 3]
    io.print("${List.size(xs)} ${xs.length}")
    s = Set.add(Set[1, 2], 3)
    io.print("${s}")
end
`)
	want := "main.nomi:5:22: type 'List' has no member 'size'\n" +
		"main.nomi:5:22: help: did you mean `List.length`?\n" +
		"main.nomi:5:37: List<Int> has no field 'length'\n" +
		"main.nomi:5:37: help: a List has no fields: write `List.length(xs)`\n" +
		"main.nomi:6:28: argument 2: expected Set<Int>, got Int\n" +
		"main.nomi:6:28: help: `Set.add` is `+`, the union of two sets (`Set.union`); `Set.insert(s, x)` adds one element\n"
	o := runProcess(t, env, dir, "", nomiBin, "check", "--format", "short", "main.nomi")
	if o.exit != 1 || o.stderr != want {
		t.Errorf("nomi check: exit %d\nstderr:\n%s\nwant stderr:\n%s", o.exit, o.stderr, want)
	}
}
