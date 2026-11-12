package irbuild

import "testing"

// Type-qualified calls into another file's impl functions
// (testdata/inst_impl_defaults) run:
//   - a generic type's constructor that skips a default naming `Duration`,
//     which the calling file never imports, positionally, with a named
//     argument before a trailing function name, and with a trailing lambda:
//     the declaring file builds the instance's accessors
//     (instimplaccessor.go), and a trailing name goes to the function-typed
//     last slot as the checker sends it (namedQualCall);
//   - a concrete value entering an interface-typed parameter of a
//     monomorphic constructor whose defaults are left out, positionally and
//     with a named argument (qualEraseSlotted, namedCallHome).
func TestInstImplAccessors_DefaultsTheCallerCannotNameFill(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	path := fixture("inst_impl_defaults/main.nomi")
	if _, err := Analyze(path); err != nil {
		t.Fatalf("the front end rejects this, so the test no longer checks the builder: %v", err)
	}
	want := "" +
		"dbg line 24: (a.size, Box.wait_seconds(a), a.handler(4)) = (1, 300, 8)\n" +
		"dbg line 26: (b.size, Box.wait_seconds(b), b.handler(5)) = (3, 300, 10)\n" +
		"dbg line 28: (c.size, Box.wait_seconds(c), c.handler(\"four\")) = (2, 300, 4)\n" +
		"dbg line 29: Greeter.new(Cat{nick: \"Tom\"}).greeting = \"hello, Tom!\"\n" +
		"dbg line 30: Greeter.new(Cat{nick: \"Tom\"}, punct: \"?\").greeting = \"hello, Tom?\"\n"
	got := vmReference(path)
	if got.stdout != want || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
}
