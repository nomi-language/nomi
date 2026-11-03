package irbuild

import (
	"os"
	"path/filepath"
	"testing"
)

// `==` and `!=` between an interface value and a concrete value run, on
// either side: the concrete value is boxed as the interface value and the
// two compare by their run-time types (spec §12 *Interface values*), as two
// interface values do. Covers a tuple, an anonymous struct, a struct, an Int
// and a String held as Equatable and as Hashable, a value held as a
// program's interface and as Display, an impure concrete operand, and
// values of different concrete types. The comparison declined unless both
// operands were interface values.
func TestInterfaceValueEqualsConcrete_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	src := `interface Shape {
    fn area(s: self) -> Int
}

struct Point {
    x: Int
    y: Int
}

derive Equatable for Point
derive Hashable for Point

impl Shape for Point {
    fn area(p: Point) -> Int {
        p.x * p.y
    }
}

fn two() -> Int {
    2
}

fn main() {
    t: Equatable = #(1, "a")
    dbg t == #(1, "a")
    dbg #(1, "a") == t
    dbg t != #(1, "b")
    dbg t == #(1, "b")
    a: Equatable = #{name: "n", n: 2}
    dbg a == #{name: "n", n: 2}
    dbg #{name: "n", n: 3} != a
    p: Equatable = Point{x: 1, y: 2}
    dbg p == Point{x: 1, y: two()}
    dbg Point{x: 2, y: 2} == p
    i: Equatable = 3
    dbg i == 3
    dbg 4 == i
    dbg i != 3
    s: Equatable = "s"
    dbg s == "s"
    dbg "t" == s
    dbg s == 3
    h: Hashable = #(1, "a")
    dbg h == #(1, "a")
    dbg 5 == h
    hp: Hashable = Point{x: 1, y: 2}
    dbg hp == Point{x: 1, y: 2}
    hs: Hashable = "x"
    dbg "x" == hs
    hi: Hashable = 7
    dbg hi != 7
    sh: Shape = Point{x: 2, y: 2}
    dbg sh == Point{x: two(), y: 2}
    d: Display = 4
    dbg d == 4
}
`
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Analyze(path); err != nil {
		t.Fatalf("the front end rejects this, so nothing below is tested: %v", err)
	}
	const want = `dbg line 25: t == #(1, "a") = True
dbg line 26: #(1, "a") == t = True
dbg line 27: t != #(1, "b") = True
dbg line 28: t == #(1, "b") = False
dbg line 30: a == #{name: "n", n: 2} = True
dbg line 31: #{name: "n", n: 3} != a = True
dbg line 33: p == Point{x: 1, y: two()} = True
dbg line 34: Point{x: 2, y: 2} == p = False
dbg line 36: i == 3 = True
dbg line 37: 4 == i = False
dbg line 38: i != 3 = False
dbg line 40: s == "s" = True
dbg line 41: "t" == s = False
dbg line 42: s == 3 = False
dbg line 44: h == #(1, "a") = True
dbg line 45: 5 == h = False
dbg line 47: hp == Point{x: 1, y: 2} = True
dbg line 49: "x" == hs = True
dbg line 51: hi != 7 = False
dbg line 53: sh == Point{x: two(), y: 2} = True
dbg line 55: d == 4 = True
`
	got := vmReference(path)
	if got.stdout+got.stderr != want || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s", got.exit, got.stdout, got.stderr, want)
	}
}
