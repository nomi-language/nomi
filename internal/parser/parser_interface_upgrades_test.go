package parser

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"strings"
	"testing"
)

// Interface upgrades v1: parser-side coverage for
//   - `field` items, which an interface body refuses
//   - `open fn ...` modifier on default methods
//   - `impl Iface for Type { … }` blocks (the sole interface-impl form;
//     kept brief here)

// An interface declares functions only. A `field` item is refused with a
// message that spells the function requirement replacing it, and the test
// fails if the parser ever admits one again.
func TestInterfaceFieldItemIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
	}{
		{
			"a well-formed field item",
			"pub interface Named {\n  field name: String\n}",
			"line 2, col 3: interfaces declare functions only; replace `field name: String` with a function requirement such as `fn name(value: self): String`",
		},
		{
			"a generic field type, beside a function",
			"interface Container<T> {\n  fn size(value: self): Int\n  field items: List<T>\n}",
			"line 3, col 3: interfaces declare functions only; replace `field items: List<T>` with a function requirement such as `fn items(value: self): List<T>`",
		},
		{
			"a field item with no type",
			"interface Bad {\n  field context\n}",
			"line 2, col 3: interfaces declare functions only; replace `field name: String` with a function requirement such as `fn name(value: self): String`",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(lexer.Lex(tc.src))
			if err == nil {
				t.Fatalf("the parser admits a `field` item in an interface body:\n%s", tc.src)
			}
			if err.Error() != tc.want {
				t.Errorf("error = %q\nwant    %q", err.Error(), tc.want)
			}
		})
	}
}

// `field` is an ordinary identifier outside an interface body.
func TestFieldIsAnOrdinaryIdentifier(t *testing.T) {
	nodes := parse(t, "struct Box {\n  field: Int\n}\nfn f(field: Int): Int {\n  field + 1\n}")
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
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
