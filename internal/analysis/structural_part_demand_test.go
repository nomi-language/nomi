package analysis_test

import "testing"

// The declarations the part-demand tests share: a type with no impl of
// ToJson, and functions whose record fields are a type parameter's values.
const partDemandDecls = `import
  std::json.{Json, ToJson}
  std::parse.{self, Parse}
end

struct X
  n: Int
end

fn texts<T>(x: T) -> List<(String, String)> where T: Display
  parse.write_fields({v: x})
end

fn bounded<T>(x: T) -> List<(String, String)> where T: Parse<String> and Display
  parse.write_fields({v: x})
end

fn encoded<T>(x: T) -> Json where T: Display
  ToJson.to_json({v: x})
end
`

// A record read from texts, or written as JSON, asks each field for what
// the record needs, and a field of an interface value, an Iter or a type
// parameter has only what its interface or bounds guarantee (spec §12
// *Tuples, anonymous structs and functions*). Each is refused at the
// record, naming the interface the field's type lacks.
func TestStructuralParts_AFieldHasOnlyWhatItsTypeGuarantees(t *testing.T) {
	cases := []struct {
		name, stmt, want string
	}{
		{"an Iter read from texts", `_ = parse.write_fields({v: [1] |> Iter.map(fn n -> n)})`,
			"this value holds values of `Iter<Int>`, and Iter<Int> does not require Parse of its implementors"},
		{"an interface value read from texts", `_ = parse.write_fields({v: (1 as Display)})`,
			"this value holds values of `Display`, and Display does not require Parse of its implementors"},
		{"an interface value parsed into", `_ = parse.read_fields<{v: Display}>({"v" => "1"})`,
			"Display does not require Parse of its implementors"},
		{"an Iter as JSON", `_ = ToJson.to_json({v: [1] |> Iter.map(fn n -> n)})`,
			"this value holds values of `Iter<Int>`, and Iter<Int> does not require ToJson of its implementors"},
		{"an interface value as JSON", `_ = ToJson.to_json({v: (1 as Display)})`,
			"this value holds values of `Display`, and Display does not require ToJson of its implementors"},
		{"a type with no impl as JSON", `_ = ToJson.to_json({v: X{n: 1}})`,
			"no impl of `ToJson` for `X`"},
		{"an Iter read from JSON", `_ = Json.decode_as<{v: Iter<Int>}>("{}")`,
			"Iter<Int> does not require FromJson of its implementors"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := partDemandDecls + "\nfn run() -> Unit\n  " + tc.stmt + "\nend\n"
			fa := buildWithStdlibForDerive(src)
			if !hasErrSubstring(fa.TypeErrors, tc.want) {
				t.Fatalf("the checker admits %s, or rejects it with another message; want %q, got:\n%s",
					tc.stmt, tc.want, errorsText(fa.TypeErrors))
			}
		})
	}
}

// A field of a type parameter asks the parameter's bounds: one bounded by
// Display alone is no ParseFields field and no ToJson field, and one
// bounded by Parse and Display is a ParseFields field.
func TestStructuralParts_ATypeParameterFieldAsksItsBounds(t *testing.T) {
	fa := buildWithStdlibForDerive(partDemandDecls + "\nfn run() -> Unit\n  _ = texts(1)\n  _ = bounded(1)\n  _ = encoded(1)\nend\n")
	for _, want := range []string{
		"this value holds values of `T`, and `texts` does not require Parse of `T`",
		"this value holds values of `T`, and `encoded` does not require ToJson of `T`",
	} {
		if !hasErrSubstring(fa.TypeErrors, want) {
			t.Errorf("the checker admits a field of a type parameter its bounds do not cover; want %q, got:\n%s", want, errorsText(fa.TypeErrors))
		}
	}
	if hasErrSubstring(fa.TypeErrors, "`bounded`") {
		t.Errorf("the checker rejects a field of a type parameter bounded by Parse and Display:\n%s", errorsText(fa.TypeErrors))
	}
}
