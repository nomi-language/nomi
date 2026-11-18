package std

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// TestInterfaceCallee: the record of a call of a std interface's function
// the IR builder writes is what the checker records for the call written
// at the value's type. An Int's `Hashable.hash` reaches std/int's block and
// its function; a tuple's, which no block answers, names the interface's
// function with the tuple as For; with no type it names the function and
// no type.
func TestInterfaceCallee(t *testing.T) {
	lib := All()
	fa := lib.Files["json"]
	if fa == nil {
		t.Fatal("std has no module json")
	}
	hashable := analysis.StdType{Module: "std/hashable", Name: "Hashable"}

	at := fa.InterfaceCallee(hashable, "hash", analysis.TypeInt)
	if at.Kind != analysis.CalleeImpl || at.Impl == nil {
		t.Fatalf("Hashable.hash at Int: kind %v, impl %v; want the impl block std/int writes", at.Kind, at.Impl)
	}
	if fd, ok := at.Decl.(*ast.FuncDef); !ok || fd.Name != "hash" {
		t.Errorf("Hashable.hash at Int: Decl %T, want the block's `fn hash`", at.Decl)
	}
	if at.Std.Module != "std/int" {
		t.Errorf("Hashable.hash at Int: Std %+v, want std/int's function", at.Std)
	}

	tuple := &analysis.TupleType{Elems: []analysis.Type{analysis.TypeInt, analysis.TypeString}}
	for _, recv := range []analysis.Type{tuple, nil} {
		c := fa.InterfaceCallee(hashable, "hash", recv)
		if c.Kind != analysis.CalleeInterface || c.Impl != nil || c.For != recv {
			t.Errorf("Hashable.hash at %v: kind %v, impl %v, For %v; want the interface's function for it", recv, c.Kind, c.Impl, c.For)
		}
		if _, ok := c.Decl.(*ast.InterfaceMethod); !ok || c.Qualifier.Std != hashable {
			t.Errorf("Hashable.hash at %v: Decl %T, qualifier %+v; want Hashable's `hash`", recv, c.Decl, c.Qualifier)
		}
	}
}
