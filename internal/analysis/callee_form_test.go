package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A call of an interface function records how it is answered
// (Callee.Form), in the terms an operator through the interface records
// (OperatorUse.Form): through the block it reaches, structurally for a
// tuple, an anonymous struct, a function and a declared type with no
// Equatable or Hashable of its own, by dispatch on an interface value, and
// per instance through a type parameter, which CalleeAt answers at an
// instance's types. Where the operator form of the same operation on the
// same type has a record, the two agree.
func TestCalleeForm_RecordsHowAnInterfaceCallIsAnswered(t *testing.T) {
	fa, errs := checkEntry(t, `import std::json.{Json, ToJson}

struct Plain
    n: Int
end

struct Own
    n: Int
end

impl Equatable for Own
    fn equal(a: Own, b: Own) -> Bool
        True
    end
end

impl Hashable for Own
    fn hash(a: Own) -> Int
        1
    end
end

fn tuple_hash(t: (Int, String)) -> Int
    Hashable.hash(t)
end

fn tuple_equal(t: (Int, String)) -> List<Bool>
    [Equatable.equal(t, t), t == t]
end

fn tuple_compare(t: (Int, String)) -> Ordering
    Comparable.compare(t, t)
end

fn tuple_debug(t: (Int, String)) -> String
    Debug.inspect(t)
end

fn tuple_display(t: (Int, String)) -> String
    Display.to_string(t)
end

fn record_hash(r: {a: Int, b: String}) -> Int
    Hashable.hash(r)
end

fn record_json(r: {a: Int, b: String}) -> Json
    ToJson.to_json(r)
end

fn record_equal(r: {a: Int, b: String}) -> List<Bool>
    [Equatable.equal(r, r), r == r]
end

fn list_equal(xs: List<Int>) -> Bool
    Equatable.equal(xs, xs)
end

fn scalar_equal(n: Int) -> Bool
    Equatable.equal(n, n)
end

fn own_equal(o: Own) -> List<Bool>
    [Equatable.equal(o, o), o == o]
end

fn own_hash(o: Own) -> Int
    Hashable.hash(o)
end

fn plain_equal(p: Plain) -> List<Bool>
    [Equatable.equal(p, p), p == p]
end

fn plain_hash(p: Plain) -> Int
    Hashable.hash(p)
end

fn held_show(a: Display) -> String
    Display.to_string(a)
end

fn hashed<T>(x: T) -> Int where T: Hashable
    Hashable.hash(x)
end

fn equal_at<T>(a: T, b: T) -> Bool where T: Equatable
    Equatable.equal(a, b)
end

fn uses(p: Plain, o: Own) -> (List<Int>, Bool)
    ([hashed((1, "a")), hashed(p), hashed(o)], equal_at(p, p))
end
`)
	if len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	byLine := map[int]analysis.Callee{}
	for n, rc := range fa.Calls {
		if call, ok := n.(*ast.Call); ok {
			byLine[call.Line] = rc.Callee
		}
	}
	opByLine := map[int]*analysis.OperatorUse{}
	for n, use := range fa.Operators {
		if b, ok := n.(*ast.Binary); ok && b.Op == "==" {
			opByLine[b.Line] = use
		}
	}
	form := func(line int, what string, want analysis.OperatorForm) analysis.Callee {
		t.Helper()
		c, ok := byLine[line]
		if !ok {
			t.Fatalf("%s: no call recorded on line %d", what, line)
		}
		if c.Form != want {
			t.Errorf("%s: Form %d, want %d (kind %d, For %v)", what, c.Form, want, c.Kind, c.For)
		}
		if op := opByLine[line]; op != nil && op.Form != c.Form {
			t.Errorf("%s: the call's Form %d, the operator's %d", what, c.Form, op.Form)
		}
		return c
	}
	form(24, "Hashable.hash of a tuple", analysis.OperatorStructural)
	form(28, "Equatable.equal of a tuple", analysis.OperatorStructural)
	form(32, "Comparable.compare of a tuple", analysis.OperatorStructural)
	form(36, "Debug.inspect of a tuple", analysis.OperatorStructural)
	form(40, "Display.to_string of a tuple", analysis.OperatorStructural)
	form(44, "Hashable.hash of a record", analysis.OperatorStructural)
	form(48, "ToJson.to_json of a record", analysis.OperatorStructural)
	form(52, "Equatable.equal of a record", analysis.OperatorStructural)
	if c := form(56, "Equatable.equal of a List", analysis.OperatorByImpl); c.Impl == nil {
		t.Errorf("Equatable.equal of a List: no block")
	}
	form(60, "Equatable.equal of an Int", analysis.OperatorByImpl)
	if c := form(64, "Equatable.equal of a type with an impl", analysis.OperatorByImpl); c.Impl == nil {
		t.Errorf("Equatable.equal of a type with an impl: no block")
	}
	form(68, "Hashable.hash of a type with an impl", analysis.OperatorByImpl)
	form(72, "Equatable.equal of a type with no impl", analysis.OperatorStructural)
	form(76, "Hashable.hash of a type with no impl", analysis.OperatorStructural)
	form(80, "Display.to_string of an interface value", analysis.OperatorDispatched)
	hashed := form(84, "Hashable.hash through a type parameter", analysis.OperatorPerInstance)
	equalAt := form(88, "Equatable.equal through a type parameter", analysis.OperatorPerInstance)

	// The generic bodies at the instances `uses` makes.
	at := func(c analysis.Callee, ty analysis.Type) analysis.Callee {
		return fa.CalleeAt(c, func(*analysis.TypeParam_) (analysis.Type, bool) { return ty, true })
	}
	use := byLine[92]
	if use.Ref == nil {
		t.Fatalf("no record of the generic's call on line 92")
	}
	var tupleT, plainT, ownT analysis.Type
	for n, rc := range fa.Calls {
		call, ok := n.(*ast.Call)
		if !ok || call.Line != 92 || rc.Callee.Ref == nil {
			continue
		}
		ty := rc.Callee.Ref.CallTypeArgs["T"]
		switch {
		case ty == nil:
		case ty.String() == "Plain":
			plainT = ty
		case ty.String() == "Own":
			ownT = ty
		default:
			if _, isTuple := analysis.ResolveTypeVar(ty).(*analysis.TupleType); isTuple {
				tupleT = ty
			}
		}
	}
	if tupleT == nil || plainT == nil || ownT == nil {
		t.Fatalf("the instances' types: tuple %v, Plain %v, Own %v", tupleT, plainT, ownT)
	}
	for _, c := range []struct {
		what string
		got  analysis.Callee
		want analysis.OperatorForm
	}{
		{"Hashable.hash at a tuple", at(hashed, tupleT), analysis.OperatorStructural},
		{"Hashable.hash at a type with no impl", at(hashed, plainT), analysis.OperatorStructural},
		{"Hashable.hash at a type with an impl", at(hashed, ownT), analysis.OperatorByImpl},
		{"Equatable.equal at a type with no impl", at(equalAt, plainT), analysis.OperatorStructural},
	} {
		if c.got.Form != c.want {
			t.Errorf("%s: Form %d, want %d", c.what, c.got.Form, c.want)
		}
		if c.got.For == nil || analysis.ContainsTypeParam(c.got.For) {
			t.Errorf("%s: For %v, want the instance's type", c.what, c.got.For)
		}
	}
	// InterfaceCallee, the record of a call the builder writes, answers as
	// a written call does.
	for _, c := range []struct {
		what string
		recv analysis.Type
		want analysis.OperatorForm
	}{
		{"a tuple", tupleT, analysis.OperatorStructural},
		{"a type with no impl", plainT, analysis.OperatorStructural},
		{"a type with an impl", ownT, analysis.OperatorByImpl},
	} {
		got := fa.InterfaceCallee(analysis.StdType{Module: "std/hashable", Name: "Hashable"}, "hash", c.recv)
		if got.Form != c.want {
			t.Errorf("InterfaceCallee Hashable.hash at %s: Form %d, want %d", c.what, got.Form, c.want)
		}
	}
}

