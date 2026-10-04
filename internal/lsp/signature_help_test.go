package lsp

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

func TestFindCallContext(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		line, char int
		wantFunc   string
		wantParam  int
	}{
		{
			name:      "just after open paren",
			content:   `add(`,
			line:      0,
			char:      4,
			wantFunc:  "add",
			wantParam: 0,
		},
		{
			name:      "first arg",
			content:   `add(1, `,
			line:      0,
			char:      7,
			wantFunc:  "add",
			wantParam: 1,
		},
		{
			name:      "qualified call",
			content:   `io.inspect(x`,
			line:      0,
			char:      12,
			wantFunc:  "io.inspect",
			wantParam: 0,
		},
		{
			name:      "nested call - inner",
			content:   `io.inspect(add(1, 2`,
			line:      0,
			char:      19,
			wantFunc:  "add",
			wantParam: 1,
		},
		{
			name:      "nested call - outer after close",
			content:   `io.inspect(add(1, 2), `,
			line:      0,
			char:      22,
			wantFunc:  "io.inspect",
			wantParam: 1,
		},
		{
			name:      "not in a call",
			content:   `x = 42`,
			line:      0,
			char:      6,
			wantFunc:  "",
			wantParam: 0,
		},
		{
			name:      "multiline",
			content:   "fn main() {\n    add(1,\n        ",
			line:      2,
			char:      8,
			wantFunc:  "add",
			wantParam: 1,
		},
		{
			name:      "predicate function name (trailing ?)",
			content:   `contains?(`,
			line:      0,
			char:      10,
			wantFunc:  "contains?",
			wantParam: 0,
		},
		{
			name:      "predicate function name with first arg",
			content:   `contains?(xs, `,
			line:      0,
			char:      14,
			wantFunc:  "contains?",
			wantParam: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFunc, gotParam := findCallContext(tt.content, tt.line, tt.char)
			if gotFunc != tt.wantFunc {
				t.Errorf("funcName = %q, want %q", gotFunc, tt.wantFunc)
			}
			if gotParam != tt.wantParam {
				t.Errorf("activeParam = %d, want %d", gotParam, tt.wantParam)
			}
		})
	}
}

// TestSignatureHelpCarriesAnInterfaceMethodDoc pins the doc channel of
// buildSignatureInfo's *ast.InterfaceMethod arm.
//
// That arm returned "" where its *ast.FuncDef and *ast.ExternFunc siblings
// return n.Doc, so signature help for `Struct.update(` showed the parameter
// list and no prose — while the same `///` text was sitting in the AST node
// the arm had in hand. The reference page and the editor hover dropped it in
// their own two places; this is the third.
//
// The subjects are DERIVED from std rather than listed, and the test fails on
// an empty population: "no documented interface method in std" is both what a
// regression looks like and what would make every assertion below vacuous.
func TestSignatureHelpCarriesAnInterfaceMethodDoc(t *testing.T) {
	lib := std.Load()
	checked := 0
	for module, scope := range lib.Modules {
		if scope == nil {
			continue
		}
		for _, iface := range scope.Symbols {
			if iface.Kind != analysis.SymbolInterface {
				continue
			}
			for name, method := range iface.Members {
				if method == nil || method.Kind != analysis.SymbolInterfaceMethod {
					continue
				}
				// The doc is read off the AST NODE, which is what
				// the arm under test reads, so this test does not
				// also depend on analysis copying Doc onto the
				// symbol (a separate contract with its own test).
				node, ok := method.Node.(*ast.InterfaceMethod)
				if !ok || strings.TrimSpace(node.Doc) == "" {
					continue
				}
				checked++
				sig, _, doc := buildSignatureInfo(method)
				if sig == "" {
					t.Errorf("std/%s: %s.%s produced no signature label", module, iface.Name, name)
				}
				want := strings.TrimSpace(strings.SplitN(strings.TrimSpace(node.Doc), "\n", 2)[0])
				if !strings.Contains(doc, want) {
					t.Errorf(`std/%s: signature help for %s.%s dropped its /// doc.

  want the doc to contain: %q
  got:                     %q

buildSignatureInfo's *ast.InterfaceMethod arm has the node; return n.Doc from
it the way the FuncDef and ExternFunc arms do.`, module, iface.Name, name, want, doc)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no documented interface method found in std, so this test asserted nothing — " +
			"either the /// comments were deleted or the AST stopped carrying InterfaceMethod.Doc")
	}
	t.Logf("%d documented interface methods, all carrying their doc through signature help", checked)
}
