package irbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Each call's record classifies its callee by the declaration the checker
// resolved, whatever the call's spelling (analysis.Callee).
func TestCallee_EachCallIsClassified(t *testing.T) {
	src := `interface Shown
    fn show(value: self) -> String
end

type Id Int

enum Colour
    Red
    Mix(Int)
end

struct Point
    x: Int
end

impl Shown for Point
    fn show(value: Point) -> String
        "p${value.x}"
    end
end

fn sub(a: Int, b: Int = 1) -> Int
    a - b
end

fn each<T>(value: T, f: (Int) -> Int) -> String
    where T: Shown
    g = sub
    n = g(f(2))
    _ = Id(n)
    _ = Colour.Mix(n)
    _ = Point({x: n})
    _ = sub(n, 1)
    _ = String.length("ab")
    _ = Shown.show(Point{x: 1})
    T.show(value)
end

fn main()
    _ = each(Point{x: 1}, fn n -> n)
end
`
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatal(err)
	}
	var fa *analysis.FileAnalysis
	for _, m := range p.Modules {
		if m.Path == path {
			fa = m.FA
		}
	}
	if fa == nil {
		t.Fatal("the program holds no analysis of its entry")
	}
	want := map[string]analysis.CalleeKind{
		"g":             analysis.CalleeValue,
		"f":             analysis.CalleeValue,
		"Id":            analysis.CalleeConstructor,
		"Colour.Mix":    analysis.CalleeVariant,
		"Point":         analysis.CalleeConstructor,
		"sub":           analysis.CalleeFunction,
		"String.length": analysis.CalleeHost,
		"Shown.show":    analysis.CalleeImpl,
		"T.show":        analysis.CalleeInterface,
	}
	got := map[string]analysis.CalleeKind{}
	var subDecl, copyOfG ast.Node
	for n, rc := range fa.Calls {
		call, ok := n.(*ast.Call)
		if !ok {
			continue
		}
		name := calleeSpelling(call.Func)
		got[name] = rc.Callee.Kind
		switch name {
		case "sub":
			subDecl = rc.Callee.Decl
		case "g":
			copyOfG = rc.Callee.Decl
			if id, isIdent := rc.Callee.Copy.(*ast.Ident); !isIdent || id.Name != "sub" {
				t.Errorf("the call through `g` names the expression it copies as %T, want the name `sub`", rc.Callee.Copy)
			}
		}
	}
	for name, kind := range want {
		if got[name] != kind {
			t.Errorf("%s is classified %d, want %d", name, got[name], kind)
		}
	}
	if subDecl == nil || copyOfG != subDecl {
		t.Errorf("the call through `g`, a copy of `sub`, names the declaration %p, want sub's %p", copyOfG, subDecl)
	}
}

func calleeSpelling(n ast.Node) string {
	switch f := n.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.TypeIdent:
		return f.Name
	case *ast.FieldAccess:
		if f.Field != nil {
			return strings.TrimPrefix(calleeSpelling(f.Object)+"."+f.Field.Name, ".")
		}
	}
	return ""
}
