package vm_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/vmhost"
)

// inPlaceChain builds a literal and updates it three times per activation:
// every update is its base's last use.
const inPlaceChain = `import std::io

struct P
    x: Int
    y: Int
    z: Int
end

fn build(n: Int) -> Int
    p = P{x: n, y: 0, z: 0}
    q = {..p, y: n}
    r = {..q, z: n}
    s = {..r, x: 0}
    s.x + s.y + s.z
end

fn down(n: Int, acc: Int) -> Int
    if n < 1 then acc else down(n - 1, acc + build(n))
end

fn main()
    down(%d, 0) |> io.print()
end
`

// TestInPlace_AChainOfUpdatesAllocatesItsLiteral: a literal updated three
// times allocates once, for the literal; copying, each update allocates its
// clone too.
func TestInPlace_AChainOfUpdatesAllocatesItsLiteral(t *testing.T) {
	if per := allocsPerActivation(t, inPlaceChain); per > 1.1 {
		t.Errorf("a literal and three updates of it allocate %.2f times, want 1 (the literal)", per)
	}
	t.Setenv(vm.NoInPlaceEnv, "1")
	if per := allocsPerActivation(t, inPlaceChain); per < 3.9 || per > 4.1 {
		t.Errorf("with %s set, a literal and three updates allocate %.2f times, want 4", vm.NoInPlaceEnv, per)
	}
}

// TestInPlace_TheUpdatesCompileInPlace: build's three updates compile to
// opUpdateIn (`update!`) on a plain machine, and to opUpdate on a debug
// machine, whose stopped frames show variables past their last use.
func TestInPlace_TheUpdatesCompileInPlace(t *testing.T) {
	p, err := vmhost.LoadSource("main", strings.Replace(inPlaceChain, "%d", "3", 1))
	if err != nil {
		t.Fatal(err)
	}
	listing, err := p.Disassemble("build")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(listing, " update! "); got != 3 {
		t.Errorf("build compiles %d updates in place, want 3:\n%s", got, listing)
	}
	restore := vm.DebugEveryMachine()
	defer restore()
	listing, err = p.Disassemble("build")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(listing, " update! ") || strings.Count(listing, " update ") != 3 {
		t.Errorf("a debug machine's build does not copy at each update:\n%s", listing)
	}
}

