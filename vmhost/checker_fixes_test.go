package vmhost

import (
	"bytes"
	"context"
	"testing"
)

// Programs that a checker gap once left partly untyped or wrongly
// rejected: each is accepted, every expression has a resolved type
// (analysis.UnresolvedExprs), and it runs with the output given.
var checkerFixPrograms = []struct {
	name, src, want string
}{
	{
		// A generic opaque type's parameter (Vector<T>) is one of the
		// function's type parameters: `|v: T|` names the T of the signature,
		// and the elements next_item yields are that T.
		name: "type parameter only inside an opaque type",
		src: `import std/io

fn heads<T>(a: Vector<T>, b: Vector<T>): Int {
    case (Vector.next_item(a), Vector.next_item(b)) {
        (Some((x, _)), Some((y, _))) -> {
            keep = |v: T| v
            _ = keep(x)
            _ = keep(y)
            2
        }
        _ -> 0
    }
}

fn main() {
    io.inspect(heads(#[1], #[2]))
    io.inspect(Comparable.compare(#[1, 2], #[1, 3]))
}
`,
		want: "2\nLess\n",
	},
	{
		// The annotation's type arguments reach a lambda in a generic
		// struct's field, in the literal and the call form, as they reach
		// one in a generic enum variant's payload.
		name: "annotation into a generic struct literal's field lambda",
		src: `import std/io

struct Box<T> {
    v: T
}

struct Pair<A, B> {
    a: A
    b: B
}

enum Slot<T> {
    Full(T)
    Empty
}

fn main() {
    f: Box<(Int) -> Int> = Box{v: |x| x + 1}
    io.inspect(f.v(2))
    c: Box<(Int) -> Int> = Box({v: |x| x * 10})
    io.inspect(c.v(2))
    h: Pair<Int, (Int) -> String> = Pair{a: 1, b: |n| "n=${n}"}
    io.print(h.b(h.a))
    g: Maybe<(Int) -> Int> = Some(|x| x * 3)
    case g {
        Some(k) -> io.inspect(k(2))
        None -> io.print("none")
    }
    s: Slot<(Int) -> Int> = Slot.Full(|x| x - 1)
    case s {
        Slot.Full(k) -> io.inspect(k(2))
        Slot.Empty -> io.print("empty")
    }
}
`,
		want: "3\n20\nn=1\n6\n1\n",
	},
}

func TestCheckerFixes_TypedAndRun(t *testing.T) {
	for _, tc := range checkerFixPrograms {
		t.Run(tc.name, func(t *testing.T) {
			o := checkLowers(t.TempDir(), tc.src)
			if !o.accepted {
				t.Fatalf("the front end rejects this:\n%s", o.rejection)
			}
			for _, r := range o.unresolved {
				t.Errorf("untyped: %s", r)
			}
			p, err := LoadSource("main.nomi", tc.src)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			var out bytes.Buffer
			if err := p.Run(context.Background(), &out, nil, false); err != nil {
				t.Fatalf("run: %v\noutput so far:\n%s", err, out.String())
			}
			if got := out.String(); got != tc.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}
