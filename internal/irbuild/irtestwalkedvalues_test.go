package irbuild

import (
	"strings"
	"testing"
)

// A walk-only test body takes the value constructions and host calls a named
// function's body takes: tuples, map, set and vector literals and their
// operations, a Decimal literal and a tagged literal with a local handler.
// The test-body reader spells none of them, so these bodies are the walk's Go
// and the VM's graph.
func TestIRTestBody_WalkedBodiesTakeCollections(t *testing.T) {
	const src = `import {
  std/io
  std/literals.{Fragment, Literal}
}

struct Upper {
  text: String
}

impl Literal for Upper {
  fn from_fragments(fragments: List<Fragment<String>>): Upper {
    text = Iter.reduce(fragments, |acc = "", frag|
      case frag {
        .Static(s) -> acc + String.to_upper(s)
        .Dynamic(v) -> acc + v
      }
    )
    Upper{text}
  }
}

test "a tuple" {
  pair = (1, "one")
  assert pair.0 == 1
  assert pair.1 == "uno"
}

test "a map" {
  m = {"a" => 1, "b" => 2}
  assert Map.size(Map.put(m, "c", 3)) == 3
  assert Map.size(m) == 3
}

test "a set and a vector" {
  s = #{1, 2, 2}
  v = #[1, 2, 3]
  assert Set.size(s) == 2
  assert Vector.length(v) == 4
}

test "a decimal" {
  assert 1.10d + 2.20d == 3.30d
  assert 1.5d == 2.5d
}

test "a List host call in a case arm" {
  case [1, 2] {
    [] -> assert False
    [_, ..t] -> assert List.head(t) == Some(3)
  }
}

test "a tagged literal" {
  u = Upper"abc"
  io.print(u.text)
  assert u.text == "abc"
}
`
	out := irTestBodyVM(t, src, 6)
	for _, want := range []string{`= {"a" => 1, "b" => 2}`, "= #[1, 2, 3]", `= "ABC"`} {
		if !strings.Contains(out, want) {
			t.Errorf("the report lacks %q:\n%s", want, out)
		}
	}
}

// An equality with a bare `None` on either side takes the other operand's
// type, and the failure rows match the recorded ones.
func TestIRTestBody_BareNoneTakesTheOtherOperandsType(t *testing.T) {
	const src = `fn find(n: Int): Maybe<Int> {
  if n > 0 {
    Some(n)
  } else {
    None
  }
}

test "a missing value" {
  assert find(0) == None
  assert None == find(-1)
}

test "a present value" {
  assert find(3) == None
}

test "reversed" {
  assert None == find(4)
}
`
	out := irTestBodyVM(t, src, 3)
	if !strings.Contains(out, "find(3)") {
		t.Errorf("the report lacks the failing operand row:\n%s", out)
	}
}

// A std host outside rt reached from a body built after the module walk crosses
// to the binding's name, which is the name the extern table holds, and not to
// the Go host key the builder interned the declaration under.
func TestIRTestBody_ExternHostsCrossByBindingName(t *testing.T) {
	const src = `import std/calendar.Date

test "formats parsed date" {
  d = try Date.parse("2026-05-04")

  assert Date.to_string(d) == "2026-05-04"
}

test "a wrong date" {
  d = try Date.parse("2026-05-04")

  assert Date.to_string(d) == "2026-05-05"
}
`
	out := irTestBodyVM(t, src, 2)
	if !strings.Contains(out, `"2026-05-04"`) {
		t.Errorf("the report lacks the rendered date:\n%s", out)
	}
}
