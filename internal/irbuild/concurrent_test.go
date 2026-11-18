package irbuild

import (
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// `concurrent { }` and `std/tasks`. See concurrent.go.

// TestConcurrent_TaskRowNamesRTsOwnType is the shape guard on the
// (std/tasks, Task) stdGenHostSpecs row.
func TestConcurrent_TaskRowNamesRTsOwnType(t *testing.T) {
	if taskSpec == nil {
		t.Fatal("stdGenHostSpecs has no (std/tasks, Task) row, so no Task<T> forms and taskHandleKind panics")
	}
	if want := []string{"T"}; !slices.Equal(taskSpec.params, want) {
		t.Errorf("the row declares params %v, want %v — genHostNames writes the argument list from them", taskSpec.params, want)
	}
	// NO BOUND, asserted rather than assumed: `bounds: nil` demands a
	// declaration with no constraint at all, and a std edit adding one must
	// produce no anchor rather than lower past a constraint nothing discharges.
	if taskSpec.bounds != nil {
		t.Error("the Task row declares a bound; nothing in this builder discharges one")
	}
	// AND THE END-TO-END POSITIVE: a program mentioning `Task<Int>` gets a kind,
	// which the three checks above do not establish between them.
	g := &gen{}
	handle := g.taskHandleKind(kindInt)
	if got := handle.nomi(); got != "Task<Int>" {
		t.Errorf("Task<Int> renders as %q, want rt.Task[int64]", got)
	}
	if payload, isTask := taskElem(handle); !isTask || payload != kindInt {
		t.Errorf("taskElem(Task<Int>) = (%v, %v), want (Int, true)", payload.nomi(), isTask)
	}
	_ = g
}

// --- the annotated channel constructor ---------------------------------------

// TestChannel_BufferedCapacityIsAnInt reads STD's own declaration, because
// channelCtorFromTarget lowers the capacity as an Int (it takes the row's
// arity and refuses an argument of any other kind) rather than from the
// checker's answer. The capacity is declared PositiveInt, a subtype whose
// values are Ints: its base is Int and the builder erases it to kindInt,
// so an annotated `Channel.buffered(n)` passes the argument through.
//
// A std edit changing the capacity's type to one that is not an Int must
// fail HERE, where the assumption is written, rather than send the wrong
// kind at a call site.
func TestChannel_BufferedCapacityIsAnInt(t *testing.T) {
	decl := stdDeclOf(t, "std/channels", "Channel", "buffered")
	if decl == nil {
		t.Fatal("std/channels declares no Channel.buffered, so channelFuncs' row names nothing")
	}
	if len(decl.Params) != 1 {
		t.Fatalf("Channel.buffered declares %d parameter(s); channelFuncs says 1", len(decl.Params))
	}
	name := analysis.TypeExprBaseName(decl.Params[0].TypeAnnotation)
	sym := resolvedTypeSymbol(std.Load().Files["channels"], name)
	if sym == nil || sym.Type == nil {
		t.Fatalf("std/channels resolves no type %q for Channel.buffered's capacity", name)
	}
	if base := analysis.RefinementBase(sym.Type); base.String() != "Int" {
		t.Errorf("Channel.buffered's capacity is declared %s, whose base is %s; channelCtorFromTarget lowers it as an Int", name, base)
	}
	if k := (&gen{}).project(sym.Type); k != kindInt {
		t.Errorf("Channel.buffered's capacity, %s, lowers as %s; channelCtorFromTarget refuses all but Int", name, k.nomi())
	}
}

// stdDeclOf is the `pub host fn` std declares at `<owner>.<method>`, or nil.
//
// Read out of `std.Load()`, the embedded copy the compiler uses, rather than
// off disk, so the assertion is about the std that ships and not about a
// working tree.
func stdDeclOf(t *testing.T, module, owner, method string) *ast.ExternFunc {
	t.Helper()
	file := strings.TrimPrefix(module, "std/")
	lib := std.Load()
	nodes := lib.Nodes[file]
	if nodes == nil {
		t.Fatalf("std/%s did not load", file)
	}
	for _, n := range nodes {
		impl, isImpl := n.(*ast.ImplBlock)
		if !isImpl || impl.Interface != nil || analysis.TypeExprBaseName(impl.Receiver) != owner {
			continue
		}
		for _, item := range impl.Items {
			if fn, isExtern := item.(*ast.ExternFunc); isExtern && fn.Name == method {
				return fn
			}
		}
	}
	return nil
}
