package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const operatorHoverDecls = `import std/calendar.{Date, Days, Months}
import std/io

struct Version {
  major: Int
  minor: Int
}

derive Comparable for Version

struct Tag {
  name: String
}

impl Equatable for Tag {
  fn equal?(a: Tag, b: Tag) -> Bool {
    String.to_lower(a.name) == String.to_lower(b.name)
  }
}

struct Point {
  x: Int
}

fn bigger<T>(a: T, b: T) -> Bool where T: Comparable {
  a > b
}

`

// An operator answers with its operand and result types, what it does on
// them, and the impl the checker chose for it.
func TestHover_Operators(t *testing.T) {
	runShapeHoverCases(t, operatorHoverDecls, []shapeHoverCase{
		{"Int +", "fn main() {\n  io.inspect(1 ▮+ 2)\n}\n",
			[]string{"`Int` + `Int` → `Int`", "Checked: traps on overflow.", "`impl Add<Int, Int> for Int` in `std/int`."}, nil},
		{"Int -", "fn main() {\n  io.inspect(1 ▮- 2)\n}\n",
			[]string{"`Int` - `Int` → `Int`", "Checked: traps on overflow.", "`impl Subtract<Int, Int> for Int`"}, nil},
		{"Int *", "fn main() {\n  io.inspect(3 ▮* 2)\n}\n",
			[]string{"`Int` * `Int` → `Int`", "Checked: traps on overflow.", "`impl Multiply<Int, Int> for Int`"}, nil},
		{"Int / chooses Divide<Int, Int>, not Divide<NonZeroInt, Int>", "fn main() {\n  io.inspect(7 ▮/ 2)\n}\n",
			[]string{"`Int` / `Int` → `Int`", "Truncates toward zero; traps on division by zero.", "`impl Divide<Int, Int> for Int`"}, []string{"NonZeroInt"}},
		{"Int %", "fn main() {\n  io.inspect(-7 ▮% 3)\n}\n",
			[]string{"`Int` % `Int` → `Int`", "Remainder: takes the dividend's sign; `Int.modulo` takes the divisor's."}, []string{"impl"}},
		{"Decimal /", "fn main() {\n  io.inspect(10d ▮/ 4d)\n}\n",
			[]string{"`Decimal` / `Decimal` → `Decimal`", "Exact or traps; use `Decimal.divide` to round."}, nil},
		{"Decimal +", "fn main() {\n  io.inspect(1.50d ▮+ 1.5d)\n}\n",
			[]string{"Exact: never rounds."}, nil},
		{"Float +", "fn main() {\n  io.inspect(1.0 ▮+ 2.0)\n}\n",
			[]string{"`Float` + `Float` → `Float`", "IEEE 754: never traps."}, nil},
		{"String +", "fn main() {\n  io.inspect(\"a\" ▮+ \"b\")\n}\n",
			[]string{"`String` + `String` → `String`", "Concatenates."}, nil},
		{"Date + Days", "fn later(day: Date) -> Date {\n  day ▮+ Days(1)\n}\n",
			[]string{"`Date` + `Days` → `Date`", "`impl Add<Days, Date> for Date` in `std/calendar`."}, []string{"Months"}},
		{"Date + Months", "fn later(day: Date) -> Date {\n  day ▮+ Months(1)\n}\n",
			[]string{"`Date` + `Months` → `Date`", "`impl Add<Months, Date> for Date` in `std/calendar`."}, []string{"Days"}},
		{"a user Equatable", "fn main() {\n  io.inspect(#Tag{name: \"a\"} ▮== #Tag{name: \"A\"})\n}\n",
			[]string{"`Tag` == `Tag` → `Bool`", "Uses `impl Equatable for Tag` in this file."}, nil},
		{"structural equality", "fn main() {\n  io.inspect(#Point{x: 1} ▮!= #Point{x: 2})\n}\n",
			[]string{"`Point` != `Point` → `Bool`", "Structural: compares field by field."}, nil},
		{"Float ==", "fn main() {\n  io.inspect(1.0 ▮== 1.0)\n}\n",
			[]string{"`nan == nan` is `True`"}, nil},
		{"a derived Comparable", "fn main() {\n  io.inspect(#Version{major: 1, minor: 2} ▮< #Version{major: 1, minor: 3})\n}\n",
			[]string{"`Version` < `Version` → `Bool`", "Derived: `derive Comparable for Version`, which compares field by field in declaration order."}, nil},
		{"a std Comparable", "fn main() {\n  io.inspect(\"a\" ▮<= \"b\")\n}\n",
			[]string{"`String` <= `String` → `Bool`", "`impl Comparable for String` in `std/strings`."}, nil},
		{"a type parameter's bound", "fn main() {\n  io.inspect(bigger(1, 2))\n}\n\nfn more<T>(a: T, b: T) -> Bool where T: Comparable {\n  a ▮>= b\n}\n",
			[]string{"`T` >= `T` → `Bool`", "Through the bound `T: Comparable`."}, nil},
		{"and", "fn main() {\n  io.inspect(True ▮and False)\n}\n",
			[]string{"`Bool` and `Bool` → `Bool`", "the right side runs only when the left is `True`."}, nil},
		{"or", "fn main() {\n  io.inspect(True ▮or False)\n}\n",
			[]string{"the right side runs only when the left is `False`."}, nil},
		{"!", "fn main() {\n  io.inspect(▮!True)\n}\n",
			[]string{"Not: `Bool` → `Bool`"}, []string{"Short-circuits"}},
		{"..", "fn main() {\n  io.inspect(0▮..3)\n}\n",
			[]string{"```nomi\nRange<Int>\n```", "Exclusive"}, nil},
		{"..=", "fn main() {\n  io.inspect(0..▮=3)\n}\n",
			[]string{"```nomi\nRange<Int>\n```", "Inclusive"}, nil},
		{"unary - on Int", "fn main() {\n  n = 3\n  io.inspect(▮-n)\n}\n",
			[]string{"Negation: `Int` → `Int`", "Checked: traps on `Int.min_value`."}, nil},
		{"unary - on Float", "fn main() {\n  f = 3.0\n  io.inspect(▮-f)\n}\n",
			[]string{"Negation: `Float` → `Float`"}, []string{"traps"}},
		{"inside a capture", "fn main() {\n  io.inspect([1, 5] |> Iter.filter(&(&1 ▮> 2)) |> Iter.to_list())\n}\n",
			[]string{"`Int` > `Int` → `Bool`", "`impl Comparable for Int` in `std/int`."}, nil},
		{"inside a lambda", "fn main() {\n  io.inspect([1, 5] |> Iter.map(fn n -> n ▮* 2) |> Iter.to_list())\n}\n",
			[]string{"`Int` * `Int` → `Int`"}, nil},
		{"an operand that fails to type", "fn main() {\n  io.inspect(nope ▮+ 1)\n}\n",
			[]string{"… + `Int`"}, []string{"→"}},
		{"two operands that fail to type", "fn main() {\n  io.inspect(nope ▮+ nada)\n}\n",
			nil, nil},
	})
}

