package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

const pipeHeadDecls = `type Fahrenheit Decimal

impl Display for Fahrenheit {
  fn to_string(Fahrenheit(n)) -> String {
    "${n} F"
  }
}

type Celsius Decimal

impl Celsius {
  fn to_fahrenheit(Celsius(n)) -> Fahrenheit {
    Fahrenheit(n * 9d / 5d + 32d)
  }
}

struct Plain {
  x: Int
}

fn half(x: Float) -> Float {
  x / 2.0
}

fn dec(x: Decimal) -> Decimal {
  x
}

fn label(width: Int, text: String) -> String {
  String.pad_start(text, width)
}

fn total(xs: List<Int>) -> Int {
  Iter.count(xs)
}

fn first<T>(xs: List<T>) -> Maybe<T> {
  List.head(xs)
}

`

// A pipe stage that refuses the value piped into it is the error, so the
// error is on the stage, names it and the argument the value fills, and
// notes where the value comes from. Each case is still rejected.
func TestPipeHeadMismatch_IsReportedAtTheStage(t *testing.T) {
	for _, tc := range []struct {
		name, body, want, at, from string
	}{
		{"a Display value into a String stage",
			"  c = Celsius(1d)\n  _ = c |> Celsius.to_fahrenheit() |> String.pad_start(6)\n",
			"`String.pad_start` takes a String as its first argument, but the pipe passes a Fahrenheit\n" +
				"help: Fahrenheit implements Display; add `|> Display.to_string()` before this stage",
			"String.pad_start", "Celsius.to_fahrenheit()"},
		{"a distinct value into a stage that takes its inner type",
			"  cel = Celsius(1d)\n  _ = cel |> dec()\n",
			"`dec` takes a Decimal as its first argument, but the pipe passes a Celsius\n" +
				"help: to get the Decimal inside, destructure the Celsius: `Celsius(inner) = cel`",
			"dec", "cel"},
		{"an Iter into a List stage",
			"  _ = [1] |> Iter.map(fn x -> x + 1) |> total()\n",
			"`total` takes a List<Int> as its first argument, but the pipe passes an Iter<Int>\n" +
				"help: an Iter is not a collection; materialize it with `|> Iter.to_list()`",
			"total", "Iter.map(fn x -> x + 1)"},
		{"a Maybe into a stage that takes its payload",
			"  _ = \"4\" |> String.to_int() |> Int.to_string()\n",
			"`Int.to_string` takes an Int as its first argument, but the pipe passes a Maybe<Int>\n" +
				"help: a Maybe may be None; handle it before this stage: give a fallback with `|> Maybe.with_default(...)`, match it with `case`, or unwrap it with `try` in a function that returns a Maybe",
			"Int.to_string", "String.to_int()"},
		{"a Result into a stage that takes its payload",
			"  res: Result<Int, String> = Ok(4)\n  _ = res |> Int.to_string()\n",
			"`Int.to_string` takes an Int as its first argument, but the pipe passes a Result<Int, String>\n" +
				"help: a Result may be an Err; handle it before this stage: give a fallback with `|> Result.with_default(...)`, match it with `case`, or unwrap it with `try` in a function that returns a Result",
			"Int.to_string", "res"},
		{"an Int into a Float stage",
			"  _ = 3 |> half()\n",
			"`half` takes a Float as its first argument, but the pipe passes an Int\n" +
				"help: convert the Int first with `|> Int.to_float()`",
			"half", "3"},
		{"an Int into a Decimal stage",
			"  _ = 3 |> dec()\n",
			"`dec` takes a Decimal as its first argument, but the pipe passes an Int\n" +
				"help: convert the Int first with `|> Decimal.from_int()`",
			"dec", "3"},
		{"a Decimal into a Float stage",
			"  _ = 3d |> half()\n",
			"`half` takes a Float as its first argument, but the pipe passes a Decimal\n" +
				"help: convert the Decimal first with `|> Decimal.to_float()`",
			"half", "3d"},
		{"a Float into an Int stage has no conversion to offer",
			"  _ = 1.5 |> Int.to_string()\n",
			"`Int.to_string` takes an Int as its first argument, but the pipe passes a Float",
			"Int.to_string", "1.5"},
		{"a value with no Display into a String stage gets no Display help",
			"  _ = #Plain{x: 1} |> String.trim()\n",
			"`String.trim` takes a String as its first argument, but the pipe passes a Plain",
			"String.trim", "#Plain{x: 1}"},
		{"the slot a `_` names",
			"  c = Celsius(1d)\n  _ = c |> Celsius.to_fahrenheit() |> label(4, _)\n",
			"`label` takes a String as its second argument, but the pipe passes a Fahrenheit\n" +
				"help: Fahrenheit implements Display; add `|> Display.to_string()` before this stage",
			"_", "Celsius.to_fahrenheit()"},
		{"the slot a named `_` names",
			"  c = Celsius(1d)\n  _ = c |> Celsius.to_fahrenheit() |> label(width: 4, text: _)\n",
			"`label` takes a String as `text`, but the pipe passes a Fahrenheit\n" +
				"help: Fahrenheit implements Display; add `|> Display.to_string()` before this stage",
			"_", "Celsius.to_fahrenheit()"},
		{"the slot a named `_` names ahead of a positional argument",
			"  _ = 3 |> label(text: _, 4)\n",
			"`label` takes a String as `text`, but the pipe passes an Int\n" +
				"help: Int implements Display; add `|> Display.to_string()` before this stage",
			"_", "3"},
		{"a generic stage",
			"  _ = [1] |> Iter.map(fn x -> x + 1) |> first()\n",
			"`first` takes a List<T> as its first argument, but the pipe passes an Iter<Int>\n" +
				"help: an Iter is not a collection; materialize it with `|> Iter.to_list()`",
			"first", "Iter.map(fn x -> x + 1)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := pipeHeadDecls + "fn main() {\n" + tc.body + "}\n"
			_, errs := checkSourceWithStdlib(src)
			if len(errs) == 0 {
				t.Fatalf("the front end accepts this program, which pipes a value its stage does not take")
			}
			expectDiagAt(t, src, errs, tc.want, tc.at)
			e := findDiag(t, errs, tc.want)
			line, col := lastPos(src, tc.from)
			want := analysis.RelatedInfo{Line: line, Col: col, EndLine: line, EndCol: col + len(tc.from)}
			if len(e.Related) != 1 {
				t.Fatalf("related locations %+v, want one at %q", e.Related, tc.from)
			}
			r := e.Related[0]
			if r.File != "" || r.Line != want.Line || r.Col != want.Col || r.EndLine != want.EndLine || r.EndCol != want.EndCol {
				t.Fatalf("related location %+v, want %d:%d-%d:%d (%q)", r, want.Line, want.Col, want.EndLine, want.EndCol, tc.from)
			}
			if !strings.HasPrefix(r.Message, "the piped value is ") {
				t.Fatalf("related message %q", r.Message)
			}
		})
	}
}