// A declared type with no block of the interface is answered by the rule
// an operator through it has (noImplUse): `Struct.update` on any struct
// structurally, and `Debug.inspect` of a type declared inside a body that
// is not opaque by its shape, which has no block, even where another
// module declares a type of the same name with its own Debug (std/xml's
// Token).
func TestCalleeForm_ADeclaredTypeWithNoBlock(t *testing.T) {
	fa, errs := checkEntry(t, `struct P
    x: Int
end

fn moved(p: P) -> P
    Struct.update(p, {x: 2})
end

fn shown() -> String
    type Token String
    Debug.inspect(Token("x"))
end
`)
	if len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	got := map[int]analysis.Callee{}
	var token ast.Node
	for n, rc := range fa.Calls {
		if call, ok := n.(*ast.Call); ok && (call.Line == 6 || call.Line == 11) && rc.Callee.Method != "" {
			got[call.Line] = rc.Callee
			if call.Line == 11 {
				token = call.Args[0]
			}
		}
	}
	if c := got[6]; c.Kind != analysis.CalleeInterface || c.Form != analysis.OperatorStructural {
		t.Errorf("Struct.update on a struct: Kind %d Form %d, want an interface's function answered structurally", c.Kind, c.Form)
	}
	if len(fa.BlockDebug) != 0 || token == nil {
		t.Fatalf("the front end wrote %d universal Debug bodies for the block-local Token, or the checker recorded no Debug.inspect", len(fa.BlockDebug))
	}
	if c := got[11]; c.Kind != analysis.CalleeInterface || c.Form != analysis.OperatorStructural {
		t.Errorf("Debug.inspect of a block-local type: Kind %d Form %d, want an interface's function answered structurally", c.Kind, c.Form)
	}
	if c := fa.InterfaceCallee(analysis.StdType{Module: "std/debug", Name: "Debug"}, "inspect", fa.ExprTypes[token]); c.Form != analysis.OperatorStructural {
		t.Errorf("InterfaceCallee Debug.inspect at the block-local type: Form %d, want structural", c.Form)
	}
}

