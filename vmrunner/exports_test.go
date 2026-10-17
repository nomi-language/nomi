package vmrunner_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
	"github.com/nomi-language/nomi/vmrunner"
)

// webImage lowers src as `nomi build --web` does and answers its image.
func webImage(t *testing.T, src string) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := vmhost.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	img, _, err := p.BuildExportsImage()
	if err != nil {
		t.Fatal(err)
	}
	return img
}

const hangman = `import std/io
import std/json.{ToJson, FromJson}

struct Game {
    word: String
    guesses: List<String>
}

derive ToJson for Game
derive FromJson for Game

enum Outcome {
    Won
    Lost
    Playing {left: Int}
}

derive ToJson for Outcome
derive FromJson for Outcome

once alphabet = load_alphabet()

fn load_alphabet() -> String {
    io.print("loading the alphabet")
    "abcdefghijklmnopqrstuvwxyz"
}

export fn new_game(word: String) -> Game {
    Game{word, guesses: []}
}

export fn guess(game: Game, letter: String) -> Game {
    {..game, guesses: [letter, ..game.guesses]}
}

export fn outcome(game: Game) -> Outcome {
    left = 6 - Iter.count(game.guesses)
    if left <= 0 { Outcome.Lost } else { Outcome.Playing{left} }
}

export fn describe(o: Outcome) -> String {
    case o {
        Outcome.Won -> "won"
        Outcome.Lost -> "lost"
        Outcome.Playing{left} -> "${left} left"
    }
}

export fn divide(a: Int, b: Int) -> Int {
    a / b
}

export fn huge() -> Int {
    9007199254740993
}

export fn shout(text: String) {
    io.print(String.to_upper(text))
}

export fn letters() -> Int {
    String.length(alphabet)
}

export fn mean(xs: List<Float>) -> Float {
    Iter.reduce(xs, fn sum = 0.0, x -> sum + x) / Int.to_float(Iter.count(xs))
}

export fn unique(xs: Vector<String>) -> Set<String> {
    Iter.to_set(xs)
}
`

func start(t *testing.T, src string) (*vmrunner.Instance, *bytes.Buffer) {
	t.Helper()
	var out, errOut bytes.Buffer
	in, err := vmrunner.Start(webImage(t, src), &out, &errOut, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { in.Close() })
	return in, &out
}

func call(t *testing.T, in *vmrunner.Instance, name, args string) string {
	t.Helper()
	got, err := in.Call(name, []byte(args))
	if err != nil {
		t.Fatalf("%s(%s): %v", name, args, err)
	}
	return string(got)
}

