package analysis

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// The collision check keys an impl on its receiver and its whole interface
// header. A generic interface may be implemented for one receiver at several
// type arguments, the operator interfaces among them, and a call selects one
// statically (impl_selection.go); two impls at one header are the duplicate.

func TestDetectImplCollisions_DistinctInstantiationsAreFine(t *testing.T) {
	for _, headers := range [][]string{
		{"Holds<Int>", "Holds<String>"},
		{"Contains<String>", "Contains<Regex>", "Contains<Codepoint>"},
		// Two operator impls that differ only in their output: the
		// expected type chooses between them.
		{"Add<Days, Day>", "Add<Days, Days>"},
		{"Add<Days, Date>", "Add<Months, Date>", "Add<Years, Date>"},
		// The right-hand types share a base name and are still two.
		{"Add<List<Int>, Bag>", "Add<List<String>, Bag>"},
	} {
		iface := strings.Split(headers[0], "<")[0]
		index := map[string]map[string][]*ast.FuncDef{iface: {"m": nil}}
		files := map[*ast.FuncDef]string{}
		receivers := map[*ast.FuncDef]string{}
		keys := map[*ast.FuncDef]string{}
		for _, h := range headers {
			fn := implFuncDef("m", iface, "Box")
			index[iface]["m"] = append(index[iface]["m"], fn)
			files[fn] = ""
			receivers[fn] = "Box"
			keys[fn] = h
		}
		if errs := detectImplCollisions(index, files, receivers, keys, nil, nil); len(errs) != 0 {
			t.Errorf("impls of %s for one receiver were reported as colliding: %v", strings.Join(headers, ", "), errs)
		}
	}
}

func TestDetectImplCollisions_OneHeaderTwiceIsTheDuplicate(t *testing.T) {
	a := implFuncDef("holds?", "Holds", "Box")
	b := implFuncDef("holds?", "Holds", "Box")
	errs := detectImplCollisions(
		map[string]map[string][]*ast.FuncDef{"Holds": {"holds?": {a, b}}},
		map[*ast.FuncDef]string{a: "", b: ""},
		map[*ast.FuncDef]string{a: "Box", b: "Box"},
		map[*ast.FuncDef]string{a: "Holds<Int>", b: "Holds<Int>"},
		nil, nil,
	)
	if len(errs) != 1 {
		t.Fatalf("two impls of `Holds<Int>` for `Box` produced %d diagnostics, want 1: %v", len(errs), errs)
	}
	for _, want := range []string{"duplicate impl", "`Holds<Int>`", "`Box`", "once at each set of type arguments"} {
		if !strings.Contains(errs[0].Message, want) {
			t.Errorf("the duplicate's message does not say %q: %s", want, errs[0].Message)
		}
	}
}

// A header that did not reach the index is no evidence of a collision.
func TestDetectImplCollisions_UnresolvableHeaderFailsOpen(t *testing.T) {
	fnA := implFuncDef("add", "Add", "Date")
	fnB := implFuncDef("add", "Add", "Date")
	index := map[string]map[string][]*ast.FuncDef{"Add": {"add": {fnA, fnB}}}
	files := map[*ast.FuncDef]string{fnA: "std/calendar", fnB: "std/calendar"}
	receivers := map[*ast.FuncDef]string{fnA: "calendar.Date", fnB: "calendar.Date"}
	ifaceKeys := map[*ast.FuncDef]string{fnA: "Add<Days, Date>"}

	if errs := detectImplCollisions(index, files, receivers, ifaceKeys, nil, nil); len(errs) != 0 {
		t.Fatalf("a missing interface header produced a diagnostic; the check must only ever "+
			"reject on evidence it has, not on an index gap: %v", errs)
	}
}
