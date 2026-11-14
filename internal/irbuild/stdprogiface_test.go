package irbuild

import "testing"

// A std interface with no rt table, held as a value (stdprogiface.go):
// Hashable at an annotated binding, a list element, a struct field, a
// parameter and a return, over program structs and over Int, whose std impl
// is a retained Nomi body.
func TestStdProgIface_HashableHeldAsAValue(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import std::io

struct P
    n: Int
end

impl Hashable for P
    fn hash(value: P) -> Int
        value.n * 10
    end
end

struct Q
    s: String
end

impl Hashable for Q
    fn hash(value: Q) -> Int
        String.length(value.s)
    end
end

struct Holder
    h: Hashable
end

fn pick(flag: Bool) -> Hashable
    if flag then
        return P{n: 4}
    end
    Q{s: "abc"}
end

fn total(hs: List<Hashable>) -> Int
    hs |> Iter.map(&Hashable.hash(&1)) |> Iter.sum()
end

fn main()
    h: Hashable = P{n: 3}
    io.print(Int.to_string(Hashable.hash(h)))
    io.print(Int.to_string(total([P{n: 1}, Q{s: "xy"}])))
    holder = Holder{h: Q{s: "a"}}
    io.print(Int.to_string(Hashable.hash(holder.h)))
    io.print(Int.to_string(Hashable.hash(pick(True))))
    io.print(Int.to_string(Hashable.hash(pick(False))))
    s: Hashable = 7
    io.print(Int.to_string(Hashable.hash(s)))
    io.print(Int.to_string(total([P{n: 2}, 5])))
end
`, "30\n12\n1\n40\n3\n7\n25\n")
}

// A std GENERIC interface held at its instantiation: Contains<Int> over two
// program structs, dispatched by a qualified call and by the `in` operator
// whose lambda parameter's type the checker inferred, and Contains<String>
// over a String, whose std impl at that argument is chosen.
func TestStdProgIface_ContainsHeldAtAnInstantiation(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import std::io

struct Bag
    n: Int
end

impl Contains<Int> for Bag
    fn contains(c: Bag, x: Int) -> Bool
        x <= c.n
    end
end

struct Exactly
    v: Int
end

impl Contains<Int> for Exactly
    fn contains(c: Exactly, x: Int) -> Bool
        x == c.v
    end
end

fn holders(x: Int, cs: List<Contains<Int>>) -> Int
    cs |> Iter.filter(fn c -> x in c) |> Iter.count()
end

fn main()
    b: Contains<Int> = Bag{n: 3}
    io.print(Bool.to_string(2 in b))
    io.print(Bool.to_string(Contains.contains(b, 5)))
    io.print(Int.to_string(holders(3, [Bag{n: 5}, Exactly{v: 3}, Exactly{v: 4}])))
    t: Contains<String> = "hello"
    io.print(Bool.to_string("ell" in t))
    io.print(Bool.to_string("z" in t))
end
`, "True\nFalse\n2\nTrue\nFalse\n")
}