// operatorProject writes a project whose entry adds two values of a type
// whose Add impl another file writes, and answers the entry's path.
func operatorProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"nomi.toml": "[module]\nname = \"ops\"\n",
		"money.nomi": `pub struct Money {
  cents: Int
}

impl Add<Money, Money> for Money {
  fn add(a: Money, b: Money) -> Money {
    #Money{cents: a.cents + b.cents}
  }
}
`,
		"main.nomi": `import std/io
import money.Money

fn main() {
  io.inspect(#Money{cents: 1} + #Money{cents: 2})
}
`,
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(root, "main.nomi")
}

// An Add impl another file writes is named with its file on the `+` that
// uses it, and go-to-definition on the `+` opens its `fn add`.
func TestOperator_UserImplInAnotherFile(t *testing.T) {
	mainPath := operatorProject(t)
	text, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	uri := pathToURI(mainPath)
	s := NewServer()
	s.docs.Open(uri, string(text))
	pos := protocol.Position{Line: 4, Character: uint32(strings.Index(strings.Split(string(text), "\n")[4], "+"))}

	hover, err := s.textDocumentHover(nil, &protocol.HoverParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}, Position: pos}})
	if err != nil || hover == nil {
		t.Fatalf("hover = %v, %v", hover, err)
	}
	want := "`Money` + `Money` → `Money`\n\n`impl Add<Money, Money> for Money` in `money.nomi`."
	if got := hover.Contents.(protocol.MarkupContent).Value; got != want {
		t.Errorf("hover = %q, want %q", got, want)
	}
	if hover.Range == nil || hover.Range.Start != pos || hover.Range.End.Character != pos.Character+1 {
		t.Errorf("hover range = %v, want the `+` at %v", hover.Range, pos)
	}

	loc := mustDefinitionLocation(t, s, uri, pos)
	wantURI := protocol.DocumentUri(pathToURI(filepath.Join(filepath.Dir(mainPath), "money.nomi")))
	if loc.URI != wantURI || loc.Range.Start.Line != 5 || loc.Range.Start.Character != 5 {
		t.Errorf("definition = %s %v, want %s at the `add` of line 6", loc.URI, loc.Range.Start, wantURI)
	}
}