// TestInPlace_AnAliasedBaseKeepsItsValue: a base read after its update, a
// base captured by a lambda, one stored in a list and one passed to a call
// keep their fields, and an update of a value the next statement reads
// leaves it whole; the output is the same in place and copying.
func TestInPlace_AnAliasedBaseKeepsItsValue(t *testing.T) {
	const src = `import std::io

struct P
    x: Int
    y: Int
end

fn show(p: P) -> String
    "${p.x},${p.y}"
end

fn main()
    p = P{x: 1, y: 2}
    q = {..p, x: 10}
    io.print("${show(p)} ${show(q)}")

    c = P{x: 3, y: 4}
    f = fn n: Int -> n + c.x
    d = {..c, x: 30}
    io.print("${f(0)} ${show(d)}")

    l = P{x: 5, y: 6}
    ls = [l]
    m = {..l, y: 60}
    io.inspect(ls)
    io.print(show(m))

    a = P{x: 7, y: 8}
    before = show(a)
    b = {..a, x: 70}
    io.print("${before} ${show(a)} ${show(b)}")

    r = if p.x > 0 then {..q, y: 20} else q
    io.print("${show(q)} ${show(r)}")
end
`
	const want = "1,2 10,2\n3 30,4\n[P{x: 5, y: 6}]\n5,60\n7,8 7,8 70,8\n10,2 10,20\n"
	run := func() string {
		p, err := vmhost.LoadSource("main", src)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := p.Run(context.Background(), &out, nil, false); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if got := run(); got != want {
		t.Errorf("in place:\n%s\nwant:\n%s", got, want)
	}
	t.Setenv(vm.NoInPlaceEnv, "1")
	if got := run(); got != want {
		t.Errorf("copying:\n%s\nwant:\n%s", got, want)
	}
}

// ownedStep threads one struct through a call per activation: run hands p
// to step, which updates it and returns it, and run's tail call hands the
// result back to itself.
const ownedStep = `import std::io

struct P
    x: Int
    y: Int
end

fn step(p: P, i: Int) -> P
    {..p, x: p.x + i}
end

fn run(p: P, n: Int) -> P
    if n < 1 then p else run(step(p, n), n - 1)
end

fn main()
    run(P{x: 0, y: 0}, %d).x |> io.print()
end
`

// TestInPlace_AnOwnedParameterIsUpdatedInPlace: a struct handed from call
// to call, each time at its last use, is updated in place in the callee, so
// an activation allocates nothing; copying, each update allocates its clone.
func TestInPlace_AnOwnedParameterIsUpdatedInPlace(t *testing.T) {
	if per := allocsPerActivation(t, ownedStep); per > 0.1 {
		t.Errorf("an owned struct updated once per activation allocates %.2f times, want 0", per)
	}
	t.Setenv(vm.NoInPlaceEnv, "1")
	if per := allocsPerActivation(t, ownedStep); per < 0.9 || per > 1.1 {
		t.Errorf("with %s set, an update per activation allocates %.2f times, want 1", vm.NoInPlaceEnv, per)
	}
}

// TestInPlace_ABorrowedParameterKeepsItsValue: a callee that updates its
// parameter copies when the caller keeps the value (it reads it after the
// call, passes it twice, calls through a function value, or got it from a
// collection), and the output is the same in place and copying.
func TestInPlace_ABorrowedParameterKeepsItsValue(t *testing.T) {
	const src = `import std::io

struct P
    x: Int
    y: Int
end

fn show(p: P) -> String
    "${p.x},${p.y}"
end

fn bump(p: P) -> P
    {..p, x: p.x + 100}
end

fn both(a: P, b: P) -> String
    c = {..a, x: 9}
    "${show(c)} ${show(b)}"
end

fn same(p: P) -> P
    p
end

fn twice(p: P, n: Int) -> P
    if n < 1 then p else twice(bump(p), n - 1)
end

fn main()
    p = P{x: 1, y: 2}
    q = bump(p)
    io.print("${show(p)} ${show(q)}")

    io.print(both(P{x: 3, y: 4}, P{x: 5, y: 6}))
    r = P{x: 7, y: 8}
    io.print(both(r, r))

    f = bump
    s = P{x: 10, y: 11}
    io.print("${show(f(s))} ${show(s)}")

    ps = [P{x: 12, y: 13}]
    case ps
        [first, ..] -> io.print("${show(bump(first))} ${show(first)}")
        [] -> io.print("none")
    end
    io.inspect(ps)

    u = P{x: 14, y: 15}
    v = same(u)
    w = bump(v)
    io.print("${show(u)} ${show(w)}")

    k = P{x: 0, y: 0}
    io.print("${show(twice(k, 3))} ${show(k)}")

    owned = twice(P{x: 0, y: 1}, 3)
    io.print(show(owned))
end
`
	const want = "1,2 101,2\n9,4 5,6\n9,8 7,8\n110,11 10,11\n112,13 12,13\n[P{x: 12, y: 13}]\n14,15 114,15\n300,0 0,0\n300,1\n"
	run := func() string {
		p, err := vmhost.LoadSource("main", src)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := p.Run(context.Background(), &out, nil, false); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if got := run(); got != want {
		t.Errorf("in place:\n%s\nwant:\n%s", got, want)
	}
	t.Setenv(vm.NoInPlaceEnv, "1")
	if got := run(); got != want {
		t.Errorf("copying:\n%s\nwant:\n%s", got, want)
	}
}

// TestInPlace_ADebugMachineHandsNothingOn: on a debug machine run's call
// hands step nothing and step's update copies, so a debugger stopped in
// either frame sees each value as it was bound.
func TestInPlace_ADebugMachineHandsNothingOn(t *testing.T) {
	p, err := vmhost.LoadSource("main", strings.Replace(ownedStep, "%d", "3", 1))
	if err != nil {
		t.Fatal(err)
	}
	for name, plain := range map[string]string{"run": " tail! ", "step": " update! "} {
		listing, err := p.Disassemble(name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(listing, plain) {
			t.Errorf("a plain machine's %s has no%s:\n%s", name, plain, listing)
		}
	}
	restore := vm.DebugEveryMachine()
	defer restore()
	for _, name := range []string{"run", "step"} {
		listing, err := p.Disassemble(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(listing, "! ") {
			t.Errorf("a debug machine's %s hands on or writes in place:\n%s", name, listing)
		}
	}
}

// ownedVector sets one element of a vector per activation, handing the
// vector from call to call at its last use.
const ownedVector = `import std::io

fn fill(v: Vector<Int>, n: Int) -> Vector<Int>
    if n < 1 then v else fill(Vector.set(v, n %% 64, n), n - 1)
end

fn main()
    v = Iter.to_vector(Iter.map(0..64, fn i -> i))
    Vector.get(fill(v, %d), 3)! |> io.print()
end
`

// TestInPlace_AnOwnedVectorIsSetInPlace: a vector handed from call to call
// is set in place, so an activation allocates nothing; copying, each set
// copies the vector's leaf and header.
func TestInPlace_AnOwnedVectorIsSetInPlace(t *testing.T) {
	if per := allocsPerActivation(t, ownedVector); per > 0.1 {
		t.Errorf("a set on an owned vector allocates %.2f times per activation, want 0", per)
	}
	t.Setenv(vm.NoInPlaceEnv, "1")
	if per := allocsPerActivation(t, ownedVector); per < 0.9 {
		t.Errorf("with %s set, a set allocates %.2f times per activation, want at least 1", vm.NoInPlaceEnv, per)
	}
}

// TestInPlace_AKeptCollectionKeepsItsValue: a vector, map or set a program
// reads after updating it, keeps in a list, passes twice or updates through
// a function value is copied; one it hands on is written in place; the
// output is the same both ways.
func TestInPlace_AKeptCollectionKeepsItsValue(t *testing.T) {
	const src = `import std::io

fn bump(v: Vector<Int>) -> Vector<Int>
    Vector.set(v, 0, Vector.get(v, 0)! + 100)
end

fn grow(m: Map<Int, Int>, n: Int) -> Map<Int, Int>
    if n < 1 then m else grow(Map.put(m, n, n * n), n - 1)
end

fn both(a: Vector<Int>, b: Vector<Int>) -> String
    c = Vector.push(a, 9)
    "${c} ${b}"
end

fn main()
    v = Vector[1, 2, 3]
    w = bump(v)
    io.print("${v} ${w}")

    owned = bump(Vector[4, 5])
    io.print("${bump(owned)}")

    kept = [Vector[6]]
    case kept
        [first, ..] -> io.print("${bump(first)} ${first}")
        [] -> io.print("none")
    end

    x = Vector[7]
    io.print(both(x, x))

    f = bump
    y = Vector[8, 9]
    io.print("${f(y)} ${y}")

    m = grow({}, 4)
    m2 = Map.put(m, 1, 0)
    io.print("${m} ${m2}")
    m3 = Map.remove(grow({}, 3), 2)
    io.print("${m3}")

    s = Set[1, 2]
    s2 = Set.insert(s, 3)
    s3 = Set.remove(Set.insert(Set[5], 6), 5)
    io.print("${s} ${s2} ${s3}")

    big = Iter.to_vector(0..100)
    big2 = Vector.push(Vector.set(big, 50, -1), 100)
    io.print("${Vector.get(big, 50)} ${Vector.get(big2, 50)} ${Vector.length(big)} ${Vector.length(big2)}")
end
`
	const want = "Vector[1, 2, 3] Vector[101, 2, 3]\nVector[204, 5]\nVector[106] Vector[6]\n" +
		"Vector[7, 9] Vector[7]\nVector[108, 9] Vector[8, 9]\n" +
		"{4 => 16, 3 => 9, 2 => 4, 1 => 1} {4 => 16, 3 => 9, 2 => 4, 1 => 0}\n{3 => 9, 1 => 1}\n" +
		"Set[1, 2] Set[1, 2, 3] Set[6]\nSome(50) Some(-1) 100 101\n"
	run := func() string {
		p, err := vmhost.LoadSource("main", src)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := p.Run(context.Background(), &out, nil, false); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if got := run(); got != want {
		t.Errorf("in place:\n%s\nwant:\n%s", got, want)
	}
	t.Setenv(vm.NoInPlaceEnv, "1")
	if got := run(); got != want {
		t.Errorf("copying:\n%s\nwant:\n%s", got, want)
	}
}