// A String held as Matcher, whose std impl is a set of host functions,
// dispatched through the thunks the program builds for them.
func TestStdProgIface_HostImplsDispatchThroughThunks(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import std::io
import std::matcher.Matcher

fn main()
    m: Matcher = "a"
    io.print(Bool.to_string(Matcher.is_contained_in(m, "cat")))
    io.print(Bool.to_string(Matcher.is_prefix_of(m, "cat")))
end
`, "True\nFalse\n")
}

// A value boxed in one file dispatches to an impl recorded in another, and a
// sibling's function taking a std existential is called with this file's.
func TestStdProgIface_AcrossFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	typeCycleRun(t, map[string]string{
		"nomi.toml": "[module]\nname = \"held\"\nentry_points = [\"main\"]\n",
		"main.nomi": `import shapes.{Square, Bag, total, holds}
import std::io

struct Local
    v: Int
end

impl Hashable for Local
    fn hash(l: Local) -> Int
        l.v
    end
end

fn main()
    h: Hashable = Square{side: 2}
    io.print(Int.to_string(Hashable.hash(h)))
    io.print(Int.to_string(total([Square{side: 1}, Local{v: 5}, 3])))
    io.print(Bool.to_string(holds(Bag{n: 4}, 2)))
    c: Contains<Int> = Bag{n: 1}
    io.print(Bool.to_string(2 in c))
end
`,
		"shapes.nomi": `pub struct Square
    side: Int
end

impl Hashable for Square
    fn hash(s: Square) -> Int
        s.side * 100
    end
end

pub struct Bag
    n: Int
end

impl Contains<Int> for Bag
    fn contains(c: Bag, x: Int) -> Bool
        x <= c.n
    end
end

pub fn total(hs: List<Hashable>) -> Int
    hs |> Iter.map(&Hashable.hash(&1)) |> Iter.sum()
end

pub fn holds(c: Contains<Int>, x: Int) -> Bool
    x in c
end
`,
	}, "200\n108\nTrue\nFalse\n")
}

// Every Matcher function called on a value held as Matcher, including those
// whose result is no scalar (`List<String>`, `Maybe<String>`), over a String
// and over a program struct. The signatures are std's declaration, resolved
// against std's own file (stdProgIfaceOf), so `Maybe<String>` is the prelude
// enum's instance.
func TestStdProgIface_EveryMatcherFunctionOnAnErasedReceiver(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import std::io
import std::matcher.Matcher

struct Letter
    c: String
end

impl Matcher for Letter
    fn is_contained_in(m: Letter, text: String) -> Bool
        String.contains(text, m.c)
    end

    fn is_prefix_of(m: Letter, text: String) -> Bool
        String.starts_with(text, m.c)
    end

    fn is_suffix_of(m: Letter, text: String) -> Bool
        String.ends_with(text, m.c)
    end

    fn split_in(m: Letter, text: String) -> List<String>
        String.split(text, m.c)
    end

    fn replace_in(m: Letter, text: String, replacement: String) -> String
        String.replace(text, m.c, replacement)
    end

    fn find_all_in(m: Letter, text: String) -> List<String>
        String.split(text, m.c) |> Iter.drop(1) |> Iter.map(fn _ -> m.c) |> Iter.to_list()
    end

    fn find_in(m: Letter, text: String) -> Maybe<String>
        if String.contains(text, m.c) then Some(m.c) else None
    end
end

fn report(m: Matcher)
    io.print(Bool.to_string(Matcher.is_contained_in(m, "banana")))
    io.print(Bool.to_string(Matcher.is_suffix_of(m, "banana")))
    io.print(String.join(Matcher.split_in(m, "banana"), "|"))
    io.print(Matcher.replace_in(m, "banana", "o"))
    io.print(Int.to_string(Iter.count(Matcher.find_all_in(m, "banana"))))
    io.print(Matcher.find_in(m, "banana") |> Maybe.with_default("none"))
    io.print(Matcher.find_in(m, "xyz") |> Maybe.with_default("none"))
end

fn main()
    report("a")
    report(Letter{c: "n"})
end
`, "True\nTrue\nb|n|n|\nbonono\n3\na\nnone\nTrue\nFalse\nba|a|a\nbaoaoa\n2\nn\nnone\n")
}

// A value of a std type other than a builtin scalar held as a std interface
// it implements: containers (List, Set, Map over its keys, Range), whose
// runtime value names none of its type arguments, and a Regex as Matcher,
// whose std impl is a set of host functions. Each enters boxed under its
// type's name, and the box's dispatch entries are functions the program
// builds that make the call a direct `Contains.contains(xs, 2)` makes
// (stdProgThunk). Mixed with program types in one list.
func TestStdProgIface_StdTypesHeldAsStdInterfaces(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import std::io
import std::matcher.Matcher
import std::regex.Regex

struct Bag
    n: Int
end

impl Contains<Int> for Bag
    fn contains(c: Bag, x: Int) -> Bool
        x <= c.n
    end
end

fn holders(x: Int, cs: List<Contains<Int>>) -> Int
    cs |> Iter.filter(fn c -> x in c) |> Iter.count()
end

fn found(ms: List<Matcher>, text: String) -> List<String>
    ms |> Iter.map(&(Matcher.find_in(&1, text) |> Maybe.with_default("-"))) |> Iter.to_list()
end

fn main()
    c: Contains<Int> = [1, 2, 3]
    io.print(Bool.to_string(2 in c))
    io.print(Bool.to_string(Contains.contains(c, 9)))
    io.print(Int.to_string(holders(3, [[1, 2], 1..5, Bag{n: 3}, Set[3]])))
    keys: Contains<String> = {"a" => 1}
    io.print(Bool.to_string("a" in keys))
    re = Regex`+"`a+`"+`
    ms: List<Matcher> = ["n", re]
    io.print(String.join(found(ms, "banaana"), ","))
    m: Matcher = re
    io.print(String.join(Matcher.split_in(m, "baaxaay"), "|"))
    h: Hashable = [1, 2]
    same: Hashable = [1, 2]
    io.print(Bool.to_string(Hashable.hash(h) == Hashable.hash(same)))
end
`, "True\nFalse\n3\nTrue\nn,a\nb|x|y\nTrue\n")
}

// A std struct held as a std interface enters unboxed, its run-time value
// naming its type (Date as DateParts); a std leaf enters boxed (Bytes as
// Contains<Byte>); and a std interface whose signature names a std type this
// file does not import (ToJson's `Json`) resolves it in std's scope.
func TestStdProgIface_StdRecordsLeavesAndUnimportedSignatureTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import std::io
import std::calendar.{Date, DateParts}
import std::json.ToJson

struct P
    n: Int
end

derive ToJson for P

fn main()
    ds: List<DateParts> = [Date`+"`2020-01-02`"+`, Date`+"`2021-03-04`"+`]
    io.print(String.join(ds |> Iter.map(&Int.to_string(DateParts.month(&1))) |> Iter.to_list(), ","))
    b: Contains<Byte> = Bytes.from_list([1, 2, 3])
    x: Byte = 2
    io.print(Bool.to_string(x in b))
    js: List<ToJson> = [P{n: 1}, 5, "x", [True]]
    io.print(String.join(js |> Iter.map(fn j -> "${ToJson.to_json(j)}") |> Iter.to_list(), " "))
end
`, "1,3\nTrue\n{\"n\":1} 5 \"x\" [true]\n")
}

