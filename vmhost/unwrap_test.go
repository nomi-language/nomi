package vmhost_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// bangProgram is the declarations every `!` call case runs against: a
// sibling file, a local function, a function value, an interface's
// function, a generic function, and errors with and without Display.
const bangProgram = `import std/io
import helper

struct Oops {
    code: Int
}

struct Nice {
    why: String
}

impl Display for Nice {
    fn to_string(n: Nice) -> String {
        "nice: ${n.why}"
    }
}

struct User {
    name: String
    nickname: Maybe<String>
}

enum Color {
    Red
    Green
}

interface Parse {
    fn parse(text: String) -> Maybe<self>
}

impl Parse for Int {
    fn parse(text: String) -> Maybe<Int> {
        String.to_int(text)
    }
}

fn half(n: Int) -> Result<Int, String> {
    if n % 2 == 0 {
        Ok(n / 2)
    } else {
        Err("${n} is odd")
    }
}

fn oops(n: Int) -> Result<Int, Oops> {
    if n > 0 { Ok(n) } else { Err(#Oops{code: n}) }
}

fn nice(n: Int) -> Result<Int, Nice> {
    if n > 0 { Ok(n) } else { Err(#Nice{why: "too small"}) }
}

fn first<T>(xs: List<T>) -> Maybe<T> {
    List.head(xs)
}

fn pick(_u: User, _c: Color, _t: #(Int, String), _cp: Codepoint, _d: Decimal, _f: Float, _b: Bool, _s: String) -> Maybe<Int> {
    None
}

fn main() {
    fn local(n: Int) -> Maybe<Int> {
        if n > 0 { Some(n) } else { None }
    }
    f = local
    m = %{"a" => 1}
    _ = #(f(1), m, helper.twice(1))
    io.print("%BODY%")
}
`

const bangHelper = `pub fn twice(n: Int) -> Maybe<Int> {
    if n < 100 { Some(n * 2) } else { None }
}
`

// runBang writes bangProgram with body as main's last line, and helper.nomi
// beside it, and runs it: the output and the run's error text.
func runBang(t *testing.T, body string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	src := strings.Replace(bangProgram, `    io.print("%BODY%")`, body, 1)
	if err := os.WriteFile(filepath.Join(dir, "main.nomi"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "helper.nomi"), []byte(bangHelper), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := vmhost.Load(filepath.Join(dir, "main.nomi"))
	if err != nil {
		t.Fatalf("%s: the front end rejects the program: %v", body, err)
	}
	var out bytes.Buffer
	runErr := p.Run(context.Background(), &out, nil, false)
	if runErr == nil {
		return out.String(), ""
	}
	return out.String(), runErr.Error()
}

// bodyLine is the line of main's last statement in bangProgram.
var bodyLine = strings.Count(bangProgram[:strings.Index(bangProgram, "%BODY%")], "\n") + 1

// A postfix `!` answers the Ok's or the Some's payload, over every kind of
// expression.
func TestUnwrap_EveryFormAnswersThePayload(t *testing.T) {
	cases := []struct{ body, want string }{
		{`    io.print("${half(10)!}")`, "5\n"},
		{`    io.print("${local(5)!}")`, "5\n"},
		{`    io.print("${f(6)!}")`, "6\n"},
		{`    io.print("${helper.twice(7)!}")`, "14\n"},
		{`    io.print("${Int.parse("9")!}")`, "9\n"},
		{`    io.print("${first([10])!} ${first<Int>([11])!}")`, "10 11\n"},
		{`    io.print("${Map.get(m, "a")!}")`, "1\n"},
		{`    io.print("${"12" |> String.to_int()! |> Int.abs()}")`, "12\n"},
		{`    io.print("${Iter.map([2, 4], &half(&1)!) |> Iter.to_list()}")`, "[1, 2]\n"},
		{`    io.print("${#User{name: "a", nickname: Some("b")}.nickname!}")`, "b\n"},
		{`    io.print("${(8 |> half())! + 1}")`, "5\n"},
		{`    found = local(3)
    io.print("${found!}")`, "3\n"},
	}
	for _, tc := range cases {
		out, errText := runBang(t, tc.body)
		if out != tc.want || errText != "" {
			t.Errorf("%s: output %q, error %q; want %q", tc.body, out, errText, tc.want)
		}
	}
}

// A failed `!` traps at its line, naming the expression as written: an Err
// shows the error by Display when its type has one and by Debug otherwise,
// and a None says it found nothing.
func TestUnwrap_FailureTrapText(t *testing.T) {
	cases := []struct{ body, want string }{
		{`    io.print("${half(3)!}")`, "`half(3)` failed: 3 is odd"},
		{`    io.print("${oops(-1)!}")`, "`oops(-1)` failed: #Oops{code: -1}"},
		{`    io.print("${nice(-1)!}")`, "`nice(-1)` failed: nice: too small"},
		{`    io.print("${local(0)!}")`, "`local(0)` found nothing"},
		{`    io.print("${f(-2)!}")`, "`f(-2)` found nothing"},
		{`    io.print("${helper.twice(200)!}")`, "`helper.twice(200)` found nothing"},
		{`    io.print("${Map.get(m, "z")!}")`, "`Map.get(m, \"z\")` found nothing"},
		{`    io.print("${"abc" |> String.to_int()!}")`, "`\"abc\" |> String.to_int()` found nothing"},
		{`    io.print("${#User{name: "a", nickname: None}.nickname!}")`, "`#User{name: \"a\", nickname: None}.nickname` found nothing"},
		{`    io.print("${(9 |> half())! + 1}")`, "`(9 |> half())` failed: 9 is odd"},
		{`    io.print("${pick(#User{name: "a", nickname: None}, .Red, #(1, "x"), 'q', 1.50d, 2.5, True, "a long string")!}")`,
			"`pick(#User{name: \"a\", nickname: None}, .Red, #(1, \"x\"), 'q',...` found nothing"},
	}
	for _, tc := range cases {
		out, errText := runBang(t, tc.body)
		want := "line " + itoa(bodyLine) + ": " + tc.want
		if out != "" || errText != want {
			t.Errorf("%s:\n  output %q\n  error  %q\n  want   %q", tc.body, out, errText, want)
		}
	}
}

func itoa(n int) string {
	var b []byte
	for n > 0 || len(b) == 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
