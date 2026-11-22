package vmhost_test

import (
	"fmt"
	"strings"
	"testing"
)

// A derived FromJson reads an enum's payload variant only from an object
// with exactly one key, the variant's name, as std's FromJson for Result
// reads `{"Ok": value}`: an object with no key, several keys or a key no
// payload variant has is refused, and the error names the enum and the keys.
// A bare variant is its name as a string, never an object. Each case is a
// type, the JSON read as it, and what the read answers.
func TestRun_DerivedEnumJsonReadsAnObjectWithOneKey(t *testing.T) {
	cases := []struct{ typ, json, want string }{
		// A payload variant.
		{"R", `{"Good": 4}`, `ok Good(4)`},
		{"R", `{"Bad": "x"}`, `ok Bad("x")`},
		{"R", `{"Good": 4, "Bad": "x"}`, `err expected R, got object with keys "Bad", "Good"`},
		{"R", `{}`, `err expected R, got object with no keys`},
		{"R", `{"Ugly": 1}`, `err expected R, got object with keys "Ugly"`},
		{"R", `{"Good": "4"}`, `err Good: expected Int, got string`},
		{"R", `"Good"`, `err expected R, got string`},
		{"R", `[1]`, `err expected R, got array`},
		// Bare variants alone.
		{"Color", `"Red"`, `ok Red`},
		{"Color", `"Purple"`, `err expected Color, got "Purple"`},
		{"Color", `{"Red": null}`, `err expected Color, got object`},
		// Bare, positional, tuple and struct variants together.
		{"Mixed", `"Off"`, `ok Off`},
		{"Mixed", `{"Off": null}`, `err expected Mixed, got object with keys "Off"`},
		{"Mixed", `"Level"`, `err expected Mixed, got "Level"`},
		{"Mixed", `{"Level": 3}`, `ok Level(3)`},
		{"Mixed", `{"Pair": [1, "a"]}`, `ok Pair(1, "a")`},
		{"Mixed", `{"Pair": [1, 2]}`, `err Pair.[1]: expected String, got int`},
		{"Mixed", `{"Named": {"x": 1}}`, `ok Named{x: 1, y: 0}`},
		{"Mixed", `{"Named": {"x": 1, "z": 9}}`, `ok Named{x: 1, y: 0}`},
		{"Mixed", `{"Named": {"x": "1"}}`, `err Named.x: expected Int, got string`},
		{"Mixed", `{"Level": 3, "Named": {"x": 1}}`, `err expected Mixed, got object with keys "Level", "Named"`},
		{"Mixed", `{"Level": 3, "Off": null}`, `err expected Mixed, got object with keys "Level", "Off"`},
		{"Mixed", `{}`, `err expected Mixed, got object with no keys`},
		{"Mixed", `7`, `err expected Mixed, got int`},
		// A generic enum.
		{"Wrap<Int>", `"Empty"`, `ok Empty`},
		{"Wrap<Int>", `{"Full": 2}`, `ok Full(2)`},
		{"Wrap<Int>", `{"Full": 2, "Empty": null}`, `err expected Wrap, got object with keys "Empty", "Full"`},
		// An enum in a struct's field: the path names the field, then the variant.
		{"Holder", `{"r": {"Good": 1}, "m": "Off"}`, `ok Holder{r: Good(1), m: Off}`},
		{"Holder", `{"r": {"Good": 1, "Bad": "x"}, "m": "Off"}`, `err r: expected R, got object with keys "Bad", "Good"`},
		{"Holder", `{"r": {"Good": 1}, "m": {}}`, `err m: expected Mixed, got object with no keys`},
		{"Holder", `{"r": {"Good": 1}, "m": {"Named": {"x": "s"}}}`, `err m.Named.x: expected Int, got string`},
		{"Holder", `{"r": {"Good": 1}, "m": "Off", "extra": 1}`, `ok Holder{r: Good(1), m: Off}`},
	}
	var calls strings.Builder
	for _, c := range cases {
		fmt.Fprintf(&calls, "    show(Json.decode_as<%s>(`%s`))\n", c.typ, c.json)
	}
	out := runOutput(t, `import
    std::io
    std::json.{FromJson, Json}
end

enum R
    Good Int
    Bad String
end

enum Color
    Red
    Green
end

enum Mixed
    Off
    Level Int
    Pair (Int, String)
    Named {x: Int, y: Int = 0}
end

enum Wrap<T>
    Empty
    Full T
end

struct Holder
    r: R
    m: Mixed
end

derive FromJson for R
derive FromJson for Color
derive FromJson for Mixed
derive FromJson for Wrap
derive FromJson for Holder

fn show<T>(read: Result<T, Json.Error>) where T: Debug
    case read
        Ok(v)  -> io.print("ok ${Debug.inspect(v)}")
        Err(e) -> io.print("err ${Display.to_string(e)}")
    end
end

fn main()
`+calls.String()+`end
`)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != len(cases) {
		t.Fatalf("%d lines for %d cases:\n%s", len(lines), len(cases), out)
	}
	for i, c := range cases {
		if lines[i] != c.want {
			t.Errorf("%s from %s: got %s, want %s", c.typ, c.json, lines[i], c.want)
		}
	}
	t.Logf("%d cases", len(cases))
}