// A std container boxed as a std interface in one file is dispatched in
// another, which names neither the container nor the interface's module.
func TestStdProgIface_BoxedStdValueAcrossFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	typeCycleRun(t, map[string]string{
		"nomi.toml": "[module]\nname = \"held\"\nentry_points = [\"main\"]\n",
		"main.nomi": `import shapes.{holds, first_match}
import std::io
import std::matcher.Matcher
import std::regex.Regex

fn main()
    io.print(Bool.to_string(holds([1, 2, 3], 2)))
    io.print(Bool.to_string(holds(1..3, 7)))
    m: Matcher = Regex"o+"
    io.print(first_match(m, "foo"))
end
`,
		"shapes.nomi": `import std::matcher.Matcher

pub fn holds(c: Contains<Int>, x: Int) -> Bool
    x in c
end

pub fn first_match(m: Matcher, text: String) -> String
    Matcher.find_in(m, text) |> Maybe.with_default("-")
end
`,
	}, "True\nFalse\noo\n")
}

// A file that declares its own `Matcher` holds std values as
// `matcher.Matcher`: the box's dispatch entries reach std's impl by the
// interface's declaration, not by the bare name, which means the file's
// struct here, and a module-qualified call dispatches the erased receiver.
func TestStdProgIface_ShadowedBareNameThroughItsModule(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import std::io
import std::matcher
import std::regex.Regex

struct Matcher
    s: String
end

fn main()
    m: matcher.Matcher = Regex`+"`o+`"+`
    io.print(matcher.Matcher.find_in(m, "foo") |> Maybe.with_default("-"))
    n: matcher.Matcher = "o"
    io.print(Bool.to_string(matcher.Matcher.is_contained_in(n, "foo")))
    io.print(Matcher{s: "x"}.s)
end
`, "oo\nTrue\nx\n")
}

// A function called through a value held as a std interface answers what
// the same call on the concrete value answers: `Hashable.hash(h)` with
// `h: Hashable = v` is `Hashable.hash(v)`, for every std type that
// implements Hashable and for a program struct. Map keys and Set elements
// of interface type hash with vm.hash_value, which only has to agree with
// `==`; a call written `Hashable.hash` dispatches to the held type's impl.
// `==` over two Equatable values, Debug and Display are held to the same
// rule. (`Equatable.equal` itself takes two `self` operands, which the
// checker refuses on interface values.)
func TestStdProgIface_InterfaceCallsAnswerAsTheDirectCall(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import
    std::io
    std::calendar.Date
end

struct P
    n: Int
end

impl Hashable for P
    fn hash(value: P) -> Int
        value.n
    end
end

derive Equatable for P

derive Debug for P

impl Display for P
    fn to_string(value: P) -> String
        "P${value.n}"
    end
end

fn hashes(label: String, direct: Int, h: Hashable)
    io.print("${label} ${direct == Hashable.hash(h)}")
end

fn equals(label: String, direct: Bool, a: Equatable, b: Equatable)
    io.print("${label} ${direct == (a == b)}")
end

fn inspects(label: String, direct: String, d: Debug)
    io.print("${label} ${direct == Debug.inspect(d)}")
end

fn shows(label: String, direct: String, d: Display)
    io.print("${label} ${direct == Display.to_string(d)}")
end

fn main()
    hashes("Int", Hashable.hash(5), 5)
    hashes("Float", Hashable.hash(1.5), 1.5)
    hashes("String", Hashable.hash("ab"), "ab")
    hashes("Bool", Hashable.hash(True), True)
    hashes("List", Hashable.hash([1, 2]), [1, 2])
    hashes("Set", Hashable.hash(Set[1, 2]), Set[1, 2])
    hashes("Map", Hashable.hash({"a" => 1}), {"a" => 1})
    hashes("Bytes", Hashable.hash(Bytes.from_list([1, 2])), Bytes.from_list([1, 2]))
    hashes("Maybe", Hashable.hash(Some(3)), Some(3))
    case Date.new(2026, 5, 4)
        Ok(d) -> hashes("Date", Hashable.hash(d), d)
        Err(_) -> io.print("Date missing")
    end
    hashes("P", Hashable.hash(P{n: 7}), P{n: 7})
    equals("List", Equatable.equal([1, 2], [1, 2]), [1, 2], [1, 2])
    none: Maybe<Int> = None
    equals("Maybe", Equatable.equal(Some(1), none), Some(1), none)
    equals("P", Equatable.equal(P{n: 1}, P{n: 2}), P{n: 1}, P{n: 2})
    inspects("List", Debug.inspect([1, 2]), [1, 2])
    inspects("Map", Debug.inspect({"a" => 1}), {"a" => 1})
    inspects("P", Debug.inspect(P{n: 3}), P{n: 3})
    shows("Int", Display.to_string(4), 4)
    shows("P", Display.to_string(P{n: 3}), P{n: 3})
end
`, "Int True\nFloat True\nString True\nBool True\nList True\nSet True\nMap True\nBytes True\nMaybe True\nDate True\nP True\nList True\nMaybe True\nP True\nList True\nMap True\nP True\nInt True\nP True\n")
}