// The mirror: a stage that takes what the pipe passes reports nothing.
func TestPipeHeadMismatch_AMatchingStageIsAccepted(t *testing.T) {
	src := pipeHeadDecls + `fn main() {
  c = Celsius(1d)
  _ = c |> Celsius.to_fahrenheit() |> Display.to_string() |> String.pad_start(6)
  _ = "x" |> label(4, _)
  _ = "x" |> label(width: 4, text: _)
  _ = [1] |> Iter.map(fn x -> x + 1) |> Iter.to_list() |> total()
  _ = 3 |> Int.to_float() |> half()
}
`
	_, errs := checkSourceWithStdlib(src)
	for _, e := range errs {
		t.Errorf("%d:%d %s", e.Line, e.Col, diagText(e))
	}
}

func findDiag(t *testing.T, errs []analysis.TypeError, want string) analysis.TypeError {
	t.Helper()
	for _, e := range errs {
		if diagText(e) == want {
			return e
		}
	}
	t.Fatalf("no error reads %q", want)
	return analysis.TypeError{}
}

// lastPos is the 1-based line and byte column of the last occurrence of at
// in src.
func lastPos(src, at string) (int, int) {
	i := strings.LastIndex(src, at)
	return strings.Count(src[:i], "\n") + 1, i - strings.LastIndex(src[:i], "\n")
}
