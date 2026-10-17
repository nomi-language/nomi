package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// variantsDeriveErrors lowers and checks src with the stdlib loaded and
// returns every message: the lowering's, where derive shape errors live,
// and the checker's.
func variantsDeriveErrors(t *testing.T, src string) []string {
	t.Helper()
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	nodes, lowerErrs := analysis.LowerDerives(nodes)
	lib := loadStdlibOnce()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	fa.TypeErrors = append(fa.TypeErrors, analysis.CheckTypes(fa, nodes)...)
	var out []string
	for _, e := range lowerErrs {
		out = append(out, e.Message)
	}
	for _, e := range fa.TypeErrors {
		out = append(out, e.Message)
	}
	return out
}

func TestDeriveVariants_AcceptsAnEnumWhoseVariantsCarryNoData(t *testing.T) {
	src := `enum Coin {
    Penny
    Nickel
}

derive Variants for Coin

fn all() -> List<Coin> {
    Coin.variants()
}
`
	if errs := variantsDeriveErrors(t, src); len(errs) != 0 {
		t.Fatalf("the front end rejects `derive Variants` on a data-free enum:\n  %s", strings.Join(errs, "\n  "))
	}
}

func TestDeriveVariants_RejectsAVariantThatCarriesData(t *testing.T) {
	src := `enum Shape {
    Dot
    Circle Float
}

derive Variants for Shape
`
	want := "`derive Variants for Shape`: variant `Shape.Circle` carries data, and `Variants` lists only variants that carry none; remove its data, or write `impl Variants for Shape { ... }` by hand"
	errs := variantsDeriveErrors(t, src)
	if len(errs) == 0 {
		t.Fatal("the front end admits `derive Variants` on an enum with a variant that carries data")
	}
	if !variantsHasMessage(errs, want) {
		t.Fatalf("errors = %q, want %q", errs, want)
	}
}

func TestDeriveVariants_RejectsAType(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			"a struct",
			"struct Point {\n    x: Int\n}\n\nderive Variants for Point\n",
			"`derive Variants for Point`: `Variants` lists an enum's variants, and `Point` is a struct, not an enum; write `impl Variants for Point { ... }` to list its values by hand",
		},
		{
			"a distinct type",
			"type Meters Int\n\nderive Variants for Meters\n",
			"`derive Variants for Meters`: `Variants` lists an enum's variants, and `Meters` is a distinct type, not an enum; write `impl Variants for Meters { ... }` to list its values by hand",
		},
		{
			"a generic enum",
			"enum Tag<T> {\n    A\n}\n\nderive Variants for Tag<T>\n",
			"`derive Variants for Tag`: `Tag` has type parameters, and `Variants` lists the variants of an enum without them",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := variantsDeriveErrors(t, tt.src)
			if len(errs) == 0 {
				t.Fatalf("the front end admits `derive Variants` on %s", tt.name)
			}
			if !variantsHasMessage(errs, tt.want) {
				t.Fatalf("errors = %q, want %q", errs, tt.want)
			}
		})
	}
}

func variantsHasMessage(msgs []string, want string) bool {
	for _, m := range msgs {
		if m == want {
			return true
		}
	}
	return false
}
