package analysis

import (
	"strconv"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

const coercionSrc = `interface Show
    fn show(v: self) -> Int
end

struct P
    x: Int
end

impl Show for P
    fn show(v: P) -> Int
        v.x
    end
end

impl Show for List<Int>
    fn show(_v: List<Int>) -> Int
        1
    end
end

struct Holder
    item: Show
end

fn take(s: Show) -> Int
    Show.show(s)
end

fn bounded<T>(v: T) -> Int where T: Show
    take(v)
end

fn run() -> Int
    held: Show = P{x: 1}
    h = Holder{item: [2]}
    take(P{x: 3}) + take([4]) + Show.show(P{x: 5}) + bounded(P{x: 6}) + take(held) + take(h.item)
end
`

// TestCoercions_RecordWhatEntersAnInterface: an argument, an annotated
// binding's value, a struct field's value and a bounded type parameter's
// value entering an interface each have a record naming the interface, the
// value's type and, for a List<Int>, whose name does not select its impl,
// the impl block; the receiver of an interface call and a value that is an
// interface value already have none.
func TestCoercions_RecordWhatEntersAnInterface(t *testing.T) {
	fa, nodes := checkNodes(t, coercionSrc)
	impls := map[string]*ast.ImplBlock{}
	for _, n := range nodes {
		if ib, ok := n.(*ast.ImplBlock); ok && ib.Receiver != nil {
			impls[ib.Receiver.TypeString()] = ib
		}
	}
	// The expressions the test looks at: each argument of `take` and of
	// `Show.show`, the annotated binding's value and the struct field's.
	exprs := map[string]ast.Node{}
	for _, name := range []string{"take", "Show.show"} {
		for _, call := range elabCalls(nodes, name) {
			exprs[name+"("+coercionText(call.Args[0])+")"] = call.Args[0]
		}
	}
	for _, top := range nodes {
		ast.Inspect(top, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.Binding:
				if n.TypeAnnotation != nil {
					exprs["binding "+coercionText(n.Value)] = n.Value
				}
			case *ast.StructLit:
				if n.TypeName != nil && n.TypeName.TypeString() == "Holder" {
					exprs["field "+coercionText(n.Fields[0].Value)] = n.Fields[0].Value
				}
			}
			return true
		})
	}
	type want struct {
		value string
		impl  *ast.ImplBlock
	}
	// An impl whose receiver's name selects it is not recorded (P's).
	wants := map[string]*want{
		"binding P{x: 1}":    {"P", nil},
		"field [2]":          {"List<Int>", impls["List<Int>"]},
		"take(P{x: 3})":      {"P", nil},
		"take([4])":          {"List<Int>", impls["List<Int>"]},
		"take(v)":            {"T", nil},
		"take(held)":         nil,
		"take(h.item)":       nil,
		"Show.show(P{x: 5})": nil,
		"Show.show(s)":       nil,
	}
	for text, w := range wants {
		n := exprs[text]
		if n == nil {
			t.Errorf("no expression %s in the source", text)
			continue
		}
		rec := fa.Coercions[n]
		switch {
		case w == nil && rec != nil:
			t.Errorf("%s: recorded a coercion into %s, want none", text, rec.Iface)
		case w == nil:
		case rec == nil:
			t.Errorf("%s: no coercion recorded", text)
		case rec.Iface == nil || rec.Iface.Name != "Show":
			t.Errorf("%s: recorded into %v, want Show", text, rec.Iface)
		case rec.Value.String() != w.value:
			t.Errorf("%s: recorded value type %s, want %s", text, rec.Value, w.value)
		case rec.Impl != w.impl:
			t.Errorf("%s: recorded impl %p, want %p", text, rec.Impl, w.impl)
		}
	}
	if got := VerifyElaboration(fa, nodes); len(got) > 0 {
		t.Fatalf("want nothing reported, got %v", got)
	}
}

// TestVerifyElaboration_ReportsAnUnrecordedCoercion removes the record of
// `take(P{x: 3})`'s argument and holds the verifier to reporting it.
func TestVerifyElaboration_ReportsAnUnrecordedCoercion(t *testing.T) {
	fa, nodes := checkNodes(t, coercionSrc)
	var arg ast.Node
	for _, call := range elabCalls(nodes, "take") {
		if coercionText(call.Args[0]) == "P{x: 3}" {
			arg = call.Args[0]
		}
	}
	if arg == nil || fa.Coercions[arg] == nil {
		t.Fatal("no recorded argument P{x: 3} in the source")
	}
	delete(fa.Coercions, arg)
	if got := VerifyElaboration(fa, nodes); !hasElabCategory(got, arg, ElabBoxingUnrecorded) {
		t.Fatalf("an unrecorded coercion of P{x: 3} is not reported: %v", got)
	}
}

// coercionText is n written out, for the expressions the tests name.
func coercionText(n ast.Node) string {
	switch n := n.(type) {
	case *ast.StructLit:
		if len(n.Fields) == 1 && n.TypeName != nil {
			if lit, ok := n.Fields[0].Value.(*ast.IntLit); ok {
				return n.TypeName.TypeString() + "{" + n.Fields[0].Name + ": " + strconv.FormatInt(lit.Value, 10) + "}"
			}
		}
	case *ast.ListLit:
		if len(n.Items) == 1 {
			if lit, ok := n.Items[0].(*ast.IntLit); ok {
				return "[" + strconv.FormatInt(lit.Value, 10) + "]"
			}
		}
	case *ast.Ident:
		return n.Name
	case *ast.FieldAccess:
		if obj, ok := n.Object.(*ast.Ident); ok && n.Field != nil {
			return obj.Name + "." + n.Field.Name
		}
	}
	return ""
}
