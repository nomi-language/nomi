package irbuild

import "testing"

// A subtype named through its module's qualifier is called as its bare
// name is: subtypeCall reads the declaration the checker resolved the
// name to, and a std struct literal named through its module builds the
// struct the checker typed it as.
func TestQualifiedSubtype_ACallThroughItsModuleRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import std::io
import std::float
import std::int

fn main()
    x = float.NonNegativeFloat(2.5)
    n = int.PositiveInt(4)
    io.print("${x} ${n}")
end
`, "2.5 4\n")
}
