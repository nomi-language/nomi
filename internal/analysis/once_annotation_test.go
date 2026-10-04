package analysis

import "testing"

func TestOnceAnnotationIsAvailableBeforeBodyChecking(t *testing.T) {
	fa, errs := buildTypesFromSource(`pub once label: String = initialize()
fn initialize(): String { "service" }`)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	sym := fa.ModuleScope.Lookup("label")
	if sym == nil || sym.Type != TypeString {
		t.Fatalf("once declaration type before checking: %v", sym)
	}
}

func TestOnceAnnotationResolvesDeclaredAliases(t *testing.T) {
	fa, errs := buildTypesFromSource(`typealias Label String
pub once label: Label = "service"`)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	sym := fa.ModuleScope.Lookup("label")
	if sym == nil || sym.Type != TypeString {
		t.Fatalf("aliased once declaration type: %v", sym)
	}
}

// A once's annotation is the expected type of its value, as a local
// binding's is: `.Variant` shorthand resolves against the declared enum.
// The checker used to check the value with no expected type, so
// `once directions: List<Direction> = [.North, .South]` failed with
// ".North requires a determinable enum type at this position; annotate the
// binding or qualify the variant" on a binding that was annotated.
func TestOnceAnnotationIsTheValuesExpectedType(t *testing.T) {
	_, errs := checkSourceSynth(`pub enum Direction {
    North
    South
}

pub once directions: List<Direction> = [.North, .South]
pub once first: Direction = .North`)
	if len(errs) != 0 {
		t.Fatalf("annotated once with variant shorthand: %v", errs)
	}
}

// A value of another type than the annotation is a type error at the once.
// The checker used to accept `once n: Int = "x"`, and the program then
// stopped at the IR builder with "no decline reason recorded".
func TestOnceValueMustMatchItsAnnotation(t *testing.T) {
	_, errs := checkSourceSynth(`pub once n: Int = "x"`)
	if len(errs) != 1 || errs[0].Message != "type mismatch: expected Int, got String" || errs[0].Line != 1 {
		t.Fatalf("errors = %v, want one line-1 \"type mismatch: expected Int, got String\"", errs)
	}
}
