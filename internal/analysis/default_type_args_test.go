package analysis_test

import (
	"strings"
	"testing"
)

// A type parameter may have a default, which a use in a type position that
// leaves the argument out takes (default_type_args.go).

const defaultArgsLib = `pub struct Tagged<T, Tag = String>
    value: T
    tag: Tag
end

pub enum Outcome<E = String>
    Done
    Failed(E)
end

pub type Id<K = Int> K
`

// Another file's uses take the defaults, whichever file is checked first.
func TestDefaultTypeArgs_UsesFromAnotherFileTakeTheDefault(t *testing.T) {
	src := `import lib.{Id, Outcome, Tagged}

fn tag(t: Tagged<Int>) -> String
    t.tag
end

fn reason(o: Outcome) -> String
    case o
        .Done -> ""
        .Failed(e) -> e
    end
end

fn raw(id: Id) -> Int
    Id(n) = id
    n
end

fn main()
    _ = (tag(Tagged{value: 1, tag: "a"}), reason(.Failed("x")), raw(Id(3)))
end
`
	files := map[string]string{"main.nomi": src, "lib.nomi": defaultArgsLib}
	for _, order := range [][]string{{"", "lib"}, {"lib", ""}} {
		got := checkOnceProject(t, files, order)
		for file, errs := range got {
			if len(errs) > 0 {
				t.Errorf("order %v: %s: unexpected errors:\n%s", order, file, strings.Join(messages(errs), "\n"))
			}
		}
	}
}

// A bare name in a type position is the default, never a type to infer,
// and a type prints with its trailing defaults left out.
func TestDefaultTypeArgs_BareNameIsTheDefault(t *testing.T) {
	src := `import lib.Tagged

fn tag(t: Tagged<Int>) -> String
    t.tag
end

fn main()
    _ = tag(Tagged{value: 1, tag: 2})
end
`
	got := checkOnceProject(t, map[string]string{"main.nomi": src, "lib.nomi": defaultArgsLib}, []string{"", "lib"})
	msgs := strings.Join(messages(got["main.nomi"]), "\n")
	if !strings.Contains(msgs, "expected Tagged<Int>, got Tagged<Int, Int>") {
		t.Fatalf("want the mismatch to print Tagged<Int>, got:\n%s", msgs)
	}
}

// Each misuse of a default is refused with its message.
func TestDefaultTypeArgs_MisusesAreRefused(t *testing.T) {
	cases := []struct{ src, want string }{
		{"struct Bad<A, B = List<A>>\n    a: A\n    b: B\nend\n", "a type parameter's default can't name a type parameter (`A`)"},
		{"fn f<T = Int>(x: T) -> T\n    x\nend\n", "a function's type parameter can't have a default; a call infers `T`"},
		{"struct Pair<A, B = String>\n    a: A\n    b: B\nend\n\nfn g(p: Pair) -> Pair\n    p\nend\n", "`Pair` expects 1 to 2 type arguments, got 0"},
		{"struct Pair<A, B = String>\n    a: A\n    b: B\nend\n\nfn g(p: Pair<Int, Int, Int>) -> Pair<Int>\n    todo\nend\n", "`Pair` expects 1 to 2 type arguments, got 3"},
	}
	for _, c := range cases {
		got := checkOnceProject(t, map[string]string{"main.nomi": c.src + "\nfn main() end\n"}, []string{""})
		msgs := messages(got["main.nomi"])
		found := false
		for _, m := range msgs {
			if m == c.want {
				found = true
			}
		}
		if !found {
			t.Errorf("want %q, got:\n%s", c.want, strings.Join(msgs, "\n"))
		}
	}
}
