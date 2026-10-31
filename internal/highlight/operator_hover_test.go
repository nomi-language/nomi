package highlight_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/highlight"
	"github.com/nomi-language/nomi/internal/hoverdoc"
	"github.com/nomi-language/nomi/std"
)

// The tour answers an operator as the editor does: its operand and result
// types, what it does on them, and the impl the checker chose.
func TestAnalyze_OperatorHovers(t *testing.T) {
	const src = `import std/calendar.{Date, Days, Months}
import std/io

struct Money {
  cents: Int
}

impl Add<Money, Money> for Money {
  fn add(a: Money, b: Money) -> Money {
    #Money{cents: a.cents + b.cents}
  }
}

struct Version {
  major: Int
  minor: Int
}

derive Comparable for Version

struct Tag {
  name: String
}

impl Equatable for Tag {
  fn equal(a: Tag, b: Tag) -> Bool {
    String.lowercase(a.name) == String.lowercase(b.name)
  }
}

struct Point {
  x: Int
}

fn later(day: Date) -> #(Date, Date) {
  #(day + Days(1), day + Months(1))
}

fn sizes(n: Int, d: Decimal, f: Float) -> #(Int, Decimal, Float) {
  #(n / 3 + n % 3 - -n, d * 2d, f * 2.0)
}

fn main() {
  m = #Money{cents: 1} + #Money{cents: 2}
  newer = #Version{major: 1, minor: 2} < #Version{major: 1, minor: 3}
  same = #Tag{name: "a"} == #Tag{name: "A"}
  origin = #Point{x: 0} != #Point{x: 1}
  ok = 1 < 2 and not newer or same
  big = [1, 5] |> Iter.filter(&(&1 > 2)) |> Iter.map(fn n -> n * 2) |> Iter.to_list()
  io.inspect(#(m, ok, origin, big, 0..3, 1..=3, later, sizes))
  bad = nope + 1
}
`
	lib := std.Load()
	fa, nodes := highlight.AnalyzeNodes(src, lib)
	if fa == nil {
		t.Fatal("highlight.AnalyzeNodes returned nil")
	}
	editor, editorNodes := editorDoc(t, lib, src)
	cases := []struct {
		name string
		// line starts the line that holds the operator, op is the
		// operator's text and nth its occurrence on the line (from 1).
		line, op string
		nth      int
		want     string
	}{
		{"Int +", "  #(n / 3", "+", 1, "`Int` + `Int` → `Int`\n\nExact at any size: never overflows.\n\n`impl Add<Int, Int> for Int` in `std/int`."},
		{"Int /", "  #(n / 3", "/", 1, "`Int` / `Int` → `Int`\n\nTruncates toward zero; traps on division by zero.\n\n`impl Divide<Int, Int> for Int` in `std/int`."},
		{"Int %", "  #(n / 3", "%", 1, "`Int` % `Int` → `Int`\n\nRemainder: takes the dividend's sign; `Int.modulo` takes the divisor's. Traps on division by zero."},
		{"unary - on Int", "  #(n / 3", "-n", 1, "Negation: `Int` → `Int`\n\nExact at any size."},
		{"Decimal *", "  #(n / 3", "*", 1, "`Decimal` * `Decimal` → `Decimal`\n\nExact: never rounds.\n\n`impl Multiply<Decimal, Decimal> for Decimal` in `std/decimal`."},
		{"Float *", "  #(n / 3", "*", 2, "`Float` * `Float` → `Float`\n\nIEEE 754: never traps.\n\n`impl Multiply<Float, Float> for Float` in `std/float`."},
		{"a user Add", "  m = #Money", "+", 1, "`Money` + `Money` → `Money`\n\n`impl Add<Money, Money> for Money` in this file."},
		{"Date + Days", "  #(day + Days", "+", 1, "`Date` + `Days` → `Date`\n\n`impl Add<Days, Date> for Date` in `std/calendar`."},
		{"Date + Months", "  #(day + Days", "+", 2, "`Date` + `Months` → `Date`\n\n`impl Add<Months, Date> for Date` in `std/calendar`."},
		{"a derived Comparable", "  newer =", "<", 1, "`Version` < `Version` → `Bool`\n\nDerived: `derive Comparable for Version`, which compares field by field in declaration order."},
		{"a user Equatable", "  same =", "==", 1, "`Tag` == `Tag` → `Bool`\n\nUses `impl Equatable for Tag` in this file."},
		{"structural equality", "  origin =", "!=", 1, "`Point` != `Point` → `Bool`\n\nStructural: compares field by field."},
		{"Int <", "  ok = 1", "<", 1, "`Int` < `Int` → `Bool`\n\n`impl Comparable for Int` in `std/int`."},
		{"and", "  ok = 1", "and", 1, "`Bool` and `Bool` → `Bool`\n\nShort-circuits: the right side runs only when the left is `True`."},
		{"or", "  ok = 1", "or", 1, "`Bool` or `Bool` → `Bool`\n\nShort-circuits: the right side runs only when the left is `False`."},
		{"not", "  ok = 1", "not", 1, "Not: `Bool` → `Bool`"},
		{"inside a capture", "  big =", "> 2", 1, "`Int` > `Int` → `Bool`\n\n`impl Comparable for Int` in `std/int`."},
		{"inside a lambda", "  big =", "*", 1, "`Int` * `Int` → `Int`\n\nExact at any size: never overflows.\n\n`impl Multiply<Int, Int> for Int` in `std/int`."},
		{"..", "  io.inspect(", "..", 1, "```nomi\nRange<Int>\n```\n\nExclusive: from the start up to, not including, the end."},
		{"..=", "  io.inspect(", "..=", 1, "```nomi\nRange<Int>\n```\n\nInclusive: from the start through the end."},
		{"an operand that fails to type", "  bad =", "+", 1, "… + `Int`"},
	}
	lines := strings.Split(src, "\n")
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			line := -1
			for i, l := range lines {
				if strings.HasPrefix(l, c.line) {
					line = i
					break
				}
			}
			if line < 0 {
				t.Fatalf("no line starts with %q", c.line)
			}
			col := -1
			for i := 0; i < c.nth; i++ {
				at := strings.Index(lines[line][col+1:], c.op)
				if at < 0 {
					t.Fatalf("line %q has no %q number %d", lines[line], c.op, c.nth)
				}
				col += at + 1
			}
			width := len(c.op)
			if c.op == "-n" || c.op == "> 2" {
				width = 1
			}
			for offset := 0; offset < width; offset++ {
				pos := analysis.Pos{Line: line + 1, Col: col + 1 + offset}
				if got := hoverdoc.At(fa, nodes, pos); got != c.want {
					t.Errorf("tour hover on character %d = %q, want %q", offset+1, got, c.want)
				}
				if got, _, _, _ := hoverdoc.SyntaxAt(editor, editorNodes, pos); got != c.want {
					t.Errorf("editor hover on character %d = %q, want %q", offset+1, got, c.want)
				}
			}
		})
	}
}
