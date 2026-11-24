package analysis

import (
	"testing"
)

const ctorTypeArgDecls = `type UserId Int

struct Point
    x: Int
end

type Tagged<T> (String, T)

struct Box<T>
    v: T
end

struct Pair<A, B = String>
    a: A
    b: B
end

enum Holder<T>
    Of T
    Two (T, T)
    Rec {value: T}
end

enum Res<T, E>
    Good T
    Bad E
end

fn run()
`

// TestCallTypeArgs_EveryConstructorChecksItsTypeArguments: a construction's
// type arguments are its type's own parameters, bound by the function every
// call's are (bindTypeArgs), so each form counts them and resolves each:
// a distinct type's call form, a struct's call form, a positional variant,
// a struct-shaped variant's record form, and each as a pipe stage. A type
// with no parameters takes none, a variant's are its enum's in declared
// order (`Res.Bad<Int, String>`, whose signature names E alone), and a
// struct's parameter with a default may be left out. Each count error says
// what the type takes, each name that is no type is the unknown type error,
// each once, and an argument the instance does not take is that argument's
// error. A distinct's and a struct's call form ignored every type argument,
// so `UserId<Nope>(5)` and `Box<String>({v: 1})` were admitted, and a
// variant's were bound in the order its payload names them.
func TestCallTypeArgs_EveryConstructorChecksItsTypeArguments(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		count      string // the count error, "" for none
		unknown    bool   // whether `Nope` is the unknown type error
		mismatch   string // the argument's error, "" for none
	}{
		{"distinct, no parameters", "_ = UserId<Int>(5)", "expected 0 type argument(s), got 1", false, ""},
		{"distinct, no type", "_ = UserId<Nope>(5)", "expected 0 type argument(s), got 1", true, ""},
		{"struct call form, no parameters", "_ = Point<Nope>({x: 1})", "expected 0 type argument(s), got 1", true, ""},
		{"generic distinct, the argument", `_ = Tagged<String>(("n", 5))`, "", false, "cannot unify String with Int"},
		{"generic distinct, too many", `_ = Tagged<String, Int>(("n", "x"))`, "expected 1 type argument(s), got 2", false, ""},
		{"generic struct, the argument", "_ = Box<String>({v: 1})", "", false, "field 'v' of Box: expected String, got Int"},
		{"defaulted parameter, too many", `_ = Pair<Int, String, Int>({a: 1, b: "x"})`, "expected 1 to 2 type argument(s), got 3", false, ""},
		{"defaulted parameter, its default", "_ = Pair<Int>({a: 1, b: 2})", "", false, "field 'b' of Pair: expected String, got Int"},
		{"variant, the enum's order", "_ = Res.Bad<Int, String>(1)", "", false, "argument 1: expected String, got Int"},
		{"variant, too many", "_ = Holder.Of<Int, Int>(1)", "expected 1 type argument(s), got 2", false, ""},
		{"struct-shaped variant, the argument", "_ = Holder.Rec<String>({value: 1})", "", false, "field 'value' type mismatch: expected String, got Int"},
		{"struct-shaped variant, no type", "_ = Holder.Rec<Nope>({value: 1})", "", true, ""},
		{"distinct stage, no type", "_ = 5 |> UserId<Nope>()", "expected 0 type argument(s), got 1", true, ""},
		{"distinct stage, the argument", `_ = ("n", 5) |> Tagged<String>()`, "", false, "Tagged wraps (String, String), so Tagged(...) takes a (String, String); got (String, Int)"},
		{"struct stage, no parameters", "_ = {x: 1} |> Point<Int>()", "expected 0 type argument(s), got 1", false, ""},
		{"struct stage, the argument", "_ = {v: 1} |> Box<String>()", "", false, "field 'v' of Box: expected String, got Int"},
		{"struct-shaped variant stage, the argument", "_ = {value: 1} |> Holder.Rec<String>()", "", false, "field 'value' of Holder.Rec: expected String, got Int"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := ctorTypeArgDecls + "    " + tc.body + "\nend\n"
			_, errs := checkSource(src)
			if len(errs) == 0 {
				t.Fatalf("the checker admits this source:\n%s", src)
			}
			counts, unknowns, mismatches := 0, 0, 0
			for _, e := range errs {
				switch {
				case tc.count != "" && e.Message == tc.count:
					counts++
				case e.Code == UnknownTypeCode && len(e.Args) > 0 && e.Args[0] == "Nope":
					unknowns++
				case tc.mismatch != "" && e.Message == tc.mismatch:
					mismatches++
				default:
					t.Errorf("an error the construction should not draw: %v", e)
				}
			}
			if want := btoi(tc.count != ""); counts != want {
				t.Errorf("%q reported %d times, want %d: %v", tc.count, counts, want, errs)
			}
			if want := btoi(tc.unknown); unknowns != want {
				t.Errorf("unknown type reported %d times, want %d: %v", unknowns, want, errs)
			}
			if want := btoi(tc.mismatch != ""); mismatches != want {
				t.Errorf("%q reported %d times, want %d: %v", tc.mismatch, mismatches, want, errs)
			}
		})
	}
}

// TestCallTypeArgs_AConstructorsTypeArgumentsNameItsType: a construction
// whose argument does not say its type's parameters takes them from its
// type arguments, so the binding is determined: `Tagged<String>(...)` of a
// tuple whose second element is a String, `Ids<Int>([])`, and each form
// through a pipe stage. `Ids<Int>([])` was "not locally determined", as its
// type argument was ignored.
func TestCallTypeArgs_AConstructorsTypeArgumentsNameItsType(t *testing.T) {
	src := `type Ids<T> List<T>

struct Box<T>
    v: List<T>
end

enum Holder<T>
    Rec {value: List<T>}
end

fn run() -> (Ids<Int>, Ids<String>, Box<Int>, Holder<Int>)
    ids = Ids<Int>([])
    piped = [] |> Ids<String>()
    b = Box<Int>({v: []})
    h = Holder.Rec<Int>({value: []})
    (ids, piped, b, h)
end
`
	fa, errs := checkSource(src)
	if len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	got := map[string]string{}
	for pos, sym := range fa.Definitions {
		if sym != nil && sym.Kind == SymbolBinding && pos.Line >= 12 && pos.Line <= 15 {
			got[sym.Name] = forName(sym.Type)
		}
	}
	want := map[string]string{"ids": "Ids<Int>", "piped": "Ids<String>", "b": "Box<Int>", "h": "Holder<Int>"}
	for name, ty := range want {
		if got[name] != ty {
			t.Errorf("%s is a %s, want %s (all: %v)", name, got[name], ty, got)
		}
	}
}
