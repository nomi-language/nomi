package vmhost_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A type-prefixed list literal builds the enum variant or list-distinct
// type it names, in every position: with no expected type, against the
// enum, against a list of the enum, and as a function's result.
func TestRun_TypePrefixedListLiterals(t *testing.T) {
	out := runOutput(t, `import std/io

enum JV {
  Num Int
  Arr List<JV>
}

type Ids List<Int>

fn total(ids: Ids): Int {
  case ids {
    Ids[a, b] -> a + b
    _ -> 0
  }
}

fn arr(): JV {
  .Arr[.Num(1), .Arr[.Num(2)]]
}

fn main() {
  a: JV = JV.Arr[.Num(1), .Num(2)]
  b: JV = .Arr[.Num(3)]
  ids: Ids = Ids[4, 5]
  vs: List<JV> = [.Arr[.Num(6)], JV.Arr[]]
  io.print(Debug.inspect(a))
  io.print(Debug.inspect(b))
  io.print(Debug.inspect(arr()))
  io.print(total(ids))
  io.print(total(Ids[7, 8]))
  io.print(total(Ids([9, 10])))
  io.print(Debug.inspect(vs))
}
`)
	want := `Arr([Num(1), Num(2)])
Arr([Num(3)])
Arr([Num(1), Arr([Num(2)])])
9
15
19
[Arr([Num(6)]), Arr([])]
`
	if out != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
}

// FuzzFrontEndAcceptsSoItLowers found `Holder{items: X[1, 2]}` with nothing
// named X: the checker typed the literal from the field's List<Int> and the
// IR builder declined it. The front end rejects it now.
func TestLoad_UnknownListPrefixIsRejected(t *testing.T) {
	_, err := vmhost.Load(writeProgram(t, `struct Holder {
  items: List<Int>
}

fn main() {
  _c = Holder{items: X[1, 2]}
}
`))
	if err == nil {
		t.Fatal("the front end accepts `X[1, 2]` with nothing named X")
	}
	if !strings.Contains(err.Error(), "undefined type X") {
		t.Fatalf("error does not name the unknown type:\n%v", err)
	}
}
