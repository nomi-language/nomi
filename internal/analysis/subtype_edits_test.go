package analysis_test

import (
	"fmt"
	"runtime"
	"testing"
	"time"
)

// TestSubtypeModulesSurviveEdits edits units.nomi of the corpus project
// subtype_modules, whose test file narrows another file's subtype,
// as FuzzIncrementalMatchesFull and FuzzCheckFromInterfacesMatchesFull do
// with the file's own text as the input. The second edit deletes the
// import of NonNegativeFloat, so Meters's parent names no type and the
// test file's literals of Hill and Height reach a chain with no base; the
// checker recursed on them until the stack ran out.
func TestSubtypeModulesSurviveEdits(t *testing.T) {
	const name = "10-generics-and-type-wrappers/subtype_modules"
	for _, p := range fuzzProjects(t) {
		if p.name != name {
			continue
		}
		src := p.texts["units"]
		hang := time.AfterFunc(fuzzHang, func() {
			buf := make([]byte, 1<<20)
			panic(fmt.Sprintf("editing units in %s took over %v:\n%s", name, fuzzHang, buf[:runtime.Stack(buf, true)]))
		})
		defer hang.Stop()
		if diff := fuzzEdits(t, p, "units", src, int64(hashText(src))); diff != "" {
			t.Fatal(diff)
		}
		if diff := interfaceEdits(t, p, "units", src, int64(hashText(src))); diff != "" {
			t.Fatal(diff)
		}
		return
	}
	t.Fatalf("no corpus project %s", name)
}
