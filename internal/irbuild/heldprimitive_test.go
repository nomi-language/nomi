package irbuild

import (
	"os"
	"path/filepath"
	"testing"
)

// A value of every primitive type runs held as each std interface it
// implements: Comparable, Equatable, Hashable and their conjunctions, read
// back through a call that works on an interface value (spec §12 *Interface
// values*): `Debug.inspect`, `==` for Equatable, a Map key for Hashable.
// Byte is not Comparable, so it is held as the other two. A value held as
// Comparable declined: compare takes two `self`, which no erased receiver
// dispatches (gapMultiSelf), and the shared Comparable def asked a held
// scalar for a dispatch entry of it anyway, and a Decimal, Date or Duration
// for one only a scalar can have.
func TestHeldPrimitive_RunsAsEachStdInterface(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	src := `import
    std::calendar.Date
    std::duration.Duration
end

fn held(x: Comparable) -> String
    Debug.inspect(x)
end

fn ordered(x: Comparable and Hashable) -> String
    Debug.inspect(x)
end

fn shown(x: Display and Comparable) -> String
    Debug.inspect(x)
end

fn same(a: Equatable, b: Equatable) -> Bool
    a == b
end

fn keys(xs: List<Hashable>) -> Int
    xs |> Iter.map(fn k -> (k, 1)) |> Iter.to_map() |> Map.size()
end

fn both(xs: List<Equatable and Hashable>) -> Int
    xs |> Iter.map(fn k -> (k, 1)) |> Iter.to_map() |> Map.size()
end

fn main()
    b: Byte = 7
    dbg held(1)
    dbg held(1.5)
    dbg held(2.5d)
    dbg held("a")
    dbg held(True)
    dbg held(Date"2026-01-01")
    dbg held(Duration.seconds(1))
    dbg ordered(1)
    dbg ordered(1.5)
    dbg ordered(2.5d)
    dbg ordered("a")
    dbg ordered(False)
    dbg shown(3)
    dbg shown(2.5d)
    dbg shown("a")
    dbg same(1, 1)
    dbg same(1.5, 2.5)
    dbg same(2.5d, 2.50d)
    dbg same("a", "a")
    dbg same(True, False)
    dbg same(b, b)
    dbg keys([1, 1, 2])
    dbg keys([1.5, 2.5])
    dbg keys([1.5d, 1.50d])
    dbg keys(["a", "b", "a"])
    dbg keys([True, True])
    dbg keys([b, b])
    dbg both([1, 2])
    dbg both([1.5d, 1.5d])
    dbg both([b, b])
    i: Comparable = 1
    s: Display and Comparable = "a"
    e: Equatable and Hashable = 2.5d
    dbg Debug.inspect(i)
    dbg Debug.inspect(s)
    dbg e == 2.5d
end
`
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Analyze(path); err != nil {
		t.Fatalf("the front end rejects this, so nothing below is tested: %v", err)
	}
	const want = `dbg line 32: held(1) = "1"
dbg line 33: held(1.5) = "1.5"
dbg line 34: held(2.5d) = "2.5d"
dbg line 35: held("a") = "\"a\""
dbg line 36: held(True) = "True"
dbg line 37: held(Date"2026-01-01") = "2026-01-01"
dbg line 38: held(Duration.seconds(1)) = "1s"
dbg line 39: ordered(1) = "1"
dbg line 40: ordered(1.5) = "1.5"
dbg line 41: ordered(2.5d) = "2.5d"
dbg line 42: ordered("a") = "\"a\""
dbg line 43: ordered(False) = "False"
dbg line 44: shown(3) = "3"
dbg line 45: shown(2.5d) = "2.5d"
dbg line 46: shown("a") = "\"a\""
dbg line 47: same(1, 1) = True
dbg line 48: same(1.5, 2.5) = False
dbg line 49: same(2.5d, 2.50d) = True
dbg line 50: same("a", "a") = True
dbg line 51: same(True, False) = False
dbg line 52: same(b, b) = True
dbg line 53: keys([1, 1, 2]) = 2
dbg line 54: keys([1.5, 2.5]) = 2
dbg line 55: keys([1.5d, 1.50d]) = 1
dbg line 56: keys(["a", "b", "a"]) = 2
dbg line 57: keys([True, True]) = 1
dbg line 58: keys([b, b]) = 1
dbg line 59: both([1, 2]) = 2
dbg line 60: both([1.5d, 1.5d]) = 1
dbg line 61: both([b, b]) = 1
dbg line 65: Debug.inspect(i) = "1"
dbg line 66: Debug.inspect(s) = "\"a\""
dbg line 67: e == 2.5d = True
`
	got := vmReference(path)
	if got.stdout+got.stderr != want || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s", got.exit, got.stdout, got.stderr, want)
	}
}
