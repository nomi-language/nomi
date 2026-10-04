package irbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

const irValTypeSource = `import std/io

struct Tag {
  name: String
}

struct Node {
  value: Int
  tag: Tag
}

enum Shape {
  Dot
  Circle Float
  Rect {w: Float, h: Float}
}

fn area(s: Shape): Float {
  case s {
    .Dot -> 0.0
    .Circle(r) -> r * r
    .Rect{w, h} -> w * h
  }
}

fn total(n: Node): Int {
  n.value + 1
}

fn describe(s: Shape): String {
  a = area(s)
  "area ${a}"
}

fn main() {
  io.print(describe(Shape.Circle(2.0)))
  io.print(total(Node{value: 1, tag: Tag{name: "a"}}))
}
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