// A file that declares its own `Json` holds values as std's ToJson, whose
// `to_json` returns std's `Json`: the signature resolves in std's scope, so
// the result is std's enum, not the file's struct. At a binding, a
// parameter, a struct field and over a program impl. Beside it, the file's
// own interface whose function returns `Json` means the file's struct.
func TestStdProgIface_SignatureNamesATypeTheFileAlsoDeclares(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import std::io
import std::json
import std::json.ToJson

struct Json
    raw: String
end

interface Encode
    fn encode(v: self) -> Json
end

struct Box
    v: ToJson
end

struct Point
    x: Int
end

impl ToJson for Point
    fn to_json(p: Point) -> json.Json
        ToJson.to_json([p.x, p.x])
    end
end

impl Encode for Point
    fn encode(p: Point) -> Json
        Json{raw: "p${p.x}"}
    end
end

fn encoded(v: ToJson) -> String
    "${ToJson.to_json(v)}"
end

fn raw(e: Encode) -> String
    Encode.encode(e).raw
end

fn main()
    x: ToJson = 5
    io.print("${ToJson.to_json(x)}")
    io.print(encoded("hi"))
    io.print(encoded(Point{x: 7}))
    b = Box{v: [1, 2]}
    io.print(encoded(b.v))
    io.print(raw(Point{x: 3}))
    io.print(Json{raw: "own"}.raw)
end
`, "5\n\"hi\"\n[7,7]\n[1,2]\np3\nown\n")
}

// The same across files: a sibling holds a value as ToJson in a struct
// field, and the file that reads it declares its own `Json`; and the other
// way, the sibling declares a `Json` and the entry holds the ToJson.
func TestStdProgIface_SignatureNameShadowedAcrossFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	typeCycleRun(t, map[string]string{
		"nomi.toml": "[module]\nname = \"held\"\nentry_points = [\"main\"]\n",
		"main.nomi": `import boxes
import std::io
import std::json.ToJson

struct Json
    raw: String
end

fn main()
    io.print("${ToJson.to_json(boxes.make().v)}")
    io.print(boxes.shown(boxes.make().v))
    k: ToJson = ["a"]
    io.print(boxes.shown(k))
    io.print(Json{raw: "own"}.raw)
end
`,
		"boxes.nomi": `import std::json.ToJson

pub struct Json
    n: Int
end

pub struct Box
    v: ToJson
end

pub fn make() -> Box
    Box{v: Json{n: 4}.n}
end

pub fn shown(v: ToJson) -> String
    "${ToJson.to_json(v)}"
end
`,
	}, "4\n4\n[\"a\"]\nown\n")
}

// A program's own typed-literal handler in a file that declares its own
// `Fragment`: the impl names std's as `literals.Fragment`, and std's
// generic Literal interface reads `Fragment<I>` by the position std wrote it
// at, so neither resolves to the file's struct.
func TestStdProgIface_LiteralHandlerBesideAFileFragment(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import std::io
import std::literals
import std::literals.Literal

struct Fragment
    x: Int
end

struct Joiner
    s: String
end

impl Literal<String, Joiner> for Joiner
    fn from_fragments(fragments: List<literals.Fragment<String>>) -> Joiner
        Joiner{s: "${Iter.count(fragments)}"}
    end
end

fn main()
    name = "a"
    io.print(Joiner"x ${name} y".s)
    io.print("${Fragment{x: 1}.x}")
end
`, "3\n1\n")
}
