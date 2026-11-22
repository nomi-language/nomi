package vmhost

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// A channel's element may nest declarations as deep as the program writes
// them. The check that a handle's element ends (irHandleElemEnds) walks the
// declarations the element reaches and stops at one already on its path;
// a depth past which it answered no put such a struct outside the value
// domain, so a `once` of it was an internal error and a local binding of it
// a body that was not supported. Here the element is a list of tuples of a
// struct whose field is a struct holding a channel of a struct four
// declarations deep: a pool of connections, each with its state in a cell.
func TestHandleElement_DeeplyNestedLowersAndRuns(t *testing.T) {
	const decls = `import
    std::channels.{Channel, Receiver, Sender}
    std::io
end

struct Box<S>
    slot: Channel<S>
end

fn boxed<S>(value: S) -> Box<S>
    slot = Channel.buffered<S>(1)
    _ = Sender.send(slot.sender, value)
    Box{slot:}
end

fn size(box: Box<List<(Link, Int)>>) -> Int
    case Receiver.receive(box.slot.receiver)
        Some(xs) -> List.length(xs)
        None     -> -1
    end
end

struct S0
    a: Int
end

struct S1
    a: S0
end

struct S2
    a: S1
end

struct S3
    a: S2
end

struct Link
    io: Box<S3>
end
`
	for _, c := range []struct{ name, src string }{
		{"a once", decls + `
once pairs: Box<List<(Link, Int)>> = boxed([(Link{io: boxed(S3{a: S2{a: S1{a: S0{a: 1}}}})}, 2)])

fn main()
    io.print(size(pairs))
end
`},
		{"a local binding", decls + `
fn main()
    pairs: Box<List<(Link, Int)>> = boxed([])
    io.print(size(pairs))
end
`},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, err := LoadSource("main.nomi", c.src)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if u := p.Unsupported(); u != nil {
				t.Fatalf("the program does not lower: %v", u)
			}
			var out bytes.Buffer
			if err := p.Run(context.Background(), &out, nil, false); err != nil {
				t.Fatalf("run: %v\n%s", err, out.String())
			}
			want := "1\n"
			if strings.Contains(c.name, "local") {
				want = "0\n"
			}
			if out.String() != want {
				t.Fatalf("output %q, want %q", out.String(), want)
			}
		})
	}
}
