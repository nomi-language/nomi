package vmhost

import (
	"context"
	"strings"
	"testing"
)

// A std function whose argument would otherwise trap takes a refinement, so a
// literal that breaks it is a compile error at the literal, naming the rule
// (spec §4 *Literals, `try_from` and `unchecked`): the named divisors, a bit
// count, the format functions' digit counts and fraction ranges, allocate's
// Ratios, Generator.float's FiniteRange, csv's Delimiter and Bits.field's
// FieldBits.
func TestStdRefinementArguments_ALiteralThatWouldTrapIsACompileError(t *testing.T) {
	nonNegative := "is not a NonNegativeInt: `fn n -> n >= 0` is false"
	nonEmpty := "breaks the rule of NonEmptyRange<NonNegativeInt>, a supertype of it: `fn r -> Range.contains(r, Range.start(r))` is false"
	ratios := "this Ratios literal breaks its rule: `fn ratios -> Iter.all_match(ratios, fn r -> r >= 0) and Iter.any_match(ratios, fn r -> r > 0)` is false"
	delimiter := " is not a Delimiter: `fn c -> c != '\"' and c != '\\r' and c != '\\n' and c != '\\u{0}' and Codepoint.to_int(c) != 0xFFFD` is false"
	for _, tc := range []struct{ name, src, want string }{
		{"Int.modulo", "fn main()\n    dbg Int.modulo(7, 0)\nend\n",
			"0 is not a NonZeroInt: `fn n -> n != 0` is false"},
		{"Int.floor_div", "fn main()\n    dbg Int.floor_div(7, 0)\nend\n",
			"0 is not a NonZeroInt: `fn n -> n != 0` is false"},
		{"Decimal.divide", "fn main()\n    dbg Decimal.divide(1d, 0d, 2, .HalfEven)\nend\n",
			"0d is not a NonZeroDecimal: `fn d -> d != 0d` is false"},
		{"Decimal.divide_exact", "fn main()\n    dbg Decimal.divide_exact(1d, 0.00d)\nend\n",
			"0.00d is not a NonZeroDecimal: `fn d -> d != 0d` is false"},
		{"Int.bit_count", "fn main()\n    dbg Int.bit_count(-1)\nend\n", "-1 " + nonNegative},
		{"Int.format", "fn main()\n    dbg Int.format(7, Locale\"en\", min_digits: -1)\nend\n", "-1 " + nonNegative},
		{"Float.format_percent", "fn main()\n    dbg Float.format_percent(0.5, Locale\"en\", -2)\nend\n", "-2 " + nonNegative},
		{"Float.format backwards", "fn main()\n    dbg Float.format(1.5, Locale\"en\", 3..=1)\nend\n",
			"this ClosedRange<NonNegativeInt> literal " + nonEmpty},
		{"Float.format exclusive", "fn main()\n    dbg Float.format(1.5, Locale\"en\", 0..2)\nend\n",
			"this ClosedRange<NonNegativeInt> literal breaks its rule: `fn r -> Range.is_inclusive(r) or not Range.is_bounded(r)` is false"},
		{"Decimal.format backwards", "fn main()\n    dbg Decimal.format(1.5d, Locale\"en\", fraction: 2..=1)\nend\n",
			"this ClosedRange<NonNegativeInt> literal " + nonEmpty},
		{"Decimal.format_percent backwards", "fn main()\n    dbg Decimal.format_percent(0.5d, Locale\"en\", fraction: 2..=1)\nend\n",
			"this ClosedRange<NonNegativeInt> literal " + nonEmpty},
		{"Decimal.allocate empty", "fn main()\n    dbg Decimal.allocate(1d, [], 2)\nend\n", ratios},
		{"Decimal.allocate negative", "fn main()\n    dbg Decimal.allocate(1d, [3, -1], 2)\nend\n", ratios},
		{"Decimal.allocate all zero", "fn main()\n    dbg Decimal.allocate(1d, [0, 0], 2)\nend\n", ratios},
		{"Money.allocate", "import std::money.Money\n\nfn main()\n    dbg Money.allocate(Money\"1 EUR\", [0])\nend\n", ratios},
		{"Generator.float", "import std::random.Generator\n\nfn main()\n    _ = Generator.float(2.5..1.5)\nend\n",
			"this FiniteRange literal breaks the rule of NonEmptyRange<Float>, a supertype of it: `fn r -> Range.contains(r, Range.start(r))` is false"},
		{"csv delimiter", "import std::csv\n\nfn main()\n    dbg csv.decode(\"a\", csv.Options{delimiter: '\"'})\nend\n", "'\"'" + delimiter},
		{"csv comment", "import std::csv\n\nfn main()\n    dbg csv.decode(\"a\", csv.Options{comment: Some('\\n')})\nend\n", "'\\n'" + delimiter},
		{"csv write delimiter", "import std::csv\n\nfn main()\n    dbg csv.encode([[\"a\"]], csv.WriteOptions{delimiter: '\\r'})\nend\n", "'\\r'" + delimiter},
		{"Bits.field", "import std::bits.Bits\n\nfn main()\n    dbg Bits.field(Bits\"10xx\", 8)\nend\n",
			"Bits\"10xx\" is not a FieldBits: `fn pattern -> pattern.field_mask != 0` is false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds := literalDiagnostics(t, tc.src)
			if len(ds) != 1 || ds[0].Message != tc.want || ds[0].Code != InvalidRefinementCode {
				t.Errorf("diagnostics = %v, want one reading %q\n%s", ds, tc.want, tc.src)
			}
		})
	}
}

