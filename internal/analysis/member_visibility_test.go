package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/parser"
)

// A function or `once` of an inherent impl is seen by no more files than
// see its type (spec §14 *Visibility consistency*): each pair of levels
// where the member outranks its type is one error at the member's name,
// with its Code, the type's keyword as declared, and a fix that gives the
// member its type's level. The fixed source checks clean, and the test
// fails if the source starts to check clean unfixed.
func TestMemberVisibility_AMemberOutrankingItsTypeIsOneError(t *testing.T) {
	for _, tc := range []struct {
		name, src  string
		line, col  int
		want       string
		fixedLine  string
		otherError string // an exposes error the member also gets, for another type
	}{
		{name: "a pub fn of an internal struct",
			src:  "struct Money\n    cents: Int\nend\n\nimpl Money\n    pub fn double(m: Money) -> Money\n        Money{cents: m.cents * 2}\n    end\nend\n",
			line: 6, col: 12,
			want:      "`double` is `pub`, but its type `Money` is internal: drop `pub`, or write `pub struct Money`",
			fixedLine: "    fn double(m: Money) -> Money"},
		{name: "a pub fn of a private struct",
			src:  "private struct Money\n    cents: Int\nend\n\nimpl Money\n    pub fn double(m: Money) -> Money\n        Money{cents: m.cents * 2}\n    end\nend\n",
			line: 6, col: 12,
			want:      "`double` is `pub`, but its type `Money` is private: write `private fn double`, or write `pub struct Money`",
			fixedLine: "    private fn double(m: Money) -> Money"},
		{name: "an internal fn of a private struct",
			src:  "private struct Money\n    cents: Int\nend\n\nimpl Money\n    fn double(m: Money) -> Money\n        Money{cents: m.cents * 2}\n    end\nend\n",
			line: 6, col: 8,
			want:      "`double` is internal, but its type `Money` is private: write `private fn double`, or drop `private` from `struct Money`",
			fixedLine: "    private fn double(m: Money) -> Money"},
		{name: "a pub once of an internal struct",
			src:  "struct Money\n    cents: Int\nend\n\nimpl Money\n    pub once zero: Money = Money{cents: 0}\nend\n",
			line: 6, col: 14,
			want:      "`zero` is `pub`, but its type `Money` is internal: drop `pub`, or write `pub struct Money`",
			fixedLine: "    once zero: Money = Money{cents: 0}"},
		{name: "an internal unannotated once of a private struct",
			src:  "private struct Money\n    cents: Int\nend\n\nimpl Money\n    once zero = Money{cents: 0}\nend\n",
			line: 6, col: 10,
			want:      "`zero` is internal, but its type `Money` is private: write `private once zero`, or drop `private` from `struct Money`",
			fixedLine: "    private once zero = Money{cents: 0}"},
		{name: "a pub fn whose signature names no type of its own",
			src:  "struct Money\n    cents: Int\nend\n\nimpl Money\n    pub fn zero() -> Int\n        0\n    end\nend\n",
			line: 6, col: 12,
			want:      "`zero` is `pub`, but its type `Money` is internal: drop `pub`, or write `pub struct Money`",
			fixedLine: "    fn zero() -> Int"},
		{name: "a pub fn of an internal opaque struct",
			src:  "opaque struct Money\n    cents: Int\nend\n\nimpl Money\n    pub fn zero() -> Int\n        0\n    end\nend\n",
			line: 6, col: 12,
			want:      "`zero` is `pub`, but its type `Money` is internal: drop `pub`, or write `pub opaque struct Money`",
			fixedLine: "    fn zero() -> Int"},
		{name: "an internal fn of a private enum",
			src:  "private enum Coin\n    Penny\n    Dime\nend\n\nimpl Coin\n    fn cents(c: Coin) -> Int\n        case c\n            .Penny -> 1\n            .Dime -> 10\n        end\n    end\nend\n",
			line: 7, col: 8,
			want:      "`cents` is internal, but its type `Coin` is private: write `private fn cents`, or drop `private` from `enum Coin`",
			fixedLine: "    private fn cents(c: Coin) -> Int"},
		{name: "a pub fn of an internal distinct type",
			src:  "type Cents Int\n\nimpl Cents\n    pub fn zero() -> Cents\n        Cents(0)\n    end\nend\n",
			line: 4, col: 12,
			want:      "`zero` is `pub`, but its type `Cents` is internal: drop `pub`, or write `pub type Cents`",
			fixedLine: "    fn zero() -> Cents"},
		{name: "a pub fn of an internal refinement",
			src:  "refine Small Int when fn n -> n < 10\n\nimpl Small\n    pub fn limit() -> Int\n        10\n    end\nend\n",
			line: 4, col: 12,
			want:      "`limit` is `pub`, but its type `Small` is internal: drop `pub`, or write `pub refine Small`",
			fixedLine: "    fn limit() -> Int"},
		{name: "a pub fn that also returns a private type",
			src:  "private struct Coin\n    n: Int\nend\n\nstruct Money\n    cents: Int\nend\n\nimpl Money\n    pub fn coin(m: Money) -> Coin\n        Coin{n: m.cents}\n    end\nend\n",
			line: 10, col: 12,
			want:       "`coin` is `pub`, but its type `Money` is internal: drop `pub`, or write `pub struct Money`",
			fixedLine:  "    fn coin(m: Money) -> Coin",
			otherError: "function return exposes private type Coin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, errs := checkSourceAsWrittenWithStdlib(tc.src)
			var member []analysis.TypeError
			other := false
			for _, e := range errs {
				switch {
				case e.Code == analysis.MemberMoreVisibleThanTypeCode:
					member = append(member, e)
				case e.Code == analysis.ExposesTypeCode && e.Message == tc.otherError:
					other = true
				default:
					t.Errorf("unexpected error: %s", e.Error())
				}
			}
			if len(member) != 1 {
				t.Fatalf("want one %s error, got %d: %v", analysis.MemberMoreVisibleThanTypeCode, len(member), errs)
			}
			e := member[0]
			if e.Message != tc.want {
				t.Errorf("message = %q\nwant      %q", e.Message, tc.want)
			}
			if e.Line != tc.line || e.Col != tc.col {
				t.Errorf("at %d:%d, want %d:%d", e.Line, e.Col, tc.line, tc.col)
			}
			if tc.otherError != "" && !other {
				t.Errorf("want also %q", tc.otherError)
			}
			if e.Fix == nil {
				t.Fatal("the error has no fix")
			}
			fixed := parser.ApplyEdits(tc.src, e.Fix.Edits)
			if got := strings.Split(fixed, "\n")[tc.line-1]; got != tc.fixedLine {
				t.Errorf("fixed line = %q\nwant         %q", got, tc.fixedLine)
			}
			_, _, again := checkSourceAsWrittenWithStdlib(fixed)
			for _, e := range again {
				if tc.otherError != "" && e.Message == tc.otherError {
					continue
				}
				t.Errorf("the fixed source is refused: %s\n%s", e.Error(), fixed)
			}
		})
	}
}

// A member no more visible than its type checks clean: a private member of
// any type, an internal member of an internal type, and a pub member of a
// pub type. An interface impl's functions take the interface's level and
// are not held to their type's.
func TestMemberVisibility_AMemberAtOrBelowItsTypesLevelChecks(t *testing.T) {
	src := "private struct Coin\n    n: Int\nend\n\nimpl Coin\n    private fn twice(c: Coin) -> Coin\n        Coin{n: c.n * 2}\n    end\nend\n\n" +
		"struct Money\n    cents: Int\nend\n\nimpl Money\n    fn double(m: Money) -> Money\n        Money{cents: m.cents * 2}\n    end\n\n    private once zero = Money{cents: 0}\nend\n\n" +
		"pub struct Open\n    n: Int\nend\n\nimpl Open\n    pub fn make() -> Open\n        Open{n: 1}\n    end\nend\n\n" +
		"impl Display for Coin\n    fn to_string(c: Coin) -> String\n        \"${c.n}\"\n    end\nend\n"
	_, _, errs := checkSourceAsWrittenWithStdlib(src)
	for _, e := range errs {
		t.Errorf("refused: %s", e.Error())
	}
}