// Go-to-definition on an operator opens the function of the impl the
// checker chose: a std impl among several (`Date + Days`), a scalar's
// `host fn` in std, and nothing for `%`, which has no impl, or for a
// derived impl, which has no function in the source.
func TestDefinition_Operators(t *testing.T) {
	stdLine := func(file, header string) uint32 {
		text, err := os.ReadFile(filepath.Join("..", "..", "std", file))
		if err != nil {
			t.Fatal(err)
		}
		for i, l := range strings.Split(string(text), "\n") {
			if strings.HasPrefix(l, header) {
				// The impl's one function is the next line that is not a
				// doc comment or a prompt.
				for j, next := range strings.Split(string(text), "\n")[i+1:] {
					if trimmed := strings.TrimSpace(next); !strings.HasPrefix(trimmed, "//") {
						return uint32(i + 1 + j)
					}
				}
			}
		}
		t.Fatalf("std/%s has no %q", file, header)
		return 0
	}
	cases := []struct {
		name, body string
		file       string // "" for no definition
		line       uint32
	}{
		{"Date + Days", "fn later(day: Date) -> Date {\n  day ▮+ Days(1)\n}\n", "calendar", stdLine("calendar.nomi", "impl Add<Days, Date> for Date")},
		{"Date + Months", "fn later(day: Date) -> Date {\n  day ▮+ Months(1)\n}\n", "calendar", stdLine("calendar.nomi", "impl Add<Months, Date> for Date")},
		{"Int +", "fn main() {\n  io.inspect(1 ▮+ 2)\n}\n", "int", stdLine("int.nomi", "impl Add<Int, Int> for Int")},
		{"Int <", "fn main() {\n  io.inspect(1 ▮< 2)\n}\n", "int", stdLine("int.nomi", "impl Comparable for Int")},
		{"Int %", "fn main() {\n  io.inspect(7 ▮% 2)\n}\n", "", 0},
		{"a derived Comparable", "fn main() {\n  io.inspect(#Version{major: 1, minor: 2} ▮< #Version{major: 1, minor: 3})\n}\n", "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			input, pos := definitionMarkerPosition(t, operatorHoverDecls+c.body)
			uri := "file:///operator_definition.nomi"
			s := NewServer()
			s.docs.Open(uri, input)
			result, err := s.textDocumentDefinition(nil, &protocol.DefinitionParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}, Position: pos}})
			if err != nil {
				t.Fatal(err)
			}
			if c.file == "" {
				if result != nil {
					t.Fatalf("definition = %v, want none", result)
				}
				return
			}
			loc, ok := result.(*protocol.Location)
			if !ok {
				t.Fatalf("definition = %T %v, want a location", result, result)
			}
			if want := protocol.DocumentUri(s.std.FileURI(c.file)); loc.URI != want || loc.Range.Start.Line != c.line {
				t.Errorf("definition = %s line %d, want %s line %d", loc.URI, loc.Range.Start.Line, want, c.line)
			}
		})
	}
}
