package irbuild

import (
	"strings"
	"testing"
)

// Every form of `Pattern = value else { ... }` runs on the VM
// (testdata/binding_else.nomi): a block or arms that leave, a fallback for
// the pattern's payload (a name, a tuple, a struct variant's fields or its
// whole record), arms mixing the two, a guard, a rebinding of the value's own
// name, a nested or list or map or tuple pattern whose else leaves, `try` in
// the else, and the exits of a lambda (`return`), an Iter.each callback
// (`continue`) and an inline Iter.loop (`break v`, `return v`). No function
// is BLOCKED, and the output matches the golden record.
func TestBindingElse_RunsOnTheVM(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	path := fixture("binding_else.nomi")
	want := strings.Join([]string{
		"Ok(a@b)", "Err(user 2 has no email)",
		"Ok(ada)", "Err(loading user 2: disk)", "Err(no user 3)",
		"none", "x",
		"20000", "1920",
		"Ok(8080)", "Err(bad port: x)", "Ok(9000)",
		"a3:3", "unknown:0",
		"7", "-1",
		"4", "-1",
		"hi ada", "anon",
		"3", "-1",
		"2", "1", "-9",
		"got 1", "ok 2",
		"5", "0",
		"3", "0",
		"Ok(2)", "Ok(10)", "Err(b)",
		"[10, 0, 30]",
		"(200, 100)", "(200, 100)",
		"6",
	}, "\n") + "\n"
	got := vmReference(path)
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
	golden := goldenReference(t, path)
	if got.stdout != golden.stdout || got.exit != golden.exit {
		t.Errorf("VM and golden output differ:\nVM (exit %d):\n%s\ngolden (exit %d):\n%s",
			got.exit, got.stdout, golden.exit, golden.stdout)
	}
}

// The checker admits a binding else the builder must lower, and it does: the
// fixture's functions are all retained.
func TestBindingElse_EveryFunctionIsRetained(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	src := `fn shown(email: Maybe<String>): String {
    Some(e) = email else { "none" }
    e
}

fn first_or(xs: List<Int>): Int {
    [first, .._rest] = xs else { return -1 }
    first
}

fn port(r: Result<Int, String>): Result<Int, String> {
    Ok(p) = r else {
        Err("") -> 8080
        Err(e) -> return Err(e)
    }
    Ok(p)
}

fn main() {
    _ = shown(None)
    _ = first_or([])
    _ = port(Ok(1))
}
`
	names, _ := irRetainedNames(t, src)
	for _, name := range []string{"shown", "first_or", "port"} {
		if !names[name] {
			t.Errorf("%s is not retained (retained: %v)", name, names)
		}
	}
}
