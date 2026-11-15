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
		// Decimal.format, the std function it rounds through, and the host
		// function that ends in: the program's line is its call.
		"stdlib_trap_deep.nomi": "line 11: Decimal.round: scale 4294967298 is past a Decimal's, which is -2147483648 to 2147483647\n",
		// The lambda's own line, not the `Iter.map` stage's (line 10).
		"stdlib_trap_lambda.nomi": "line 12: `NonZeroInt.try_from(divisor)` found nothing\n",
		// A host function's text names no line; the call's is put first.
		"stdlib_trap_host.nomi": "line 17: Int.pow: exponent 100000000000 is too large: the power would have more than 17179869184 bits\n",
	} {
		t.Run(name, func(t *testing.T) {
			got := goldenReference(t, fixture(name))
			if got.exit != 1 || got.stdout != "before\n" || got.stderr != want {
				t.Fatalf("%s:\n%s\nwant stdout %q and stderr %q", name, got, "before\n", want)
			}
		})
	}
}
