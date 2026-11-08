package irbuild

import (
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/stdlibbindings"
)

// TestStdHostHandleRowsMatchTheBindingsTypeTable holds stdHostSpecs'
// `handle` bit against the host-type registry, in both directions.
//
// The source cannot answer the question: every std type declaration is a bare
// `pub host type`, so `Regex` and `Context` read identically in source.
//
// A handle is a type registered through internal/stdlibbindings' Types(), which
// is what makes its value a host handle and its assertion `values:` operand
// `<Regex>` rather than whatever `impl Debug` std wrote. So the check is
// against that table.
//
// BOTH DIRECTIONS, because each rules out a different wrong row:
//
//   - a `handle: true` row whose type is NOT registered claims a handle
//     rendering for a value that will never be one, so the `values:` row is
//     wrong and nothing else notices;
//   - a `handle: false` row whose type it DOES register sends a Go-backed
//     handle down the rt-implemented path, which is the wrong-layout direction.
//
// The registry's names are `<module>.<Type>` and this table's identity is
// `(origin, nomi)` — `std/regex` + `Regex` against `regex.Regex` — so the
// comparison strips the `std/` prefix rather than matching on the bare name. A
// bare-name match would let a row for one module's `Regex` satisfy another's.
func TestStdHostHandleRowsMatchTheBindingsTypeTable(t *testing.T) {
	registered := map[string]bool{}
	for _, tb := range stdlibbindings.Types() {
		registered[tb.Name] = true
	}
	if len(registered) == 0 {
		t.Fatal("internal/stdlibbindings registers no host types at all; both directions " +
			"below would pass vacuously")
	}

	var claimedNotRegistered, registeredNotClaimed []string
	claimed := map[string]bool{}
	for i := range stdHostSpecs {
		s := &stdHostSpecs[i]
		if s.origin == "" {
			// A row identified by its analyzer singleton (Int, String, Bytes
			// and friends). Those are language primitives with no module
			// qualifier to compare, and none is or could be a handle.
			if s.handle {
				t.Errorf("%s is a handle row with no origin; a handle is a module's type "+
					"and the registry names it <module>.<Type>", s.nomi)
			}
			continue
		}
		name := strings.TrimPrefix(s.origin, "std/") + "." + s.nomi
		claimed[name] = true
		switch {
		case s.handle && !registered[name]:
			claimedNotRegistered = append(claimedNotRegistered, name)
		case !s.handle && registered[name]:
			registeredNotClaimed = append(registeredNotClaimed, name)
		}
	}
	// The other half of direction two: a registered type this table does not
	// mention at all is not a wrong row, but it is a type the builder has no
	// representation for, and a reader of this table should be able to
	// see that from here.
	var unmentioned []string
	for name := range registered {
		if !claimed[name] {
			unmentioned = append(unmentioned, name)
		}
	}
	sort.Strings(claimedNotRegistered)
	sort.Strings(registeredNotClaimed)
	sort.Strings(unmentioned)

	if len(claimedNotRegistered) > 0 {
		t.Errorf("handle row(s) whose type internal/stdlibbindings does not register: %v\n"+
			"The row claims a host-handle `values:` rendering (`<%s>`) for a value "+
			"that will never be one.", claimedNotRegistered, strings.Join(claimedNotRegistered, ">, <"))
	}
	if len(registeredNotClaimed) > 0 {
		t.Errorf("non-handle row(s) whose type internal/stdlibbindings DOES register: %v\n"+
			"That sends a Go-backed handle down the rt-implemented path.", registeredNotClaimed)
	}
	t.Logf("registered host types: %d; anchored here as handles: %d; registered but not in this table: %v",
		len(registered), len(claimedNotRegistered)+countHandleRows(), unmentioned)
}

func countHandleRows() int {
	n := 0
	for i := range stdHostSpecs {
		if stdHostSpecs[i].handle {
			n++
		}
	}
	return n
}
