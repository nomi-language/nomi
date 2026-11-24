package irbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

const irValTypeSource = `import std::io

struct Tag
  name: String
end

struct Node
  value: Int
  tag: Tag
end

enum Shape
  Dot
  Circle Float
  Rect {w: Float, h: Float}
end

fn area(s: Shape) -> Float
  case s
    .Dot -> 0.0
    .Circle(r) -> r * r
    .Rect{w:, h:} -> w * h
  end
end

fn total(n: Node) -> Int
  n.value + 1
end

fn describe(s: Shape) -> String
  a = area(s)
  "area ${a}"
end

fn main()
  io.print(describe(Shape.Circle(2.0)))
  io.print(total(Node{value: 1, tag: Tag{name: "a"}}))
end
`

// irRetainedFuncs lowers src as main.nomi and answers its retained functions
// by name.
func irRetainedFuncs(t *testing.T, src string) map[string]*ir.Func {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*ir.Func{}
	for _, m := range res.IR {
		if m.Name() != path {
			continue
		}
		for _, f := range m.Funcs() {
			out[f.Name()] = f
		}
	}
	return out
}

// TestIRValType_EveryTemporaryCarriesTheCheckersType: a call's result, a
// parameter and a projection each come out typed, and a declared type carries
// the layout a record descriptor is built from.
func TestIRValType_EveryTemporaryCarriesTheCheckersType(t *testing.T) {
	fns := irRetainedFuncs(t, irValTypeSource)
	for _, name := range []string{"area", "total", "describe"} {
		if fns[name] == nil {
			t.Fatalf("%s was not retained; retained: %v", name, fns)
		}
	}

	describe := fns["describe"]
	var callResult *ir.ValType
	for _, b := range describe.Blocks() {
		for _, in := range b.Instrs() {
			if c, isCall := in.(*ir.Call); isCall && strings.HasSuffix(c.Callee().Name(), "area") {
				callResult = describe.TempType(c.Dst())
			}
		}
	}
	if callResult != ir.FloatType {
		t.Fatalf("the result of calling area is typed %v, want Float", callResult)
	}

	shape := fns["area"].TempType(fns["area"].Params()[0].Temp)
	if shape == nil || shape.Kind() != ir.KindEnum || shape.Layout() == nil {
		t.Fatalf("area's parameter is typed %v with layout %v, want the Shape enum with its variants",
			shape, shape.Layout())
	}
	var forms []string
	for _, v := range shape.Layout().Variants {
		forms = append(forms, v.Name+":"+v.Form.String())
	}
	if got := strings.Join(forms, " "); got != "Dot:bare Circle:positional Rect:fields" {
		t.Fatalf("Shape's variants are %q", got)
	}
	if rect := shape.Layout().Variants[2]; rect.Fields[0].Name != "w" || rect.Fields[1].Type != ir.FloatType {
		t.Fatalf("Rect's payload fields are %+v", rect.Fields)
	}

	node := fns["total"].TempType(fns["total"].Params()[0].Temp)
	if node == nil || node.Kind() != ir.KindStruct || node.Layout() == nil {
		t.Fatalf("total's parameter is typed %v, want the Node struct with its fields", node)
	}
	tag := node.Layout().Fields[1].Type
	if tag.Kind() != ir.KindStruct || tag.Layout().Fields[0].Type != ir.StringType {
		t.Fatalf("Node.tag is %v, want the Tag struct with its String field", tag)
	}
	if node.Class() != ir.ClassRef || ir.FloatType.Class() != ir.ClassWord {
		t.Fatal("register classes are wrong")
	}
}

// A declared type that holds itself through a List, a Map or a tuple states
// that field's type, whichever the builder maps first: here `values` is a
// List<Value> before any Value is typed alone, and the layout Value states
// under it holds the List, the Map and the tuple again, never the erased
// type. The reachability walk reads these fields for the bodies a value may
// run (vm's heldTypes), and an erased one stands for every body.
func TestIRValType_ASelfHoldingTypeStatesItsFields(t *testing.T) {
	const src = `import std::io

enum Value
  Leaf Int
  Array List<Value>
  Table Map<String, Value>
  Pair (Value, Value)
end

fn count(values: List<Value>) -> Int
  Iter.count(values)
end

fn main()
  io.print(count([Value.Leaf(1)]))
end
`
	fns := irRetainedFuncs(t, src)
	count := fns["count"]
	if count == nil {
		t.Fatalf("count was not retained; retained: %v", fns)
	}
	list := count.TempType(count.Params()[0].Temp)
	if list == nil || list.Kind() != ir.KindList {
		t.Fatalf("count's parameter is typed %v, want a List", list)
	}
	value := list.Elem(0)
	if value.Kind() != ir.KindEnum || value.Layout() == nil {
		t.Fatalf("the List's element is %v with layout %v, want the Value enum with its variants", value, value.Layout())
	}
	for _, v := range value.Layout().Variants[1:] {
		held := v.Fields[0].Type
		if held.Kind() == ir.KindAny {
			t.Errorf("Value.%s holds the erased type", v.Name)
			continue
		}
		for i := range held.NumElems() {
			if e := held.Elem(i); e.Kind() == ir.KindAny {
				t.Errorf("Value.%s is %v, whose part %d is erased", v.Name, held, i)
			} else if e.Kind() == ir.KindEnum && e != value {
				t.Errorf("Value.%s holds a Value typed apart from the one it is in", v.Name)
			}
		}
	}
}
