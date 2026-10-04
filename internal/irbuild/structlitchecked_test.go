package irbuild

import (
	"strings"
	"testing"
)

// The struct-literal field rules belong to the CHECKER. `checkStructLit`
// validates a literal's field NAMES, its field VALUES and its OMISSIONS, so the
// builder has no `struct literal missing field` or `unknown struct field`
// refusal to make: neither key has a front-end-valid witness.
//
// The analysis-side rules have their own tests and this file does not duplicate
// them: `analysis.TestCheck_StructLit_ValidatesFields`,
// `TestCheck_StructVariantLit_RequiresNonDefaultedFields` and
// `TestCheck_StructLit_StdlibTypedFieldsStayLenient`. What is asserted here is
// the consequence for THIS package: that the builder is never asked.

// TestStructLitRules_AreTheCheckersNow walks every spelling that could reach
// either key and asserts the FRONT END refuses it BY MESSAGE. A check that
// asserts only "something rejected this" cannot tell the checker's rule from a
// parse error.
//
// The pattern, field-access and variant rows are here because the two keys
// would be reported from those arms too, so "the key is unreachable" is a
// claim about all of them and not only about the literal.
func TestStructLitRules_AreTheCheckersNow(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"a literal omitting a required field",
			"struct P {\n  n: Int\n}\n\nfn make(): P {\n  P{}\n}\n",
			"missing field 'n' of P"},
		{"a literal naming a field the struct does not declare",
			"struct P {\n  n: Int\n}\n\nfn make(): P {\n  P{n: 1, m: 2}\n}\n",
			"P has no field 'm'"},
		{"a literal whose field value has the wrong type",
			"struct P {\n  n: Int\n}\n\nfn make(): P {\n  P{n: \"s\"}\n}\n",
			"field 'n' of P: expected Int, got String"},
		{"the record form omitting a required field",
			"struct P {\n  n: Int\n  m: Int\n}\n\nfn f(): P {\n  P({n: 1})\n}\n",
			"missing field 'm' of P"},
		{"the record form naming an undeclared field",
			"struct P {\n  n: Int\n}\n\nfn f(): P {\n  P({n: 1, z: 2})\n}\n",
			"P has no field 'z'"},
		{"the empty record form",
			"struct P {\n  n: Int\n}\n\nfn f(): P {\n  P({})\n}\n",
			"missing field 'n' of P"},
		{"a struct-shaped variant omitting a required field",
			"enum E {\n  V { n: Int }\n}\n\nfn make(): E {\n  E.V{}\n}\n",
			"missing field 'n' of E.V"},
		{"a struct-shaped variant naming an undeclared field",
			"enum E {\n  V { n: Int }\n}\n\nfn make(): E {\n  E.V{n: 1, m: 2}\n}\n",
			"no field 'm' on variant V of enum E"},
		{"a struct pattern binding a field that does not exist",
			"struct P {\n  n: Int\n}\n\nfn get(p: P): Int {\n  case p {\n    P{m} -> m\n  }\n}\n",
			"field m not found in type P"},
		{"a variant pattern binding a field that does not exist",
			"enum E {\n  V { n: Int }\n}\n\nfn get(e: E): Int {\n  case e {\n    E.V{m} -> m\n  }\n}\n",
			"field m not found in type V"},
		{"an anonymous-struct pattern binding a field that does not exist",
			"fn get(): Int {\n  r = {a: 1}\n  {b} = r\n  b\n}\n",
			"field b not found in type {a: Int}"},
		{"field access on a struct that has no such field",
			"struct P {\n  n: Int\n}\n\nfn get(p: P): Int {\n  p.m\n}\n",
			"struct 'P' has no field 'm'"},
		{"field access on an anonymous struct that has no such field",
			"fn get(): Int {\n  r = {a: 1}\n  r.b\n}\n",
			"{a: Int} has no field 'b'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := AnalyzeSource("main", tc.src)
			if err == nil {
				t.Fatalf("this spelling ANALYSES, so %q is a builder question "+
					"and TestUnsupported_NamedTypes should carry a row for it", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refused for some other reason than the rule this row names.\nwant substring: %s\ngot: %v",
					tc.want, err)
			}
		})
	}
}
