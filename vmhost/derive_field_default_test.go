package vmhost

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// deriveFieldDefaults are struct field defaults of each expression kind a
// default can be. A derived FromJson used to place the default expression
// itself into its body, where the position walker refused any node kind it
// did not list (`#{}` crashed `nomi check`) and restamped the declaration's
// own positions into the synthesized band. The derived body now leaves a
// missing field out of the struct literal it builds, so the default is
// evaluated where struct literals evaluate it.
var deriveFieldDefaults = []struct {
	name, field, want string
}{
	{"int", "v: Int = 3", "3"},
	{"negated float", "v: Float = -0.5", "-0.5"},
	{"bool", "v: Bool = True", "True"},
	{"string", `v: String = "a"`, `"a"`},
	{"interpolation", `v: String = "n=${1 + 2}"`, `"n=3"`},
	{"list", "v: List<Int> = [1, 2]", "[1, 2]"},
	{"list spread", "v: List<Int> = [0, ..[1, 2]]", "[0, 1, 2]"},
	{"empty set", "v: Set<String> = #{}", "#{}"},
	{"set", `v: Set<String> = #{"a"}`, `#{"a"}`},
	{"set spread", `v: Set<String> = #{..#{"a"}, "b"}`, `#{"a", "b"}`},
	{"vector", "v: Vector<Int> = #[1, 2]", "#[1, 2]"},
	{"vector spread", "v: Vector<Int> = #[..#[1], 2]", "#[1, 2]"},
	{"map", `v: Map<String, Int> = %{"a" => 1}`, `%{"a" => 1}`},
	{"binary", "v: Int = 2 * 3 + 1", "7"},
	{"if", "v: Int = if 1 > 2 { 1 } else { 2 }", "2"},
	{"case", "v: Int = case 2 { 1 -> 10\n _ -> 20 }", "20"},
	{"block", "v: Int = {\n        x = 4\n        x + 1\n    }", "5"},
	{"pipe and lambda", "v: List<Int> = [1, 2] |> Iter.map(fn x -> x * 10) |> Iter.to_list()", "[10, 20]"},
	{"range", "v: List<Int> = 1..=3 |> Iter.to_list()", "[1, 2, 3]"},
	{"call", "v: Map<String, Int> = Map.empty()", "%{}"},
	{"struct literal", "v: Inner = Inner{a: 1}", "Inner{a: 1}"},
	{"struct update", "v: Inner = {..Inner{a: 1}, a: 2}", "Inner{a: 2}"},
	{"dot variant", "v: Color = .Red", "Red"},
}

func deriveFieldDefaultProgram(field string) string {
	return `import {
    std/io
    std/json.{FromJson, ToJson}
    std/json.Json
}

struct Inner {
    a: Int
}

derive ToJson for Inner
derive FromJson for Inner

enum Color {
    Red
    Blue
}

derive ToJson for Color
derive FromJson for Color

struct S {
    ` + field + `
}

derive ToJson for S
derive FromJson for S

enum E {
    Rec {
        ` + strings.ReplaceAll(field, "\n", "\n    ") + `
    }
}

derive ToJson for E
derive FromJson for E

fn main() {
    io.print(Debug.inspect(S.from_json(Json.Obj(Map.empty()))))
    io.print(Debug.inspect(E.from_json(Json.Obj%{"Rec" => Json.Obj(Map.empty())})))
    s = S{}
    io.print(Debug.inspect(S.from_json(ToJson.to_json(s)) == Ok(s)))
}
`
}

func TestDerivedJsonFillsEveryKindOfFieldDefault(t *testing.T) {
	for _, tc := range deriveFieldDefaults {
		t.Run(tc.name, func(t *testing.T) {
			src := deriveFieldDefaultProgram(tc.field)
			o := checkLowers(t.TempDir(), src)
			if o.internal != "" {
				t.Fatalf("compiler panic:\n%s\nsource:\n%s", o.internal, src)
			}
			if !o.accepted {
				t.Fatalf("the front end rejects this:\n%s\nsource:\n%s", o.rejection, src)
			}
			for _, d := range o.declines {
				t.Errorf("declined: %s", d)
			}
			for _, r := range o.unresolved {
				t.Errorf("untyped: %s", r)
			}
			p, err := LoadSource("main.nomi", src)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			var out bytes.Buffer
			if err := p.Run(context.Background(), &out, nil, false); err != nil {
				t.Fatalf("run: %v\noutput so far:\n%s", err, out.String())
			}
			want := "Ok(S{v: " + tc.want + "})\nOk(Rec{v: " + tc.want + "})\nTrue\n"
			if got := out.String(); got != want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

// A derived FromJson used to share the default's node with its body, so the
// position walker moved the default into the synthesized line band and a
// mistyped default was reported twice, once at a line no file has.
func TestDerivedFromJsonReportsABadDefaultAtItsSource(t *testing.T) {
	src := `import std/json.FromJson

struct Game {
    word: String
    misses: Int = "x"
}

derive FromJson for Game

fn main() {}
`
	o := checkLowers(t.TempDir(), src)
	if o.accepted {
		t.Fatalf("the front end accepts a String default for an Int field")
	}
	const want = "main.nomi:5:19: field 'misses' of Game: expected Int, got String"
	if !strings.Contains(o.rejection, want) {
		t.Fatalf("rejection does not contain %q:\n%s", want, o.rejection)
	}
	if n := strings.Count(o.rejection, "field 'misses' of Game"); n != 1 {
		t.Fatalf("the default is reported %d times, want once:\n%s", n, o.rejection)
	}
}
