package vmhost

import (
	"bytes"
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// displayRecordDecls are error types of each kind a failure's rendering
// can name an impl for: a program type's own impl, a derived one, a std
// type's (Codepoint), and a tuple with a part that has an impl.
const displayRecordDecls = `struct Bad
    why: String
end

impl Display for Bad
    fn to_string(b: Bad) -> String
        "bad: ${b.why}"
    end
end

struct Odd
    n: Int
end

derive Display for Odd

`

// A fixed typed literal whose handler fails is a compile error at the
// literal that shows the error through the impl its record names
// (TypedLiteral.Failure), in a generic function as anywhere.
func TestDisplayRecord_ATypedLiteralsFailureRendersByItsRecord(t *testing.T) {
	handler := func(tag, errType, err string) string {
		return "struct " + tag + "\n    n: Int\nend\n\n" +
			"impl Literal for " + tag + "\n" +
			"    fn from_fragments(_fragments: List<Fragment<String>>) -> Result<" + tag + ", " + errType + ">\n" +
			"        Err(" + err + ")\n" +
			"    end\nend\n\n"
	}
	src := "import std::literals.{Fragment, Literal}\n\n" + displayRecordDecls +
		handler("A", "Bad", `Bad{why: "no"}`) +
		handler("B", "Odd", "Odd{n: 3}") +
		handler("C", "Codepoint", "'q'") +
		handler("D", "(Int, Bad)", `(1, Bad{why: "t"})`) +
		"fn pair<T>(x: T) -> (T, A)\n    (x, A\"y\")\nend\n\n" +
		"fn main()\n    _ = (A\"x\", B\"x\", C\"x\", D\"x\", pair(1))\nend\n"
	var got []string
	for _, d := range literalDiagnostics(t, src) {
		got = append(got, d.Message)
	}
	slices.Sort(got)
	want := []string{
		`typed literal A"x" is invalid: bad: no`,
		`typed literal A"y" is invalid: bad: no`,
		`typed literal B"x" is invalid: Odd{n: 3}`,
		`typed literal C"x" is invalid: q`,
		`typed literal D"x" is invalid: (1, bad: t)`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("diagnostics:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// displayBangProgram fails a `!` over an error of each kind, directly and
// through a generic function's `!` at each instance.
const displayBangProgram = "import std::io\n\n" + displayRecordDecls + `fn bad() -> Result<Int, Bad>
    Err(Bad{why: "no"})
end

fn odd() -> Result<Int, Odd>
    Err(Odd{n: 3})
end

fn cp() -> Result<Int, Codepoint>
    Err('q')
end

fn tup() -> Result<Int, (Int, Bad)>
    Err((1, Bad{why: "t"}))
end

fn force<E>(r: Result<Int, E>) -> Int where E: Display
    r!
end

fn main()
    io.print("%BODY%")
end
`

// A failed `!` shows its error through the impl its record names
// (FileAnalysis.UnwrapFailures), and in a generic function through the one
// the record answers at the instance's types.
func TestDisplayRecord_AFailedBangRendersByItsRecord(t *testing.T) {
	lineOf := func(text string) string {
		return strconv.Itoa(strings.Count(displayBangProgram[:strings.Index(displayBangProgram, text)], "\n") + 1)
	}
	body, force := lineOf("%BODY%"), lineOf("    r!")
	for _, tc := range []struct{ call, want string }{
		{"bad()!", "line " + body + ": `bad()` failed: bad: no"},
		{"odd()!", "line " + body + ": `odd()` failed: Odd{n: 3}"},
		{"cp()!", "line " + body + ": `cp()` failed: q"},
		{"tup()!", "line " + body + ": `tup()` failed: (1, bad: t)"},
		{"force(bad())", "line " + force + ": `r` failed: bad: no"},
		{"force(odd())", "line " + force + ": `r` failed: Odd{n: 3}"},
		{"force(tup())", "line " + force + ": `r` failed: (1, bad: t)"},
	} {
		src := strings.Replace(displayBangProgram, `"%BODY%"`, tc.call, 1)
		p, err := LoadSource("main", src)
		if err != nil {
			t.Fatalf("%s: the program does not load: %v", tc.call, err)
		}
		var out bytes.Buffer
		runErr := p.Run(context.Background(), &out, nil, false)
		if runErr == nil || runErr.Error() != tc.want || out.Len() != 0 {
			t.Errorf("%s: output %q, error %v; want %q", tc.call, out.String(), runErr, tc.want)
		}
	}
}
