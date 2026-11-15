package vmhost

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What a `type` gets, `try_from`, `unchecked` and its TryFrom impl, is
// decided by what its inner type's name resolves to (spec §4 *Validated
// types*), so a subtype another file declares, or names through a
// typealias, is one; and a generic validated type implements TryFrom at
// its parameters.

// runProject writes files into a directory, loads its main.nomi and
// answers what the program prints.
func runProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Load(filepath.Join(dir, "main.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

const kindsSrc = `import std::duration.{PositiveDuration}

pub subtype Small Int when fn n -> n < 10

pub typealias Tiny Small

pub typealias Wait PositiveDuration

pub type Plain Int
`

func TestTryFrom_ATypeOverAnotherFilesSubtypeImplementsIt(t *testing.T) {
	got := runProject(t, map[string]string{"kinds.nomi": kindsSrc, "main.nomi": `import std::io
import kinds.{Small, Tiny, Plain}

type Code Small

type Bit Tiny

type Wrapper Plain

impl TryFrom<Plain> for Wrapper
    fn try_from(value: Plain) -> Maybe<Wrapper>
        Some(Wrapper(value))
    end
end

fn load<T>(n: Int) -> Maybe<T> where T: TryFrom<Int>
    T.try_from(n)
end

fn main()
    c: Maybe<Code> = load(4)
    io.inspect(c)
    b: Maybe<Bit> = load(40)
    io.inspect(b)
    io.inspect(Bit.try_from(3))
    io.inspect(Wrapper.try_from(Plain(1)))
end
`})
	if want := "Some(Code(4))\nNone\nSome(Bit(3))\nSome(Wrapper(Plain(1)))\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

// Only the alias is imported: the parent check reaches the subtype the
// alias names by its declaration, or by its std module, which this file
// has no name for.
func TestTryFrom_ATypeOverAnImportedAliasOfASubtype(t *testing.T) {
	got := runProject(t, map[string]string{"kinds.nomi": kindsSrc, "main.nomi": `import std::io
import std::duration.{Duration}
import kinds.{Tiny, Wait}

type Bit Tiny

subtype Mid Tiny when fn n -> n > 2

type Delay Wait

fn main()
    io.inspect(Bit.try_from(3))
    io.inspect(Bit.try_from(30))
    io.inspect(Mid.try_from(1))
    io.inspect(Mid.try_from(5))
    io.inspect(Delay.try_from(Duration.seconds(2)))
    io.inspect(Delay.try_from(Duration.seconds(0)))
end
`})
	if want := "Some(Bit(3))\nNone\nNone\nSome(5)\nSome(Delay(2s))\nNone\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestTryFrom_GenericValidatedTypesImplementIt(t *testing.T) {
	p, err := LoadSource("main", `import std::io

type Short<T> List<T> when fn xs -> List.length(xs) < 3

type Some2<T> NonEmptyList<T>

fn load<T>(xs: List<Int>) -> Maybe<T> where T: TryFrom<List<Int>>
    T.try_from(xs)
end

fn main()
    io.inspect(Short.try_from([1, 2]))
    io.inspect(Short.try_from([1, 2, 3]))
    s: Maybe<Short<Int>> = load([4])
    io.inspect(s)
    io.inspect(Some2.try_from([1]))
    io.inspect(Some2.try_from([]))
    io.inspect(Short([5]))
end
`)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	if want := "Some(Short([1, 2]))\nNone\nSome(Short([4]))\nSome(Some2([1]))\nNone\nShort([5])\n"; out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
}

// A rule that reads a name `value` keeps it: the generated parameter is
// the interface's `value`, binding the argument under another name.
func TestTryFrom_ARuleThatReadsValue(t *testing.T) {
	p, err := LoadSource("main", `import std::io

once value = 5

type Under Int when fn n -> n < value

fn main()
    io.inspect(Under.try_from(4))
    io.inspect(Under.try_from(value: 9))
end
`)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	if want := "Some(Under(4))\nNone\n"; out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
}

// An imported generic alias's target names what it names where the alias
// is declared: the file using it declares a `Point` of its own.
func TestGenericAlias_TargetIsReadInTheDeclaringFile(t *testing.T) {
	got := runProject(t, map[string]string{
		"shapes.nomi": `pub struct Point
    x: Int
end

pub typealias Pts<T> Map<T, Point>
`,
		"main.nomi": `import std::io
import shapes.{Pts}
import shapes

struct Point
    name: String
end

fn main()
    ps: Pts<String> = {"a" => shapes.Point{x: 1}}
    io.inspect(ps)
    io.inspect(Point{name: "b"})
end
`})
	if want := "{\"a\" => Point{x: 1}}\nPoint{name: \"b\"}\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}
