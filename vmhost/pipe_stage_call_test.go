package vmhost_test

import (
	"testing"
)

// A pipe stage is the call it means, `x |> f(a)` is `f(x, a)` (spec §8
// *Pipes*), so it is checked by the direct call's code with the piped value
// as its first argument. The stage had a copy of that code that skipped what
// the direct call checks about a call through an interface and about its
// instantiation, so each of these was admitted: a polymorphic recursion
// through a stage, refused where the program was built; a tuple piped into
// `ToJson.to_json`, which no impl can take, an internal error where it was
// built; and `Parse.parse<Port>` for a subtype, which ran and made a
// Port its rule had not checked. Each is now the error the direct call
// gives.
func TestPipeStage_IsCheckedAsTheCallItMeans(t *testing.T) {
	rejects(t, `struct Box<T>
    inner: T
end

fn grow<T>(x: T, n: Int) -> Int
    if n == 0 then
        0
    else
        Box{inner: x} |> grow(n - 1)
    end
end

fn main()
    dbg grow(1, 3)
end
`, "main.nomi:9:26: `grow` instantiates `grow` at `T = Box<T>`, a type that grows on every recursive call")

	rejects(t, `import std::json.ToJson

fn main()
    dbg (1, 2) |> ToJson.to_json()
end
`, "main.nomi:4:33: (Int, Int) does not implement ToJson (required by `ToJson.to_json`)")

	rejects(t, `import std::parse.Parse

subtype Port Int when fn v -> 1 <= v and v <= 65535

fn main()
    dbg "80" |> Parse.parse<Port>()
end
`, "main.nomi:6:34: Port has no impl of Parse (required by `Parse.parse`): Port is a subtype of Int")

	got := runSourceOutput(t, `import std::parse.Parse

fn count<T>(xs: List<T>, n: Int) -> Int
    case xs
        [] -> n
        [_, ..rest] -> rest |> count(n + 1)
    end
end

fn main()
    dbg count(["a", "b", "c"], 0)
    dbg "80" |> Parse.parse<Int>()
end
`)
	if want := "dbg line 11: count([\"a\", \"b\", \"c\"], 0) = 3\ndbg line 12: \"80\" |> Parse.parse<Int>() = Ok(80)\n"; got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
