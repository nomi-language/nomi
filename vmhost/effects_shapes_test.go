package vmhost

import (
	"strings"
	"testing"
)

// shapesPrelude is the interfaces the bound shapes below name: one with a
// function, a marker, and a generic one.
const shapesPrelude = `import std::fs

pub interface Describe
    fn describe(value: self) -> String
end

pub interface Marked end

pub interface Holds<V>
    fn holds(box: self, v: V) -> Bool
end
`

// Every bound shape a public generic function nothing instantiates may
// have is walked through its abstract body: an intersection bound, a
// marker, several type parameters each with its own bounds, a bound
// naming a generic interface, a type parameter used only inside another
// type, with no bound, and calls from one generic function to another at
// its type parameters, recursive and mutually recursive. Each reads a
// file, which an [effects.self] allowing nothing refuses at the read,
// reached from the public function, and none is reported as not
// determined; each passes once std names std::fs.
func TestEffectRoots_EveryBoundShapeIsWalkedThroughItsAbstractBody(t *testing.T) {
	cases := []struct {
		name, src, at, chain string
	}{
		{"an intersection bound", `
pub fn tell<T>(x: T, path: String) -> String
where T: Marked and Describe
    _ = fs.read_text(path)
    Describe.describe(x)
end
`, "15:9", "reached from `tell`"},
		{"a marker bound", `
pub fn mark<T>(x: T, path: String) -> T
where T: Marked
    _ = fs.read_text(path)
    x
end
`, "15:9", "reached from `mark`"},
		{"two type parameters, each bounded", `
pub fn pair<A, B>(a: A, b: B, path: String) -> String
where A: Describe, B: Marked and Describe
    _ = fs.read_text(path)
    "${Describe.describe(a)} ${Describe.describe(b)}"
end
`, "15:9", "reached from `pair`"},
		{"a generic interface bound", `
pub fn check<T>(box: T, path: String) -> Bool
where T: Holds<Int>
    _ = fs.read_text(path)
    Holds.holds(box, 1)
end
`, "15:9", "reached from `check`"},
		{"a type parameter only inside other types", `
pub fn first<T>(xs: List<T>, f: (T) -> Bool, path: String) -> Option<T>
    _ = fs.read_text(path)
    Iter.find(xs, f)
end
`, "14:9", "reached from `first`"},
		{"a type parameter inside a tuple", `
pub fn read_tagged<T>(tag: T, path: String) -> Result<(T, String), fs.Error>
    text = fs.read_text(path)?
    Ok((tag, text))
end
`, "14:12", "reached from `read_tagged`"},
		{"a generic calling a generic at its type parameter", `
pub fn outer<T>(x: T, path: String) -> String
where T: Describe
    inner(x, path)
end

fn inner<T>(x: T, path: String) -> String
where T: Describe
    _ = fs.read_text(path)
    Describe.describe(x)
end
`, "20:9", "reached from `outer`; `outer` calls `inner`"},
		{"recursion", `
pub fn count<T>(xs: List<T>, n: Int, path: String) -> Int
    if n == 0 then
        _ = fs.read_text(path)
        Iter.count(xs)
    else
        count(xs, n - 1, path)
    end
end
`, "15:13", "reached from `count`"},
		{"mutual recursion", `
pub fn ping<T>(x: T, n: Int, path: String) -> String
where T: Describe
    if n == 0 then Describe.describe(x) else pong(x, n - 1, path)
end

fn pong<T>(x: T, n: Int, path: String) -> String
where T: Describe
    _ = fs.read_text(path)
    ping(x, n, path)
end
`, "20:9", "reached from `ping`; `ping` calls `pong`"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := shapesPrelude + c.src
			got, ds := effectDiagnostics(t, "genlib.nomi", genericLibrary("[]", src))
			want := "genlib.nomi:" + c.at + ": this package's own code has the effect std::fs, and [effects.self] allows it no effects\n" +
				"genlib.nomi:" + c.at + ": help: " + c.chain + "\n" +
				"genlib.nomi:" + c.at + ": help: add \"std::fs\" to std under [effects.self] in nomi.toml, or remove the call"
			if got != want {
				t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
			}
			for _, d := range ds {
				if d.Code != EffectNotAllowedCode {
					t.Errorf("a %s diagnostic: %s; want only %s", d.Code, d.Message, EffectNotAllowedCode)
				}
			}
			checkedRootsPass(t, "genlib.nomi", genericLibrary(`["std::fs"]`, src))
		})
	}
}

