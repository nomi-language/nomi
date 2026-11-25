package vmhost

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// std::compress's encode computes from its operands alone, at a cost that
// grows with their size, as a hash does: a subtype rule may call it, and
// the program's literals are checked as it compiles. Its decode's cost
// grows with the limit it is given, a value, so a rule that calls it is
// refused as unbounded, as one calling hash.pbkdf2_sha256 is, and not as
// having effects.
func TestCompressRules_EncodeIsCheckedAsAHashIsAndDecodeIsUnbounded(t *testing.T) {
	const pure = `import
    std::compress.{self, Format}
    std::hash
end

subtype Shrinks String when fn s ->
    packed = compress.encode(Format.Gzip, String.to_bytes(s))
    Bytes.length(packed) < Bytes.length(String.to_bytes(s)) and Bytes.length(hash.sha256(packed)) == 32
end

fn main()
    a: Shrinks = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
    dbg String.length(a)
end
`
	p, err := LoadSource("main", pure)
	if err != nil {
		t.Fatalf("a rule that compresses and hashes is refused: %v", err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if want := "dbg line 13: String.length(a) = 100\n"; out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}

	const decodes = `import std::compress.{self, Format}

subtype Packed String when fn s -> compress.decode(Format.Gzip, String.to_bytes(s), max_length: 100) is Ok(_)

fn main()
    p: Packed = "x"
    dbg p
end
`
	ds := literalDiagnostics(t, decodes)
	const want = "the rule of Packed calls a function whose cost grows with its argument ("
	for _, d := range ds {
		if strings.HasPrefix(d.Message, want) && strings.Contains(d.Message, "compress/host.decode") &&
			d.Code == InvalidSubtypeCode {
			return
		}
	}
	t.Errorf("diagnostics = %v, want one reading %q... naming compress/host.decode", ds, want)
}
