package irbuild

import "testing"

func TestIRTypedLiteral_TourStructHandler(t *testing.T) {
	verifyLambdaProgram(t, `import
  std::literals.{Fragment, Literal}
end

struct Box
  contents: String
end

impl Literal for Box
  fn from_fragments(fragments: List<Fragment<String>>) -> Box
    body = Iter.reduce(fragments, "", fn acc, frag ->
      case frag
        .Static(s) -> acc + s
        .Dynamic(v) -> acc + v
      end
    end
    )
    Box{contents: body}
  end
end

fn main()
  dbg Box"hello"
  dbg Box{contents: "world"}
end
`, "dbg line 23: Box\"hello\" = Box{contents: \"hello\"}\ndbg line 24: Box{contents: \"world\"} = Box{contents: \"world\"}\n")
}

func TestIRTypedLiteral_TourDistinctHandler(t *testing.T) {
	verifyLambdaProgram(t, `import
  std::io
  std::literals.{Fragment, Literal}
end

type Sql String

impl Literal for Sql
  fn from_fragments(fragments: List<Fragment<String>>) -> Sql
    body = Iter.reduce(fragments, "", fn acc, frag ->
      case frag
        .Static(s) -> acc + s
        .Dynamic(v) -> acc + "'" + v + "'"
      end
    end
    )
    Sql(body)
  end
end

fn main()
  user = "alice"
  Sql(text) = Sql"SELECT * FROM users WHERE name = ${user}"
  io.print(text)
end
`, "SELECT * FROM users WHERE name = 'alice'\n")
}

func TestIRTypedLiteral_SlotOrderAndPayloadKinds(t *testing.T) {
	verifyLambdaProgram(t, `import
  std::io
  std::literals.{Fragment, Literal}
end

struct Sum
  total: Int
  text: String
end

impl Literal for Sum
  fn from_fragments(fragments: List<Fragment<Int>>) -> Sum
    Iter.reduce(fragments, Sum{total: 0, text: ""}, fn acc, frag ->
      case frag
        .Static(s) -> Sum{total: acc.total, text: acc.text + s}
        .Dynamic(n) -> Sum{total: acc.total + n, text: acc.text + "#"}
      end
    end
    )
  end
end

fn mark(n: Int) -> Int
  io.print(n)
  n
end

fn main()
  dbg Sum"a ${mark(1)} b ${mark(2)} c ${mark(3)}"
  dbg Sum"${mark(4)}"
  dbg Sum"plain"
end
`, "1\n2\n3\ndbg line 29: Sum\"a ${mark(1)} b ${mark(2)} c ${mark(3)}\" = Sum{total: 6, text: \"a # b # c #\"}\n4\ndbg line 30: Sum\"${mark(4)}\" = Sum{total: 4, text: \"#\"}\ndbg line 31: Sum\"plain\" = Sum{total: 0, text: \"plain\"}\n")
}