// A public generic function whose body calls nothing with an effect is
// determined to have none, whatever its bound's shape, through a call of
// its bound's function that takes no value of the type (`T.from_json(j)`)
// and through another generic function it calls at its type parameter: it
// passes under an [effects.self] allowing nothing, with no
// effects-not-determined error.
func TestEffectRoots_APureGenericOfEveryShapeIsDetermined(t *testing.T) {
	src := shapesPrelude + `
pub fn tell<T>(x: T) -> String
where T: Marked and Describe
    Describe.describe(x)
end

pub fn pair<A, B>(a: A, b: B) -> (A, B)
where A: Marked, B: Holds<String>
    (a, b)
end

pub fn last<T>(xs: List<T>) -> Option<T>
    Iter.last(xs)
end

pub fn decode_as<T>(j: Json) -> Result<T, Json.ShapeError>
where T: FromJson
    T.from_json(j)
end

pub fn read_value<T>(v: Value) -> Result<T, String>
where T: FromSql
    decode(v)
end

fn decode<T>(v: Value) -> Result<T, String>
where T: FromSql
    T.from_sql(v)
end
`
	src = strings.Replace(src, "import std::fs\n", "import\n    std::json.{FromJson, Json}\n    std::sql.{FromSql, Value}\nend\n", 1)
	files := genericLibrary("[]", src)
	checkedRootsPass(t, "genlib.nomi", files)
}

// boxPrelude is a generic struct and a plain one for the impl block cases
// below.
const boxPrelude = shapesPrelude + `
pub struct Box<T>
    value: T
end

pub struct Tag
    name: String
end
`

// Every function of a generic impl block, and every generic function of an
// impl block, is walked through its abstract body whether or not anything
// instantiates it: an inherent block over a generic type, a generic
// function of that block, an interface's impl for the type whose block has
// a bound of its own, and a generic function of a plain type's block.
func TestEffectRoots_EveryImplFunctionIsWalkedThroughItsAbstractBody(t *testing.T) {
	cases := []struct {
		name, src, at, chain string
	}{
		{"a generic block's function", `
impl Box<T>
    pub fn load(b: Box<T>, path: String) -> T
        _ = fs.read_text(path)
        b.value
    end
end
`, "23:13", "reached from `Box<T>.load`"},
		{"a generic function of a generic block", `
impl Box<T>
    pub fn map<U>(b: Box<T>, f: (T) -> U, path: String) -> Box<U>
        _ = fs.read_text(path)
        Box{value: f(b.value)}
    end
end
`, "23:13", "reached from `Box<T>.map`"},
		{"an interface's impl whose block has a bound", `
impl Describe for Box<T> where T: Describe
    fn describe(b: Box<T>) -> String
        _ = fs.read_text("notes.txt")
        Describe.describe(b.value)
    end
end
`, "23:13", "reached from `Describe.describe for Box<T>`"},
		{"a generic function of a plain type's block", `
impl Tag
    pub fn pair<U>(t: Tag, u: U, path: String) -> (Tag, U)
        _ = fs.read_text(path)
        (t, u)
    end
end
`, "23:13", "reached from `Tag.pair`"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := boxPrelude + c.src
			got, ds := effectDiagnostics(t, "genlib.nomi", genericLibrary("[]", src))
			want := "genlib.nomi:" + c.at + ": this package's own code has the effect std::fs, and [effects.self] allows it no effects\n" +
				"genlib.nomi:" + c.at + ": help: " + c.chain + "\n" +
				"genlib.nomi:" + c.at + ": help: add \"std::fs\" to std under [effects.self] in nomi.toml, or remove the call"
			if got != want {
				t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
			}
			for _, d := range ds {
				if d.Code != EffectNotAllowedCode {
					t.Errorf("a %s diagnostic: %s; want only %s", d.Code, d.Message, EffectNotAllowedCode)
				}
			}
			checkedRootsPass(t, "genlib.nomi", genericLibrary(`["std::fs"]`, src))
		})
	}
}
