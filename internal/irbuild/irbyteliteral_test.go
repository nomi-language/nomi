package irbuild

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// An Int literal the checker typed as a Byte runs as a Byte in every
// position: a binding, a list, a field, a result, an argument, an operand,
// and a literal pattern at the top of a case, in a payload and in a tuple.
func TestIRBytes_Literals(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
struct Header {
 tag: Byte
 length: Int
}
fn marker() -> Byte { 0xFF }
fn name(b: Byte) -> String {
 case b {
  0x00 -> "nul"
  0x7F -> "del"
  _ -> "other"
 }
}
fn main() {
 b: Byte = 0x81
 bytes: List<Byte> = [0x81, 0x00, 10]
 header = #Header{tag: 7, length: 300}
 io.inspect(b)
 io.inspect(bytes)
 io.inspect(header)
 io.inspect(marker())
 io.inspect(name(0x7F))
 io.inspect(name(b))
 io.inspect(b == 0x81)
 io.inspect(0x80 == b)
 io.inspect(b &&& 0x7F)
 io.inspect(0x0F ||| b)
 io.inspect(bytes == [0x81, 0x00, 0x0A])
 io.inspect(Bytes.from_list([104, 105]) |> Bytes.to_string())
 io.inspect(case Some(b) {
  Some(0x81) -> "some"
  _ -> "none"
 })
 io.inspect(case #(b, 1) {
  #(0x81, 1) -> "pair"
  _ -> "other"
 })
 io.inspect(bytes |> Iter.map(fn x -> x &&& 0x80) |> Iter.to_list())
}
`, "129\n[129, 0, 10]\n#Header{tag: 7, length: 300}\n255\n\"del\"\n\"other\"\nTrue\nFalse\n1\n143\nTrue\nOk(\"hi\")\n\"some\"\n\"pair\"\n[128, 0, 0]\n")
}

// The literal is a Byte constant in the IR, not an Int the VM converts.
func TestIRBytes_LiteralIsAByteConstant(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.nomi")
	src := "import std/io\nfn main() {\n b: Byte = 0x81\n io.inspect(b)\n}\n"
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range res.IR {
		for _, f := range m.Funcs() {
			if f.Name() != "main" {
				continue
			}
			for i := 1; i < f.NumTemps(); i++ {
				c, ok := f.Def(ir.Temp(i)).(*ir.Const)
				if !ok {
					continue
				}
				if c.Kind() == ir.ConstInt && c.Int() == 0x81 {
					t.Errorf("main holds the literal as an Int: %s", c)
				}
				if c.Kind() == ir.ConstByte && c.Int() == 0x81 {
					found = true
					if ty := f.TempType(c.Dst()); ty != ir.ByteType {
						t.Errorf("the Byte constant's temporary is %v, want Byte", ty)
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("main holds no Byte constant 0x81")
	}
}
