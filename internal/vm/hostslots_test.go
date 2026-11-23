package vm_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A crossing into an adapter with a register form reads its operands from
// their registers and writes its answer into its destination's: an Int past
// 255, a Float, a String and a Bytes cross without a box, a fused Option's
// payload lands in a register of its own bank, and a tuple lands in a split
// destination's registers. The count is per activation over a loop doing
// one crossing each (allocsPerActivation).
func TestHostSlots_ACrossingAllocatesNothing(t *testing.T) {
	for _, tc := range []struct {
		name, step string
		want       float64
		why        string
	}{
		{"Int to Int", "Int.swap_bytes_32(n + 1000)", 0, ""},
		{"Int to Float", "Float.to_int(Float.from_bits(4607182418800017408 + n))!", 0, ""},
		{"Float to Int", "Float.to_bits(1.5 + Int.to_float(n)) %% 1000", 0, ""},
		{"Bytes to Int", "Bytes.length(data) + n", 0, ""},
		{"String to Int", "String.length(text) + n", 0, ""},
		{"Bytes and Int to a fused Option<Int>, !", "Bytes.uint32_be_at(data, 1000 + n %% 4)!", 0, ""},
		{"Bytes and Int to a fused Option<Int>, case", "case Bytes.uint64_le_at(data, 990 + n %% 8)\n            Some(x) -> x %% 7\n            None -> 0\n        end", 0, ""},
		{"Bytes and Int to a fused Option<Byte>", "Byte.to_int(Bytes.get(data, 1000 + n %% 4)!)", 0, ""},
		{"a search to a fused Option<Int>", "Bytes.index_of(data, needle, n %% 900)!", 0, ""},
		{"a fused Option of a tuple, its parts into a split destination", "Bytes.uvarint_at(data, 1000 + n %% 4)!.first", 0, ""},
		{"a fused Option of a tuple, case", "case Bytes.varint_at(data, 990 + n %% 8)\n            Some((v, next)) -> v + next\n            None -> 0\n        end", 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `import std::io

fn down(n: Int, data: Bytes, text: String, needle: Bytes) -> Int
    if n < 1 then
        0
    else
        x = ` + tc.step + `
        down(n - 1, data, text, needle) + x
    end
end

fn main()
    data = String.to_bytes(String.repeat("ab", 600))
    down(%d, data, String.repeat("xy", 300), String.to_bytes("ba")) |> io.print()
end
`
			if per := allocsPerActivation(t, src); per > tc.want+0.1 {
				t.Errorf("one crossing allocates %.2f times, want %.0f%s. The likely causes: the "+
					"crossing running its boxed adapter rather than its register form (regsAt "+
					"refusing it), an rt function boxing its rt.Int, or a fused payload kept in "+
					"its reference register", per, tc.want, map[bool]string{true: " (" + tc.why + ")", false: ""}[tc.why != ""])
			}
		})
	}
}

// A Builder push reads its Int from its register and boxes only the
// Builder it answers.
func TestHostSlots_ABuilderPushBoxesItsBuilderOnly(t *testing.T) {
	per := allocsPerActivation(t, `import
    std::bytes.Builder
    std::io
end

fn down(n: Int, b: Builder) -> Int
    if n < 1 then Builder.length(b) else down(n - 1, Builder.push_int32_be(b, n + 100000))
end

fn main()
    down(%d, Builder.new(4096)) |> io.print()
end
`)
	if per > 1.1 {
		t.Errorf("one push allocates %.2f times, want 1 (the Builder's box)", per)
	}
}

// Register forms answer what the boxed adapters did, at the edges a word
// does not hold: an Int past int64 in and out, MinInt64, a None and an Err,
// a tuple's components, a payload read back boxed by an interpolation.
func TestHostSlots_ACrossingAnswersAsTheAdapterDid(t *testing.T) {
	const src = `import
    std::bytes.Builder
    std::io
end

fn word(x: Option<Int>) -> String
    case x
        Some(n) -> "${n}"
        None -> "none"
    end
end

fn main()
    big = 1180591620717411303424
    ones = Bytes.from_list([0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF])
    io.print("${Int.wrapping_add(big, 1)} ${Int.wrapping_add(-9223372036854775808, -1)} ${Int.rotate_left_32(big + 5, 1)}")
    io.print("${word(Bytes.uint64_be_at(ones, 0))} ${word(Bytes.int64_be_at(ones, 0))} ${word(Bytes.uint64_be_at(ones, 1))} ${word(Bytes.uint8_at(ones, big))}")
    (v, next) = Bytes.uvarint_at(Bytes.from_list([0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x7F]), 0)!
    (s, after) = Bytes.varint_at(Bytes.from_list([0x03]), 0)!
    io.print("${v} ${next} ${s} ${after}")
    io.print("${Float.from_bits(Float.to_bits(-0.5))} ${Float.to_bits(1.0)} ${String.length("héllo")} ${Bytes.length(Bytes.from_list([1, 2]))}")
    bad = Bytes.from_list([0xFF])
    text = case Bytes.to_string(bad)
        Ok(t) -> t
        Err(e) -> "err ${e}"
    end
    io.print(text)
    got = Bytes.uint16_be_at(ones, 6)
    io.print("${got} ${Bytes.uint16_be_at(ones, 7)}")
    b = Builder.new() |> Builder.push_uvarint(big) |> Builder.push_varint(-9223372036854775808)
    io.print("${Builder.length(b)} ${Bytes.to_list(Builder.to_bytes(b))}")
end
`
	p, err := vmhost.LoadSource("main", src)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	// The boxed adapters' answers, recorded before the register forms.
	want := `1 9223372036854775807 10
18446744073709551615 -1 none none
1180591620717411303423 10 -2 1
-0.5 4607182418800017408 5 2
err invalid UTF-8
Some(65535) None
21 [128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 1, 255, 255, 255, 255, 255, 255, 255, 255, 255, 1]
`
	if got := out.String(); got != want {
		t.Errorf("output:\n%s\nwant:\n%s", got, want)
	}
	listing, err := p.Disassemble("main")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listing, "hostm") {
		t.Errorf("main fuses none of its crossings, so this test shows nothing:\n%s", listing)
	}
}
