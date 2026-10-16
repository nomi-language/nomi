package irbuild

import (
	"testing"
)

// A trap inside the stdlib is reported at the program's call into it, with
// the trap's own words after the line (internal/vm/stdlibfault.go). Each
// fixture's record in irbuild.expect pins its whole transcript, and
// TestIRGolden_RecordsMatchTheVM runs it again on the VM; this names the
// line each one must report, so a record regenerated with a std line in it
// fails here.
func TestIRStdlibTrap_ReportsTheProgramsLine(t *testing.T) {
	for name, want := range map[string]string{
		// The trap is in std/int.nomi's `abs`.
		"stdlib_trap_abs.nomi": "line 9: integer overflow: 0 - -9223372036854775808\n",
		// The zero divisor is in std/int.nomi's `modulo`.
		"stdlib_trap_modulo.nomi": "line 7: division by zero\n",
		// Iter.sum, its reduce lambda and Duration's Add impl: the
		// program's line is the `|> Iter.sum()` stage.
		"stdlib_trap_deep.nomi": "line 14: integer overflow: 9223372036854775807 + 1\n",
		// The lambda's own line, not the `Iter.map` stage's (line 10).
		"stdlib_trap_lambda.nomi": "line 12: division by zero\n",
		// A host function's text names no line; the call's is put first.
		"stdlib_trap_repeat.nomi": "line 17: strings.String.repeat: negative count -2\n",
	} {
		t.Run(name, func(t *testing.T) {
			got := goldenReference(t, fixture(name))
			if got.exit != 1 || got.stdout != "before\n" || got.stderr != want {
				t.Fatalf("%s:\n%s\nwant stdout %q and stderr %q", name, got, "before\n", want)
			}
		})
	}
}