func callFails(t *testing.T, in *vmrunner.Instance, name, args, want string) {
	t.Helper()
	got, err := in.Call(name, []byte(args))
	if err == nil {
		t.Fatalf("%s(%s) answered %s, want the error %q", name, args, got, want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("%s(%s): error %q, want it to contain %q", name, args, err, want)
	}
}

func TestInstance_CallsRoundTripStructsAndEnums(t *testing.T) {
	in, _ := start(t, hangman)
	game := call(t, in, "new_game", `["pipeline"]`)
	if game != `{"word":"pipeline","guesses":[]}` {
		t.Fatalf("new_game answered %s", game)
	}
	game = call(t, in, "guess", `[`+game+`, "e"]`)
	if game != `{"word":"pipeline","guesses":["e"]}` {
		t.Fatalf("guess answered %s", game)
	}
	o := call(t, in, "outcome", `[`+game+`]`)
	if o != `{"Playing":{"left":5}}` {
		t.Fatalf("outcome answered %s", o)
	}
	if got := call(t, in, "describe", `[`+o+`]`); got != `"5 left"` {
		t.Fatalf("describe answered %s", got)
	}
	if got := call(t, in, "describe", `["Lost"]`); got != `"lost"` {
		t.Fatalf("describe answered %s", got)
	}
	// A whole JSON number is a Float argument too, as JavaScript sends 2.0.
	if got := call(t, in, "mean", `[[1, 2.5, 3]]`); got != "2.1666666666666665" {
		t.Fatalf("mean answered %s", got)
	}
	// A Vector and a Set cross as arrays.
	if got := call(t, in, "unique", `[["a", "a"]]`); got != `["a"]` {
		t.Fatalf("unique answered %s", got)
	}
}

func TestInstance_EachFailureIsAnErrorAndTheNextCallRuns(t *testing.T) {
	in, _ := start(t, hangman)
	callFails(t, in, "divide", `[1, 0]`, "line 50: division by zero")
	if got := call(t, in, "divide", `[7, 2]`); got != "3" {
		t.Fatalf("divide after a trap answered %s", got)
	}
	callFails(t, in, "nope", `[]`, "no exported function `nope`; the exported functions are describe, divide, guess, huge, letters, mean, new_game, outcome, shout, unique")
	callFails(t, in, "divide", `[1]`, "`divide(a: Int, b: Int) -> Int` takes 2 arguments, and was called with 1")
	callFails(t, in, "divide", `{"a": 1}`, "the arguments to `divide(a: Int, b: Int) -> Int` must be a JSON array")
	callFails(t, in, "divide", `[1, "2"]`, "argument `b` of `divide(a: Int, b: Int) -> Int`: expected int, got string")
	callFails(t, in, "guess", `[{"word": "x"}, "e"]`, "argument `game` of `guess(game: Game, letter: String) -> Game`: guesses: expected List<String>, got missing")
	callFails(t, in, "describe", `["Drawn"]`, `argument `+"`o`"+` of `+"`describe(o: Outcome) -> String`"+`: expected Outcome, got "Drawn"`)
	callFails(t, in, "huge", `[]`, "the result of `huge` holds the Int 9007199254740993, outside ±2^53: a JSON number cannot hold it exactly")
	if got := call(t, in, "new_game", `["ok"]`); got != `{"word":"ok","guesses":[]}` {
		t.Fatalf("new_game after the failures answered %s", got)
	}
}

func TestInstance_PrintsAndSharesOnceValuesAcrossCalls(t *testing.T) {
	in, out := start(t, hangman)
	if got := call(t, in, "shout", `["hi"]`); got != "null" {
		t.Fatalf("a Unit result answered %s", got)
	}
	for range 2 {
		if got := call(t, in, "letters", `[]`); got != "26" {
			t.Fatalf("letters answered %s", got)
		}
	}
	if out.String() != "HI\nloading the alphabet\n" {
		t.Fatalf("output %q, want the print and one forcing of the once value", out.String())
	}
}

func TestInstance_RunsMainFirst(t *testing.T) {
	src := "import std/io\n\nonce count = counted()\n\nfn counted() -> Int {\n    io.print(\"counted\")\n    3\n}\n\n" +
		"fn main() {\n    io.print(\"main ${count}\")\n}\n\nexport fn twice() -> Int {\n    count * 2\n}\n"
	in, out := start(t, src)
	if got := call(t, in, "twice", `[]`); got != "6" {
		t.Fatalf("twice answered %s", got)
	}
	if out.String() != "counted\nmain 3\n" {
		t.Fatalf("output %q", out.String())
	}
	if ex := in.Exports(); len(ex) != 1 || ex[0].Signature != "twice() -> Int" {
		t.Fatalf("exports %+v", ex)
	}
}

func TestInstance_AFailingMainIsTheStartError(t *testing.T) {
	src := "fn main() -> Result<Unit, String> {\n    Err(\"no setup\")\n}\n\nexport fn one() -> Int {\n    1\n}\n"
	var out, errOut bytes.Buffer
	_, err := vmrunner.Start(webImage(t, src), &out, &errOut, nil)
	if err == nil || err.Error() != "error: no setup" {
		t.Fatalf("Start answered %v, want main's failure", err)
	}
}

func TestInstance_ExportsListsSignatures(t *testing.T) {
	in, _ := start(t, hangman)
	var got []string
	for _, e := range in.Exports() {
		got = append(got, e.Signature)
	}
	want := "new_game(word: String) -> Game; guess(game: Game, letter: String) -> Game; outcome(game: Game) -> Outcome; " +
		"describe(o: Outcome) -> String; divide(a: Int, b: Int) -> Int; huge() -> Int; shout(text: String); letters() -> Int; mean(xs: List<Float>) -> Float; unique(xs: Vector<String>) -> Set<String>"
	if strings.Join(got, "; ") != want {
		t.Fatalf("exports\n%s\nwant\n%s", strings.Join(got, "; "), want)
	}
}

// Boot does not run main; Main runs it over the stdin Boot was given, as
// often as it is asked, on the booted program.
func TestInstance_BootLeavesMainToTheHost(t *testing.T) {
	src := "import std/io\n\nfn main() {\n    case io.read_line() {\n        Ok(line) -> io.print(\"main read ${line}\")\n        Err(_) -> io.print(\"main read nothing\")\n    }\n}\n\n" +
		"export fn one() -> Int {\n    1\n}\n"
	var out, errOut bytes.Buffer
	in, err := vmrunner.Boot(webImage(t, src), &out, &errOut, strings.NewReader("a line\n"), nil)
	if err != nil {
		t.Fatalf("Boot: %v", err)
	}
	defer in.Close()
	if out.Len() != 0 {
		t.Fatalf("Boot ran main: %q", out.String())
	}
	if got := call(t, in, "one", `[]`); got != "1" {
		t.Fatalf("one answered %s", got)
	}
	for range 2 {
		if err := in.Main(); err != nil {
			t.Fatalf("Main: %v", err)
		}
	}
	if out.String() != "main read a line\nmain read nothing\n" {
		t.Fatalf("output %q", out.String())
	}
}

func TestInstance_MainOfAProgramWithNoMain(t *testing.T) {
	var out, errOut bytes.Buffer
	in, err := vmrunner.Boot(webImage(t, hangman), &out, &errOut, nil, nil)
	if err != nil {
		t.Fatalf("Boot: %v", err)
	}
	defer in.Close()
	if err := in.Main(); err != vmrunner.ErrNoMain {
		t.Fatalf("Main answered %v, want ErrNoMain", err)
	}
}

// CallResult wraps each answer as the wasip1 reactor's nomi_call returns
// it.
func TestInstance_CallResultIsOkOrError(t *testing.T) {
	in, _ := start(t, hangman)
	if got := string(in.CallResult("new_game", []byte(`["x"]`))); got != `{"ok":{"word":"x","guesses":[]}}` {
		t.Fatalf("new_game answered %s", got)
	}
	if got := string(in.CallResult("shout", []byte(`["x"]`))); got != `{"ok":null}` {
		t.Fatalf("shout answered %s", got)
	}
	got := in.CallResult("divide", []byte(`[1, 0]`))
	var answer struct {
		Ok    *json.RawMessage `json:"ok"`
		Error *string          `json:"error"`
	}
	if err := json.Unmarshal(got, &answer); err != nil || answer.Ok != nil || answer.Error == nil ||
		!strings.Contains(*answer.Error, "division by zero") {
		t.Fatalf("divide by zero answered %s (%v)", got, err)
	}
}