// The same functions take a value computed when the program runs through
// the refinement's try_from, which answers None for a value that would have
// trapped, and Some for one the function then takes.
func TestStdRefinementArguments_AComputedValueGoesThroughTryFrom(t *testing.T) {
	p, err := LoadSource("main", `import
    std::bits.{Bits, FieldBits}
    std::csv.{self, Delimiter}
    std::decimal.Ratios
    std::io
    std::json.{Json, JsonObject, ToJson}
    std::random.{FiniteRange, Generator, Seed}
end

fn main()
    zero = 0
    io.inspect((NonZeroInt.try_from(zero), Int.modulo(-1, NonZeroInt.try_from(zero + 1440)!)))
    io.inspect(NonZeroDecimal.try_from(Int.to_decimal(zero)))
    io.inspect(Int.bit_count(NonNegativeInt.try_from(zero + 13)!))
    io.inspect((Ratios.try_from([zero, zero]), Decimal.allocate(1d, Ratios.try_from([zero + 1, 3])!, 2)))
    io.inspect(FiniteRange.try_from(Float.negative_infinity()..1.0))
    (x, _) = Generator.step(Generator.float(FiniteRange.try_from(0.5..=0.5)!), Seed.from_int(1))
    io.inspect(x)
    io.inspect((Delimiter.try_from('"'), csv.decode("a|b", csv.Options{delimiter: Delimiter.try_from('|')!})))
    io.inspect((FieldBits.try_from(Bits"10xx"), Bits.field(FieldBits.try_from(Bits.parse("1a")!)!, 3)))
    io.inspect((JsonObject.try_from(Json.Int(1)), JsonObject.try_from(ToJson.to_json({id: 7})) |> Option.map(JsonObject.fields)))
    digits: Range<NonNegativeInt> = 1..=Int.abs(zero - 1)
    io.inspect(Float.format(1.25, Locale"en", ClosedRange.try_from(digits)!))
end
`)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	const want = `(None, 1439)
None
3
(None, [0.25d, 0.75d])
None
0.5
(None, Ok([["a", "b"]]))
(None, Some(1))
(None, Some({"id" => 7}))
"1.2"
`
	if got := out.String(); got != want {
		t.Errorf("output:\n%s\nwant:\n%s", got, want)
	}
}
