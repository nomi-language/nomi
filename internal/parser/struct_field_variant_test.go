package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// A struct pattern's field may hold a variant named through the file that
// declares its type: `#{err: sql.Error.Closed}`. The identifier before the
// `.` is the file, not a name the field binds.
func TestStructPatternField_ModuleQualifiedVariant(t *testing.T) {
	for _, tc := range []struct {
		arm, module, variant string
		payload              bool
	}{
		{"#{err: sql.Error.Closed}", "sql.Error", "Closed", false},
		{"#Slot{shape: shapes.Shape.Square(n), bag}", "shapes.Shape", "Square", true},
		{"#{c: json.Json.Case.Snake}", "json.Json.Case", "Snake", false},
	} {
		sp, ok := caseArmPattern(t, tc.arm).(*ast.StructPattern)
		if !ok || len(sp.Fields) == 0 {
			t.Fatalf("%q: want a struct pattern", tc.arm)
		}
		f := sp.Fields[0]
		if f.Binding != "" {
			t.Fatalf("%q: the field binds %q; want a nested pattern", tc.arm, f.Binding)
		}
		ep, ok := f.Pattern.(*ast.EnumPattern)
		if !ok {
			t.Fatalf("%q: field pattern is %T, want an enum pattern", tc.arm, f.Pattern)
		}
		qt, ok := ep.Variant.(*ast.QualifiedType)
		if !ok {
			t.Fatalf("%q: variant is %T, want a qualified type", tc.arm, ep.Variant)
		}
		member, _ := qt.Member.(*ast.SimpleType)
		if qt.Module != tc.module || member == nil || member.Name != tc.variant {
			t.Fatalf("%q: got owner %q and variant %#v", tc.arm, qt.Module, qt.Member)
		}
		if got := ep.Payload != nil || ep.Binding != ""; got != tc.payload {
			t.Fatalf("%q: payload present = %v, want %v", tc.arm, got, tc.payload)
		}
	}

	// A bare name still renames the field, and a name before `or` still
	// starts the alternatives.
	sp := caseArmPattern(t, "#{x: y}").(*ast.StructPattern)
	if sp.Fields[0].Binding != "y" || sp.Fields[0].Pattern != nil {
		t.Fatalf("`#{x: y}`: got %#v", sp.Fields[0])
	}
	sp = caseArmPattern(t, "#{x: a or b}").(*ast.StructPattern)
	orPatternOf(t, sp.Fields[0].Pattern, 2)
}

// A field value that is not a pattern is still an error: a file name with
// a dangling `.`, or with something other than a name after its `.`.
func TestStructPatternField_MalformedValueIsRejected(t *testing.T) {
	for _, tc := range []struct{ arm, want string }{
		{"#{err: sql.}", "expected field name in struct pattern"},
		{"#{err: sql.(x)}", "expected field name in struct pattern"},
	} {
		err := parseError(t, "fn main() {\n    x = case v {\n        "+tc.arm+" -> 0\n    }\n}\n")
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%q: got %q, want it to name %q", tc.arm, err, tc.want)
		}
	}
}
