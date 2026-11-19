package irbuild

import (
	"os"
	"path/filepath"
	"testing"
)

// An operator over the `self` of an interface default runs at each
// implementor's instance of the default: the checker records it per
// instance, as one over a type parameter, and the builder lowers the record
// the implementor answers. Card's `>` and `!=` go through its impls; Pair's
// `!=` compares by shape, since Pair has no Equatable impl. The golden set
// holds the program, so the operator verifier sees a default's operators.
func TestDefaultSelfOperator_RunsAtEachImplementor(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	src := `import std::io

interface Ranked
    fn rank(value: self) -> Int

    fn beats(a: self, b: self) -> Bool where self: Comparable and Equatable
        a > b and a != b
    end
end

struct Card
    n: Int
end

derive Equatable for Card

impl Comparable for Card
    fn compare(a: Card, b: Card) -> Ordering
        Int.compare(a.n, b.n)
    end
end

impl Ranked for Card
    fn rank(c: Card) -> Int
        c.n
    end
end

struct Pair
    a: Int
    b: Int
end

impl Comparable for Pair
    fn compare(x: Pair, y: Pair) -> Ordering
        Int.compare(x.a + x.b, y.a + y.b)
    end
end

impl Ranked for Pair
    fn rank(p: Pair) -> Int
        p.a + p.b
    end
end

fn main()
    io.print("${Ranked.beats(Card{n: 2}, Card{n: 1})} ${Card.beats(Card{n: 1}, Card{n: 1})}")
    io.print("${Pair.beats(Pair{a: 1, b: 3}, Pair{a: 2, b: 1})} ${Ranked.beats(Pair{a: 1, b: 1}, Pair{a: 2, b: 2})}")
end
`
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Analyze(path); err != nil {
		t.Fatalf("the front end rejects this, so nothing below is tested: %v", err)
	}
	const want = "True False\nTrue False\n"
	got := vmReference(path)
	if got.stdout+got.stderr != want || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s", got.exit, got.stdout, got.stderr, want)
	}
	if golden := goldenReference(t, path); golden.stdout+golden.stderr != want || golden.exit != 0 {
		t.Errorf("golden record (exit %d):\n%s%s", golden.exit, golden.stdout, golden.stderr)
	}
}
