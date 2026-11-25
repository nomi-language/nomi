package vmhost

import (
	"context"
	"strings"
	"testing"
)

// Validated types, `type`s over subtypes and subtypes with no rule of
// their own run (spec §4 *Subtypes*, *Validated types*).

const subtypeSrc = `import std::io

fn is_email(s: String) -> Bool
    String.contains(s, "@")
end

type Email String when is_email

type UserId PositiveInt

subtype Strict NonNegativeInt and NonZeroInt

fn user(e: Email) -> String
    Email(s) = e
    s
end

fn half(n: Strict) -> Int
    n / 2
end

fn third(n: NonNegativeInt and NonZeroInt) -> Int
    n / 3
end

`

func TestSubtype_ValidatedTypesRun(t *testing.T) {
	p, err := LoadSource("main", subtypeSrc+`fn main()
    io.inspect(Email("a@b.c"))
    io.inspect(user(Email("a@b.c")))
    io.inspect(Email.try_from("nope"))
    io.inspect(Email.try_from("x@y"))
    io.inspect(UserId(5))
    io.inspect(UserId.try_from(0))
    io.inspect(UserId.try_from(7))
    p: PositiveInt = 9
    io.inspect(UserId(p))
    io.inspect(half(p))
    io.inspect(half(8))
    io.inspect(Strict.try_from(0))
    io.inspect(third(p))
    io.inspect(third(6))
    xs: List<NonNegativeInt and NonZeroInt> = [1, 2]
    io.inspect(Iter.sum(xs))
end
`)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	const want = "Email(\"a@b.c\")\n\"a@b.c\"\nNone\nSome(Email(\"x@y\"))\nUserId(5)\nNone\nSome(UserId(7))\nUserId(9)\n4\n4\nNone\n3\n2\n3\n"
	if out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
}

func TestSubtype_ValidatedLiteralsAreCheckedWhenTheProgramIsCompiled(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"    io.inspect(Email(\"nope\"))\n",
			"\"nope\" is not an Email: `is_email` is false"},
		{"    io.inspect(UserId(0))\n",
			"0 is not a UserId: UserId wraps PositiveInt, which is a subtype of NonZeroInt, and NonZeroInt's rule `fn n -> n != 0` is false"},
		{"    io.inspect(third(-3))\n",
			"-3 is not a NonNegativeInt, as NonNegativeInt and NonZeroInt requires: `fn n -> n >= 0` is false"},
		{"    io.inspect(half(0))\n",
			"0 is not a Strict: Strict is a subtype of NonZeroInt, and NonZeroInt's rule `fn n -> n != 0` is false"},
	} {
		ds := literalDiagnostics(t, subtypeSrc+"fn main()\n"+tc.body+"end\n")
		if len(ds) != 1 || ds[0].Message != tc.want || ds[0].Code != InvalidSubtypeCode {
			t.Errorf("%s: diagnostics = %v, want one reading %q", strings.TrimSpace(tc.body), ds, tc.want)
		}
	}
}

func TestSubtype_TryFromRuns(t *testing.T) {
	p, err := LoadSource("main", `import std::io

type Percent Int when fn n -> n >= 0 and n <= 100

type UserId PositiveInt

type Celsius Float when fn c -> c >= -273.15

type Kelvin Float when fn k -> k >= 0.0

impl TryFrom<Celsius> for Kelvin
    fn try_from(Celsius(c)) -> Option<Kelvin>
        Kelvin.try_from(c + 273.15)
    end
end

enum Weekday
    Monday
    Tuesday
end

impl TryFrom<Int> for Weekday
    fn try_from(value: Int) -> Option<Weekday>
        case value
            1 -> Some(Weekday.Monday)
            2 -> Some(Weekday.Tuesday)
            _ -> None
        end
    end
end

fn load<T>(n: Int) -> Option<T> where T: TryFrom<Int>
    T.try_from(n)
end

fn main()
    p: Option<Percent> = load(7)
    io.inspect(p)
    q: Option<Percent> = load(107)
    io.inspect(q)
    u: Option<UserId> = load(0)
    io.inspect(u)
    w: Option<Weekday> = load(2)
    io.inspect(w)
    io.inspect(Kelvin.try_from(Celsius(20.0)))
    io.inspect(Kelvin.try_from(-1.0))
end
`)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	if want := "Some(Percent(7))\nNone\nNone\nSome(Tuesday)\nSome(Kelvin(293.15))\nNone\n"; out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
}

func TestSubtype_GenericAliasesRun(t *testing.T) {
	p, err := LoadSource("main", `import std::io

typealias Pair<T> (T, T)

typealias Sortable<T> List<T> where T: Comparable

typealias Both NonNegativeInt and NonZeroInt

fn swap(p: Pair<Int>) -> Pair<Int>
    (a, b) = p
    (b, a)
end

fn smallest(xs: Sortable<Int>) -> Option<Int>
    Iter.min(xs)
end

fn half(n: Both) -> Int
    n / 2
end

fn main()
    io.inspect(swap((1, 2)))
    io.inspect(smallest([3, 1, 2]))
    io.inspect(half(6))
end
`)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	if want := "(2, 1)\nSome(1)\n3\n"; out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
}
