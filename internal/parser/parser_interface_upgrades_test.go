package parser

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"strings"
	"testing"
)

// Interface upgrades v1: parser-side coverage for
//   - `field name: Type` requirements inside an interface body
//   - `open fn ...` modifier on default methods
//   - `impl Iface for Type { … }` blocks (the sole interface-impl form;
//     kept brief here)

func TestInterfaceFieldRequirement(t *testing.T) {
	nodes := parse(t, `pub interface App {
  field context: Context
}`)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	idef := nodes[0].(*ast.InterfaceDef)
	if !idef.Public {
		t.Error("expected pub interface")
	}
	if len(idef.Methods) != 0 {
		t.Errorf("expected 0 methods, got %d", len(idef.Methods))
	}
	if len(idef.Fields) != 1 {
		t.Fatalf("expected 1 field requirement, got %d", len(idef.Fields))
	}
	f := idef.Fields[0]
	if f.Name != "context" {
		t.Errorf("expected field name 'context', got %q", f.Name)
	}
	if f.TypeAnnotation == nil || f.TypeAnnotation.TypeString() != "Context" {
		t.Errorf("expected field type Context, got %v", f.TypeAnnotation)
	}
}

func TestInterfaceMixedFieldsAndMethods(t *testing.T) {
	nodes := parse(t, `interface AppEnv {
  field context: Context
  field port: Int
  fn name(value: self): String
}`)
	idef := nodes[0].(*ast.InterfaceDef)
	if len(idef.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(idef.Fields))
	}
	if len(idef.Methods) != 1 {
		t.Fatalf("expected 1 method, got %d", len(idef.Methods))
	}
	if idef.Fields[0].Name != "context" || idef.Fields[1].Name != "port" {
		t.Errorf("unexpected field names: %v", idef.Fields)
	}
}

func TestInterfaceFieldRequiresType(t *testing.T) {
	src := `interface Bad {
  field context
}`
	tokens := lexer.Lex(src)
	_, err := Parse(tokens)
	if err == nil {
		t.Fatal("expected parse error for field without type annotation, got nil")
	}
	if !strings.Contains(err.Error(), "':'") && !strings.Contains(err.Error(), "after field name") {
		t.Errorf("expected error about missing colon/type, got %q", err.Error())
	}
}

func TestInterfaceOpenDefault(t *testing.T) {
	nodes := parse(t, `interface MyIface {
  fn required_method(value: self): Int
  fn final_default(_value: self): String { "fixed" }
  open fn extension_point(_value: self): String { "hi" }
}`)
	idef := nodes[0].(*ast.InterfaceDef)
	if len(idef.Methods) != 3 {
		t.Fatalf("expected 3 methods, got %d", len(idef.Methods))
	}
	if idef.Methods[0].Open {
		t.Error("required_method should not be Open")
	}
	if idef.Methods[1].Open {
		t.Error("final_default should not be Open")
	}
	if !idef.Methods[2].Open {
		t.Error("extension_point should be Open")
	}
}

// A bodyless `host fn` in an interface body is a host-backed default — a
// contract method (in Methods), provided by the host to every implementor,
// not an inherent op (Items) and not a required method. `open host fn` is
// the overridable variant.
func TestInterfaceHostBackedDefault(t *testing.T) {
	nodes := parse(t, `interface Seq {
  fn next(value: self): Int
  host fn reduce(value: self): Int
  open host fn sort(value: self): Int
}`)
	idef := nodes[0].(*ast.InterfaceDef)
	if len(idef.Methods) != 3 {
		t.Fatalf("expected 3 contract methods (host defaults included), got %d", len(idef.Methods))
	}
	// next: required (no body, not extern)
	if idef.Methods[0].Extern || idef.Methods[0].Body != nil {
		t.Errorf("next should be a plain required method")
	}
	// reduce: host-backed default (extern, not open)
	if !idef.Methods[1].Extern {
		t.Errorf("reduce should be Extern (host-backed default)")
	}
	if idef.Methods[1].Open {
		t.Errorf("reduce (plain extern) should not be Open")
	}
	// sort: open host-backed default (extern + open)
	if !idef.Methods[2].Extern || !idef.Methods[2].Open {
		t.Errorf("sort should be Extern and Open (open host-backed default)")
	}
}

// `pub` is not allowed on interface body items — visibility is the
// interface's. (The inherent-op category it used to mark is gone; ops are
// defaults, host defaults are `host fn`.)
func TestInterfacePubItemRejected(t *testing.T) {
	for _, src := range []string{
		"interface I {\n  pub fn foo(x: self): Int { 1 }\n}",
		"interface I {\n  pub host fn foo(x: self): Int\n}",
	} {
		tokens := lexer.Lex(src)
		_, err := Parse(tokens)
		if err == nil {
			t.Errorf("expected parse error for `pub` in interface body, got nil for:\n%s", src)
			continue
		}
		if !strings.Contains(err.Error(), "pub") || !strings.Contains(err.Error(), "interface") {
			t.Errorf("expected error about pub in interface, got %q", err.Error())
		}
	}
}

func TestInterfaceOpenRequiresDefault(t *testing.T) {
	src := `interface Bad {
  open fn no_body(value: self): Int
}`
	tokens := lexer.Lex(src)
	_, err := Parse(tokens)
	if err == nil {
		t.Fatal("expected parse error for open without default body, got nil")
	}
	if !strings.Contains(err.Error(), "open") || !strings.Contains(err.Error(), "default") {
		t.Errorf("expected error mentioning 'open' and 'default', got %q", err.Error())
	}
}

// Contextual-keyword pin: `field` and `open` outside an interface body
// remain regular identifiers — struct fields named `field` parse, local
// bindings named `open` parse.
func TestFieldOpenAreContextualKeywords(t *testing.T) {
	nodes := parse(t, `struct Box { field: Int }
fn make(): Int {
  open = 42
  open
}`)
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
	sd, ok := nodes[0].(*ast.StructDef)
	if !ok {
		t.Fatalf("expected StructDef, got %T", nodes[0])
	}
	if len(sd.Fields) != 1 || sd.Fields[0].Name != "field" {
		t.Errorf("expected field 'field', got %+v", sd.Fields)
	}
}
