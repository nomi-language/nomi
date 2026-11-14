package vm_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A crossing into a generated adapter whose binding answers a Maybe or a
// Result fuses as Map.get does when its value is only taken apart: the
// adapter's parts form (hostadapt.PartsFunc) hands over the payload, so a
// Some or an Ok allocates nothing. The count is per activation over a loop
// doing one crossing each, every answer a Some or an Ok.
func TestMaybeParts_AMatchedAdapterAnswerAllocatesNothing(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"Int.to_byte", `import std::io

fn down(n: Int) -> Int
    if n < 1 then
        0
    else
        x = case Int.to_byte(n %% 200)
            Some(b) -> Byte.to_int(b)
            None -> 0
        end
        down(n - 1) + x
    end
end

fn main()
    down(%d) |> io.print()
end
`},
		{"Int.to_byte, ?", `import std::io

fn sum(a: Int, b: Int) -> Maybe<Int>
    x = Int.to_byte(a)?
    y = Int.to_byte(b)?
    Some(Byte.to_int(x) + Byte.to_int(y))
end

fn down(n: Int) -> Int
    if n < 1 then
        0
    else
        case sum(n %% 100, n %% 7)
            Some(x) -> down(n - 1) + x
            None -> down(n - 1)
        end
    end
end

fn main()
    down(%d) |> io.print()
end
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// sum builds its own Some for the caller: one allocation.
			// The operands are Ints below 256, which box without
			// allocating; a String or Bytes operand allocates its box, as
			// it does for every crossing.
			want := 0.0
			if tc.name == "Int.to_byte, ?" {
				want = 1
			}
			if per := allocsPerActivation(t, tc.src); per > want+0.1 {
				t.Errorf("one matched crossing allocates %.2f times, want %.1f. The likely causes: "+
					"the binding has no parts form, or fuseMaybes does not fuse its crossing", per, want)
			}
		})
	}
}

// Fused adapter answers answer as their records did: a Some and a None, an
// Ok and an Err, `?` on both, the Err's payload read, and the fused value
// read boxed by an interpolation of the whole value elsewhere.
func TestMaybeParts_AFusedAdapterAnswersAsTheRecordDid(t *testing.T) {
	p, err := vmhost.LoadSource("main", partsProgram)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"number", "decoded", "byte_sum"} {
		listing, err := p.Disassemble(name)
		if err != nil {
			t.Fatal(err)
		}
		if ops := opcodesOf(listing); !ops["hostm"] || ops["matchv"] || ops["projp"] {
			t.Errorf("%s's crossing does not compile to the fused opcodes:\n%s", name, listing)
		}
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	want := `12 -1 9223372036854775807000 -1
3 error: invalid base64 at byte 0
Some(3) None
`
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

const partsProgram = `import
    std::base64
    std::io
end

fn number(s: String) -> Int
    case String.to_int(s)
        Some(n) -> n
        None -> -1
    end
end

fn decoded(s: String) -> String
    case base64.decode(s)
        Ok(b) -> "${Bytes.length(b)}"
        Err(e) -> "error: ${e}"
    end
end

fn byte_sum(data: Bytes, i: Int) -> Maybe<Int>
    a = Bytes.get(data, i)?
    b = Bytes.get(data, i + 1)?
    Some(Byte.to_int(a) - Byte.to_int(b))
end

fn main()
    io.print("${number("12")} ${number("x")} ${number("9223372036854775807000")} ${number("")}")
    io.print("${decoded("YWJj")} ${decoded("@@")}")
    data = String.to_bytes("da")
    io.print("${byte_sum(data, 0)} ${byte_sum(data, 1)}")
end
`
