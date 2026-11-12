package vmhost_test

import "testing"

// A derived Equatable compares a Range field through `Equatable.equal`, and
// a derived Hashable hashes it through `Hashable.hash`, neither of which
// std's Range declares an impl of: they are Range's structural equality,
// as `==` on two Ranges is, and the hash that agrees with it.
func TestRun_DerivedEqualityOverARangeField(t *testing.T) {
	out := runOutput(t, `import std::io

struct Drv
    v: Range<Int>
end

derive Equatable for Drv
derive Hashable for Drv

fn main()
    d = Drv{v: 1..4}
    io.print(Debug.inspect(d == d))
    io.print(Debug.inspect(d == Drv{v: 1..5}))
    io.print(Debug.inspect(Equatable.equal(1..4, 2..4)))
    io.print(Debug.inspect(Hashable.hash(d) == Hashable.hash(Drv{v: 1..4})))
end
`)
	if want := "True\nFalse\nFalse\nTrue\n"; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

// A derived ToJson and FromJson over a Map keyed by an alias of String: the
// derive's check sees the alias's target, and the round trip runs.
func TestRun_DerivedJsonOverAMapKeyedByAnAlias(t *testing.T) {
	out := runOutput(t, `import std::io
import std::json.{FromJson, ToJson}

typealias Key String

struct Bag
    m: Map<Key, Int>
end

derive Equatable for Bag
derive ToJson for Bag
derive FromJson for Bag

fn main()
    b = Bag{m: {"a" => 1}}
    io.print(Debug.inspect(FromJson.from_json<Bag>(ToJson.to_json(b)) == Ok(b)))
end
`)
	if want := "True\n"; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

// A field, and a variant's payload, typed by an alias of a Map type: the
// derived FromJson decodes it through the alias as a type argument
// (`FromJson.from_json<Counts>`), since the checker refuses an alias as a
// call's owner, and ToJson encodes it.
func TestRun_DerivedJsonOverAFieldOfAMapAlias(t *testing.T) {
	out := runOutput(t, `import std::io
import std::json.{FromJson, Json, ToJson}

typealias Key String
typealias Counts Map<Key, Int>

struct Tally
    counts: Counts
end

enum Event
    Counted(Counts)
    Cleared
end

derive Equatable for Tally
derive ToJson for Tally
derive FromJson for Tally
derive Equatable for Event
derive ToJson for Event
derive FromJson for Event

fn main()
    t = Tally{counts: {"a" => 1, "b" => 2}}
    io.print(Json.to_string(ToJson.to_json(t)))
    io.print(Debug.inspect(FromJson.from_json<Tally>(ToJson.to_json(t)) == Ok(t)))
    e = Event.Counted({"c" => 3})
    io.print(Debug.inspect(FromJson.from_json<Event>(ToJson.to_json(e)) == Ok(e)))
    io.print(Debug.inspect(Result.is_err(FromJson.from_json<Tally>(Json.Obj({})))))
end
`)
	if want := "{\"counts\":{\"a\":1,\"b\":2}}\nTrue\nTrue\nTrue\n"; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

// The same over an alias another file declares, imported by name
// (`counts.{Counts}`) and named through its file (`counts.Counts`): the
// derives resolve the alias by the symbol the file resolved the name to,
// and read its target in the declaring file, whose key is an alias this
// file does not import.
func TestRun_DerivedJsonOverAFieldOfAnImportedMapAlias(t *testing.T) {
	out := runSiblingProgram(t, map[string]string{
		"counts.nomi": `pub typealias Key String

pub typealias Counts Map<Key, Int>
`,
		"main.nomi": `import
    std::io
    std::json.{FromJson, Json, ToJson}
    counts
    counts.{Counts}
end

struct Tally
    picked: Counts
    whole: counts.Counts
end

enum Event
    Picked(Counts)
    Whole(counts.Counts)
    Cleared
end

derive Equatable for Tally
derive ToJson for Tally
derive FromJson for Tally
derive Equatable for Event
derive ToJson for Event
derive FromJson for Event

fn main()
    t = Tally{picked: {"a" => 1}, whole: {"b" => 2}}
    io.print(Json.to_string(ToJson.to_json(t)))
    io.print(Debug.inspect(FromJson.from_json<Tally>(ToJson.to_json(t)) == Ok(t)))
    p = Event.Picked({"c" => 3})
    io.print(Debug.inspect(FromJson.from_json<Event>(ToJson.to_json(p)) == Ok(p)))
    w = Event.Whole({"d" => 4})
    io.print(Debug.inspect(FromJson.from_json<Event>(ToJson.to_json(w)) == Ok(w)))
    io.print(Debug.inspect(Result.is_err(FromJson.from_json<Tally>(Json.Obj({"picked" => Json.Obj({})})))))
end
`,
	})
	if want := "{\"picked\":{\"a\":1},\"whole\":{\"b\":2}}\nTrue\nTrue\nTrue\nTrue\n"; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

// An owner function's generic argument written as a struct literal whose
// field is a value an enum embeds, where the call's result is expected as
// the enum: the checker instantiates the function at the enum, and the
// literal is built at that instance, its field widened into the enum.
func TestRun_OwnerGenericFunctionResultWidenedIntoItsEnum(t *testing.T) {
	out := runOutput(t, `import std::io

struct Spot
    n: Int
end

enum Shape
    embeds Spot
    Dot
end

struct Own<A>
    v: A
end

impl Own<A>
    pub fn get<A>(o: Own<A>) -> A
        o.v
    end
end

fn take(a: Shape) -> Shape
    a
end

fn main()
    io.print(Debug.inspect(take(Own.get(Own{v: Spot{n: 3}}))))
    s: Shape = Own.get(Own{v: Spot{n: 4}})
    io.print(Debug.inspect(s is Shape.Dot))
end
`)
	if want := "Spot{n: 3}\nFalse\n"; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}
