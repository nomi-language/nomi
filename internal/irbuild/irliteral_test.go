package irbuild

import "testing"

func TestIRTypedLiteral_TourStructHandler(t *testing.T) {
	verifyLambdaProgram(t, `import {
  std/literals.{Fragment, Literal}
}

struct Box {
  contents: String
}

impl Literal for Box {
  fn from_fragments(fragments: List<Fragment<String>>): Box {
    body = Iter.reduce(fragments, |acc = "", frag|
      case frag {
        .Static(s) -> acc + s
        .Dynamic(v) -> acc + v
      }
    )
    Box{contents: body}
  }
}

fn main(): Box {
  dbg Box"hello"
  dbg Box{contents: "world"}
}
`, "dbg line 22: Box\"hello\" = Box{contents: \"hello\"}\ndbg line 23: Box{contents: \"world\"} = Box{contents: \"world\"}\n")
}

func TestIRTypedLiteral_TourDistinctHandler(t *testing.T) {
	verifyLambdaProgram(t, `import {
  std/io
  std/literals.{Fragment, Literal}
}

type Sql String

impl Literal for Sql {
  fn from_fragments(fragments: List<Fragment<String>>): Sql {
    body = Iter.reduce(fragments, |acc = "", frag|
      case frag {
        .Static(s) -> acc + s
        .Dynamic(v) -> acc + "'" + v + "'"
      }
    )
    Sql(body)
  }
}

fn main() {
  user = "alice"
  Sql(text) = Sql"SELECT * FROM users WHERE name = ${user}"
  io.print(text)
}
`, "SELECT * FROM users WHERE name = 'alice'\n")
}

func TestIRTypedLiteral_SlotOrderAndPayloadKinds(t *testing.T) {
	verifyLambdaProgram(t, `import {
  std/io
  std/literals.{Fragment, Literal}
}

struct Sum {
  total: Int
  text: String
}

impl Literal for Sum {
  fn from_fragments(fragments: List<Fragment<Int>>): Sum {
    Iter.reduce(fragments, |acc = Sum{total: 0, text: ""}, frag|
      case frag {
        .Static(s) -> Sum{total: acc.total, text: acc.text + s}
        .Dynamic(n) -> Sum{total: acc.total + n, text: acc.text + "#"}
      }
    )
  }
}

fn mark(n: Int): Int {
  io.print(n)
  n
}

fn main(): Sum {
  dbg Sum"a ${mark(1)} b ${mark(2)} c ${mark(3)}"
  dbg Sum"${mark(4)}"
  dbg Sum"plain"
}
`, "1\n2\n3\ndbg line 28: Sum\"a ${mark(1)} b ${mark(2)} c ${mark(3)}\" = Sum{total: 6, text: \"a # b # c #\"}\n4\ndbg line 29: Sum\"${mark(4)}\" = Sum{total: 4, text: \"#\"}\ndbg line 30: Sum\"plain\" = Sum{total: 0, text: \"plain\"}\n")
}
