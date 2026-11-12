package analysis_test

import (
	"strings"
	"testing"
)

// An operator impl on a generic receiver whose header names the receiver's
// type parameter (`impl Add<T, Box<T>> for Box<T>`) is read at the left
// operand's type arguments. The header's T and the receiver's T are one
// parameter; recording the method signature's separate T as the impl's
// formal parameter left the header's T unbound at every use site, and each
// `Box<Int> + Int` was "no matching Add impl".

const genericOperatorBox = `struct Box<T>
    items: List<T>
end

impl Add<T, Box<T>> for Box<T>
    fn add(b: Box<T>, rhs: T) -> Box<T>
        Box{items: b.items + [rhs]}
    end
end
`

func TestGenericOperatorImpl_Accepted(t *testing.T) {
	rows := []struct {
		name string
		src  string
	}{
		{"set union", `fn main()
    s: Set<Int> = Set[1, 2] + Set[3]
    _ = s
end
`},
		{"set difference", `fn main()
    s: Set<String> = Set["a", "b"] - Set["a"]
    _ = s
end
`},
		{"interface-qualified", `fn main()
    s: Set<Int> = Add.add(Set[1], Set[2])
    _ = s
end
`},
		{"bounded generic", `fn f<T>(a: Set<T>, b: Set<T>) -> Set<T> where T: Hashable
    a + b
end

fn main()
    _ = f(Set[1], Set[2])
end
`},
		{"map merge", `fn main()
    m: Map<String, Int> = {"a" => 1} + {"b" => 2}
    _ = m
end
`},
		{"map in generic code", `fn f<K, V>(a: Map<K, V>, b: Map<K, V>) -> Map<K, V>
    a + b
end

fn main()
    _ = f({1 => "x"}, {2 => "y"})
end
`},
		{"user generic type", genericOperatorBox + `
fn main()
    b: Box<String> = Box{items: ["a"]} + "b"
    _ = b
end
`},
		{"user generic type in generic code", genericOperatorBox + `
fn grow<T>(b: Box<T>, x: T) -> Box<T>
    b + x
end

fn main()
    _ = grow(Box{items: [1]}, 2)
end
`},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			for _, e := range diagnosticsFor(t, row.src) {
				t.Errorf("line %d: %s", e.Line, e.Message)
			}
		})
	}
}

func TestGenericOperatorImpl_RightOperandMismatch(t *testing.T) {
	rows := []struct {
		name string
		src  string
		want string
	}{
		{"set plus element", `fn main()
    _ = Set[1] + 2
end
`, "binary + right operand mismatch: Add expects Set<Int>, got Int"},
		{"set minus element", `fn main()
    _ = Set["a"] - 1
end
`, "binary - right operand mismatch: Subtract expects Set<String>, got Int"},
		{"set plus other element type", `fn main()
    _ = Set[1] + Set["x"]
end
`, "binary + right operand mismatch: Add expects Set<Int>, got Set<String>"},
		{"map plus key", `fn main()
    _ = {"a" => 1} + "b"
end
`, "binary + right operand mismatch: Add expects Map<String, Int>, got String"},
		{"map plus other value type", `fn main()
    _ = {"a" => 1} + {"a" => "x"}
end
`, "binary + right operand mismatch: Add expects Map<String, Int>, got Map<String, String>"},
		{"interface-qualified", `fn main()
    _ = Add.add(Set[1], 2)
end
`, "argument 2: expected Set<Int>, got Int"},
		{"user generic type", genericOperatorBox + `
fn main()
    _ = Box{items: [1]} + "x"
end
`, "binary + right operand mismatch: Add expects Int, got String"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			errs := diagnosticsFor(t, row.src)
			found := false
			for _, e := range errs {
				if strings.Contains(e.Message, row.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("want an error containing %q, got %v", row.want, errs)
			}
		})
	}
}

// A receiver with several impls of one operator keeps the "no matching impl"
// report: no single right-hand type is the one the operand should have been.
func TestGenericOperatorImpl_SeveralImplsStillNoMatch(t *testing.T) {
	src := `type Day Int
type Days Int
type Weeks Int

impl Add<Days, Day> for Day
    fn add(Day(d), Days(n)) -> Day
        Day(d + n)
    end
end

impl Add<Weeks, Day> for Day
    fn add(Day(d), Weeks(n)) -> Day
        Day(d + 7 * n)
    end
end

fn main()
    _ = Day(1) + "x"
end
`
	errs := diagnosticsFor(t, src)
	want := "no matching Add impl for Day + String"
	for _, e := range errs {
		if strings.Contains(e.Message, want) {
			return
		}
	}
	t.Fatalf("want an error containing %q, got %v", want, errs)
}

// There is no `Map - Map`: a map difference has no single obvious meaning
// (by key, or by entry), so `-` on two maps is the missing-impl error.
func TestGenericOperatorImpl_NoMapSubtract(t *testing.T) {
	src := `fn main()
    _ = {"a" => 1} - {"a" => 1}
end
`
	errs := diagnosticsFor(t, src)
	want := "no impl of `Subtract` for `Map`"
	for _, e := range errs {
		if strings.Contains(e.Message, want) {
			return
		}
	}
	t.Fatalf("want an error containing %q, got %v", want, errs)
}

// Instant's two `Subtract` impls differ in the right operand and the result:
// `Instant - Duration` is an Instant, `Instant - Instant` a Duration. The
// checker picks the impl by the right operand, so each result type is the one
// that impl names.
const instantSubtractImports = `import std::duration.Duration
import std::instant.Instant
`

func TestInstantSubtract_SelectsByRightOperand(t *testing.T) {
	src := instantSubtractImports + `
fn main()
    a = Instant.from_seconds(15)
    d: Duration = a - Instant.from_seconds(10)
    i: Instant = a - Duration.seconds(5)
    q: Duration = Subtract.subtract(a, Instant.from_seconds(10))
    _ = (d, i, q)
end
`
	for _, e := range diagnosticsFor(t, src) {
		t.Errorf("line %d: %s", e.Line, e.Message)
	}
}

func TestInstantSubtract_Rejected(t *testing.T) {
	rows := []struct {
		name string
		src  string
		want string
	}{
		{"instant minus int", instantSubtractImports + `
fn main()
    _ = Instant.from_seconds(15) - 5
end
`, "no matching Subtract impl for Instant - Int"},
		{"instant minus instant is not an instant", instantSubtractImports + `
fn main()
    i: Instant = Instant.from_seconds(15) - Instant.from_seconds(10)
    _ = i
end
`, "expected Instant, got Duration"},
		{"instant minus duration is not a duration", instantSubtractImports + `
fn main()
    d: Duration = Instant.from_seconds(15) - Duration.seconds(5)
    _ = d
end
`, "expected Duration, got Instant"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			errs := diagnosticsFor(t, row.src)
			for _, e := range errs {
				if strings.Contains(e.Message, row.want) {
					return
				}
			}
			t.Fatalf("want an error containing %q, got %v", row.want, errs)
		})
	}
}
