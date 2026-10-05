package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// A tail `else if` chain or `case` whose every arm ends in `return` leaves
// its region's exit unreachable, with the result slot never written. The exit
// used to return that slot anyway, which ir.Lint rejects ("nothing in this
// function defines t2"), and the lint violation panicked out of `nomi run`.
// A chain of bare returns at a body's end is the checker's "this `return`
// does nothing" error unless each follows a `dbg`, whose value the return
// discards, so the Unit chains here are written that way.
func TestIRReturnChain_EveryArmReturnsInTailPosition(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"else if chain", `import std/io
fn sign(x: Int): Int {
 if x < 0 {
  return -1
 } else if x == 0 {
  return 0
 } else {
  return 1
 }
}
fn main() {
 io.print(sign(-5))
 io.print(sign(0))
 io.print(sign(5))
}`, "-1\n0\n1\n"},
		{"case", `import std/io
enum Shape {
 Circle(Int)
 Square(Int)
 Dot
}
fn area(s: Shape): Int {
 case s {
  .Circle(r) -> {
   return 3 * r * r
  }
  .Square(w) -> {
   return w * w
  }
  .Dot -> {
   return 0
  }
 }
}
fn main() {
 io.print(area(Shape.Circle(2)))
 io.print(area(Shape.Square(3)))
 io.print(area(Shape.Dot))
}`, "12\n9\n0\n"},
		{"subjectless case", `import std/io
fn size(x: Int): String {
 case {
  x > 10 -> {
   return "big"
  }
  x > 0 -> {
   return "positive"
  }
  _ -> {
   return "other"
  }
 }
}
fn main() {
 io.print(size(11))
 io.print(size(1))
 io.print(size(0))
}`, "big\npositive\nother\n"},
		{"nested if and case", `import std/io
fn which(x: Int, y: Int): String {
 if x > 0 {
  if y > 0 {
   return "both"
  } else {
   return "x only"
  }
 } else if y > 0 {
  return "y only"
 } else {
  case x {
   0 -> {
    return "zero"
   }
   _ -> {
    return "neither"
   }
  }
 }
}
fn main() {
 io.print(which(1, 1))
 io.print(which(1, 0))
 io.print(which(0, 1))
 io.print(which(0, 0))
 io.print(which(-1, 0))
}`, "both\nx only\ny only\nzero\nneither\n"},
		{"pattern if chain", `import std/io
fn pick(m: Maybe<Int>, loud: Bool): String {
 if Some(n) = m {
  return "some ${n}"
 } else if loud {
  return "NONE"
 } else {
  return "none"
 }
}
fn main() {
 io.print(pick(Some(4), False))
 io.print(pick(None, True))
 io.print(pick(None, False))
}`, "some 4\nNONE\nnone\n"},
		{"some arms return and others fall through", `import std/io
fn mixed(x: Int): Int {
 if x < 0 {
  return -1
 } else if x == 0 {
  0
 } else {
  x * 2
 }
}
fn named(x: Int): String {
 case x {
  1 -> {
   return "one"
  }
  2 -> "two"
  _ -> {
   if x > 100 {
    return "huge"
   } else {
    return "many"
   }
  }
 }
}
fn main() {
 io.print(mixed(-3))
 io.print(mixed(0))
 io.print(mixed(4))
 io.print(named(1))
 io.print(named(2))
 io.print(named(200))
 io.print(named(3))
}`, "-1\n0\n8\none\ntwo\nhuge\nmany\n"},
		{"lambda", `import std/io
fn main() {
 sign: (Int) -> Int = |x| {
  if x < 0 {
   return -1
  } else if x == 0 {
   return 0
  } else {
   return 1
  }
 }
 name = |x: Int| {
  case x {
   0 -> {
    return "lz"
   }
   _ -> {
    return "lo"
   }
  }
 }
 io.print(sign(-2))
 io.print(sign(0))
 io.print(sign(2))
 io.print(name(0))
 io.print(name(9))
}`, "-1\n0\n1\nlz\nlo\n"},
		{"bare returns after dbg", `fn report(x: Int) {
 if x < 0 {
  dbg "neg"
  return
 } else if x == 0 {
  dbg "zero"
  return
 } else {
  dbg "pos"
  return
 }
}
fn report_case(x: Int) {
 case x {
  0 -> {
   dbg "case zero"
   return
  }
  _ -> {
   dbg "case other"
   return
  }
 }
}
fn main() {
 say = |x: Int| {
  if x > 0 {
   dbg "h pos"
   return
  } else {
   dbg "h other"
   return
  }
 }
 report(-1)
 report(0)
 report(1)
 report_case(0)
 report_case(5)
 say(1)
 say(-1)
}`, "dbg line 3: \"neg\" = \"neg\"\ndbg line 6: \"zero\" = \"zero\"\ndbg line 9: \"pos\" = \"pos\"\n" +
			"dbg line 16: \"case zero\" = \"case zero\"\ndbg line 20: \"case other\" = \"case other\"\n" +
			"dbg line 28: \"h pos\" = \"h pos\"\ndbg line 31: \"h other\" = \"h other\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}

// The unreachable exit a fully returning region leaves is terminated by a
// jump to itself, which reads nothing, and lint agrees nothing reaches it.
func TestIRReturnChain_ExitIsUnreachable(t *testing.T) {
	f := ir.NewFunc(ir.At("x.nomi", 1, 1), "f")
	entry := f.NewBlock(ir.At("x.nomi", 1, 1), "entry")
	exit := f.NewBlock(ir.At("x.nomi", 2, 1), "exit")
	entry.SetTerm(ir.NewReturnUnit(ir.At("x.nomi", 1, 1)))
	if got := ir.Reachable(f); !got[0] || got[1] {
		t.Fatalf("Reachable = %v, want [true false]", got)
	}
	exit.SetTerm(ir.NewJump(ir.At("x.nomi", 2, 1), exit.ID()))
	if err := ir.Lint(f); err != nil {
		t.Fatalf("an unreachable self-jump does not lint: %v", err)
	}
}
