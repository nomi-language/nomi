package hostgen

import (
	"errors"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

func int8ToInt(int8) int64                { return 0 }
func int32ToInt(int32) int64              { return 0 }
func uint64ToInt(uint64) int64            { return 0 }
func uint16ToInt(uint16) int64            { return 0 }
func rtIntToInt(rt.Int) int64             { return 0 }
func int64sToInt([]int64) int64           { return 0 }
func intCallback(func(int64) int64) int64 { return 0 }

// TestIntPositionsFitTheirGoInteger holds the generator to the range an Int
// position's declared type admits: a fixed-width Go integer takes it only
// when every value the type admits fits, read off the subtype chain's
// `when` bounds and an intersection's members. Anything wider is refused
// at generation, naming the type to declare, rather than refused by the
// adapter as the program runs. rt.Int takes any Int.
func TestIntPositionsFitTheirGoInteger(t *testing.T) {
	const source = `import
  int.{Int64, NonNegativeInt}
end

pub subtype Octet Int when fn n -> n >= 0 and n <= 255
pub subtype Flipped Int when fn n -> 0 <= n and 255 >= n
pub subtype Loose Int when fn n -> n >= 0 and Int.is_even(n)
pub subtype Strict Octet when fn n -> n < 100

pub host fn plain(x: Int) -> Int
pub host fn wide(x: Int64) -> Int
pub host fn octet(x: Octet) -> Int
pub host fn flipped(x: Flipped) -> Int
pub host fn loose(x: Loose) -> Int
pub host fn strict(x: Strict) -> Int
pub host fn both(x: NonNegativeInt and Int64) -> Int
pub host fn nonneg(x: NonNegativeInt) -> Int
pub host fn unsigned(x: NonNegativeInt and Int64) -> Int
pub host fn list(xs: List<Int>) -> Int
pub host fn list64(xs: List<Int64>) -> Int
pub host fn callback(f: (Int) -> Int) -> Int
pub host fn callback64(f: (Int) -> Int64) -> Int
`
	cases := []struct {
		key  string
		fn   any
		want string // "" when the binding generates
	}{
		{"t.plain", intToInt, "declare it `Int64` (std::int)"},
		{"t.plain", rtIntToInt, ""},
		{"t.wide", intToInt, ""},
		{"t.octet", uint16ToInt, ""},
		{"t.octet", intToInt, ""},
		{"t.octet", int8ToInt, "Octet admits Ints past that"},
		{"t.flipped", uint16ToInt, ""},
		// A conjunct of another form narrows the values and is ignored.
		{"t.loose", intToInt, "`Loose and Int64`"},
		{"t.strict", int8ToInt, ""},
		{"t.both", intToInt, ""},
		{"t.nonneg", intToInt, "`NonNegativeInt and Int64`"},
		{"t.nonneg", uint64ToInt, "`NonNegativeInt and Int64`"},
		{"t.plain", uint64ToInt, "declare it `NonNegativeInt and Int64`"},
		{"t.wide", uint64ToInt, "declare it `NonNegativeInt and Int64`"},
		{"t.unsigned", uint64ToInt, ""},
		{"t.wide", int32ToInt, "declare it a subtype whose `when` bounds it within -2147483648..2147483647"},
		{"t.octet", int32ToInt, ""},
		{"t.list", int64sToInt, "element: Go int64 holds an Int"},
		{"t.list64", int64sToInt, ""},
		// A callback's result is an Int the Go side receives.
		{"t.callback", intCallback, "callback: result: Go int64 holds an Int"},
		{"t.callback64", intCallback, ""},
	}
	src := func(m string) ([]byte, bool) {
		if m == "t" {
			return []byte(source), true
		}
		return stdSource(m)
	}
	for _, c := range cases {
		_, err := Generate(Table{
			Package:  "x",
			Source:   src,
			Funcs:    []FuncRow{{Name: c.key, Fn: c.fn}},
			ModuleOf: func(FuncRow) (string, error) { return "t", nil },
		})
		var rangeErr *IntRangeError
		switch {
		case c.want == "":
			if err != nil {
				t.Errorf("%s: refused: %v", c.key, err)
			}
		case err == nil:
			t.Errorf("%s: generated an adapter whose Go integer the declared type overflows", c.key)
		case !errors.As(err, &rangeErr):
			t.Errorf("%s: refusal %q is no IntRangeError, which internal/ffirun reports at the binding", c.key, err)
		case !strings.HasPrefix(err.Error(), c.key+": ") || !strings.Contains(err.Error(), c.want):
			t.Errorf("%s: refusal %q does not name the binding and say %q", c.key, err, c.want)
		}
	}
}