// A hand-written `impl Debug` outranks the universal one the front end
// synthesizes, also when another file of the program writes it: the record
// of `Debug.inspect` at the type, written or asked for by the builder
// (InterfaceCallee), names the written block.
func TestCalleeForm_AWrittenDebugOutranksTheSynthesizedOne(t *testing.T) {
	fa, errs := checkProjectWithFiles(t, `import extra.{self}

fn shown() -> String
    Debug.inspect(extra.boxed(5))
end
`, map[string]string{
		"parts.nomi": "pub struct Box\n    w: Int\nend\n",
		"extra.nomi": `import parts.Box

impl Debug for Box
    fn inspect(x: Box) -> String
        "written"
    end
end

pub fn boxed(w: Int) -> Box
    Box{w:}
end
`,
	})
	if len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	written := func(what string, c analysis.Callee) {
		t.Helper()
		line := 0
		if c.Decl != nil {
			line = c.Decl.LineNum()
		}
		if c.Kind != analysis.CalleeImpl || line != 4 {
			t.Errorf("%s: Kind %d at line %d, want the written impl's `fn inspect` at line 4", what, c.Kind, line)
		}
	}
	var box analysis.Type
	for n, rc := range fa.Calls {
		if call, ok := n.(*ast.Call); ok && call.Line == 4 && rc.Callee.Method == "inspect" {
			written("the written call", rc.Callee)
			box = rc.Callee.For
		}
	}
	if box == nil {
		t.Fatal("no record of `Debug.inspect` on line 4 names the type it is for")
	}
	written("InterfaceCallee", fa.InterfaceCallee(analysis.StdType{Module: "std/debug", Name: "Debug"}, "inspect", box))
}
