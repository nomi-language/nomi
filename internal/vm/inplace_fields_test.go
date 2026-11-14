package vm_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/vmhost"
)

// runBothWays runs src in place and copying and requires both to print
// want.
func runBothWays(t *testing.T, src, want string) {
	t.Helper()
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

// TestInPlace_AFieldReadKeepsItsValue: a field read out of an owned value
// is written in place only when nothing reads the field through the value
// again. A field read kept past an update that keeps the field, past a
// call the value is handed to, or past a second read written in place,
// keeps its value; so does a value kept beside the read. The output is the
// same in place and copying.
func TestInPlace_AFieldReadKeepsItsValue(t *testing.T) {
	const src = `import std::io

struct Inner
    x: Int
end

struct S
    count: Int
    v: Vector<Int>
    inner: Inner
end

fn fresh() -> S
    S{count: 0, v: Vector[1, 2, 3], inner: Inner{x: 5}}
end

fn bump_inner(s: S) -> S
    {..s, inner: {.., x: .. + 100}}
end

fn set_v(s: S) -> S
    {..s, v: Vector.set(s.v, 0, 0)}
end

fn main()
    s = fresh()
    d = s.v
    s2 = {..s, count: 1}
    d2 = Vector.set(d, 0, 9)
    io.print("${s2.v} ${d2}")

    t = fresh()
    keep = t
    e = Vector.set(t.v, 1, 9)
    io.print("${keep.v} ${e}")

    u = fresh()
    a = u.v
    b = Vector.set(u.v, 0, 7)
    io.print("${Vector.get(a, 0)} ${b}")

    w = fresh()
    old = w.inner
    w2 = bump_inner(w)
    io.print("${old.x} ${w2.inner.x}")

    y = fresh()
    yv = y.v
    y2 = set_v(y)
    io.print("${yv} ${y2.v}")

    z = bump_inner(set_v(fresh()))
    io.print("${z.v} ${z.inner.x}")

    pair = (Vector[1, 2], 3)
    (pv, pn) = pair
    io.print("${Vector.set(pv, 0, pn)} ${pair}")
end
`
	const want = "Vector[1, 2, 3] Vector[9, 2, 3]\nVector[1, 2, 3] Vector[1, 9, 3]\nSome(1) Vector[7, 2, 3]\n" +
		"5 105\nVector[1, 2, 3] Vector[0, 2, 3]\nVector[0, 2, 3] 105\nVector[3, 2] (Vector[1, 2], 3)\n"
	runBothWays(t, src, want)
}

// TestInPlace_AFoldKeepsWhatItDoesNotOwn: a fold hands its callback the
// accumulator as owned only when the activation handed the seed over and
// the callback's answer is the accumulator's again. A seed read after the
// fold, an accumulator the callback keeps beside its answer, a captured
// value the callback answers, and a map_reduce's state keep their values.
func TestInPlace_AFoldKeepsWhatItDoesNotOwn(t *testing.T) {
	const src = `import std::io

fn main()
    v = Vector[0, 0, 0]
    r = Iter.reduce(0..3, v, fn acc, i -> Vector.set(acc, i, i + 1))
    io.print("${v} ${r}")

    (last, hist) = Iter.reduce(0..3, (Vector[0, 0, 0], []), fn (acc, seen), i ->
        (Vector.set(acc, i, 1), [acc, ..seen])
    end)
    io.print("${last} ${hist}")

    w = Vector[5, 5]
    c = Iter.reduce(0..2, Vector[0, 0], fn acc, i -> if i == 0 then w else Vector.set(acc, 0, 1))
    io.print("${w} ${c}")

    (state, outs) = Iter.map_reduce(0..3, Vector[0, 0, 0], fn acc, i ->
        next = Vector.set(acc, i, 1)
        (next, Vector.length(next) + i)
    end)
    io.print("${state} ${outs}")

    owned = Iter.reduce(0..3, (Vector[0, 0, 0], 0), fn (acc, n), i -> (Vector.set(acc, i, 2), n + i))
    io.print("${owned}")
end
`
	const want = "Vector[0, 0, 0] Vector[1, 2, 3]\n" +
		"Vector[1, 1, 1] [Vector[1, 1, 0], Vector[1, 0, 0], Vector[0, 0, 0]]\n" +
		"Vector[5, 5] Vector[1, 5]\n" +
		"Vector[1, 1, 1] [3, 4, 5]\n" +
		"(Vector[2, 2, 2], 3)\n"
	runBothWays(t, src, want)
}

// sieveFold folds a tuple of a vector and a count through Iter.reduce,
// handing the vector to a call at its last use, as AWFY's Sieve does.
const sieveFold = `import std::io

fn clear(flags: Vector<Bool>, k: Int, i: Int, size: Int) -> Vector<Bool>
    if k > size then flags else clear(Vector.set(flags, k - 1, False), k + i, i, size)
end

fn sieve(flags: Vector<Bool>, size: Int) -> Int
    (_, primes) = Iter.reduce(2..=size, (flags, 0), fn (flags, primes), i ->
        if Vector.get(flags, i - 1)! then
            (clear(flags, i + i, i, size), primes + 1)
        else
            (flags, primes)
        end
    end)

    primes
end

fn down(n: Int, acc: Int) -> Int
    if n < 1 then acc else down(n - 1, acc + sieve(Iter.to_vector(Iter.map(1..=64, fn _ -> True)), 64))
end

fn main()
    down(%d, 0) |> io.print()
end
`

// TestInPlace_AFoldHandsItsAccumulatorOn: the sieve's flags, handed to
// the fold in a tuple and out of it to clear, are set in place: an
// activation allocates its vector, its tuples and the fold's machinery,
// not a copy of the vector per cleared flag.
func TestInPlace_AFoldHandsItsAccumulatorOn(t *testing.T) {
	inPlace := allocsPerActivation(t, sieveFold)
	t.Setenv(vm.NoInPlaceEnv, "1")
	copying := allocsPerActivation(t, sieveFold)
	// 64 flags clear 64 - 18 times once each or more (every multiple of
	// each prime): copying, each clear copies a leaf and a header.
	if copying-inPlace < 100 {
		t.Errorf("a sieve allocates %.0f times in place and %.0f copying, want at least 100 fewer in place", inPlace, copying)
	}
	if !strings.Contains(sieveFold, "Iter.reduce") {
		t.Fatal("the fixture lost its fold")
	}
}

// TestInPlace_AProfilingMachineWritesInPlace: under NOMI_PROFILE a vector
// handed from call to call is set in place, as on a plain machine; the
// profiler reads no values. Copying, each set allocates.
func TestInPlace_AProfilingMachineWritesInPlace(t *testing.T) {
	t.Setenv("NOMI_PROFILE", "1")
	measure := func() float64 {
		const low, high = 50, 200
		at := func(n int) float64 {
			p, err := vmhost.LoadSource("main", fmt.Sprintf(ownedVector, n), vmhost.WithErrorOutput(io.Discard))
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			return testing.AllocsPerRun(10, func() {
				out.Reset()
				if err := p.Run(context.Background(), &out, nil, false); err != nil {
					t.Fatal(err)
				}
			})
		}
		return (at(high) - at(low)) / (high - low)
	}
	if per := measure(); per > 0.5 {
		t.Errorf("profiled, a set on an owned vector allocates %.2f times per activation, want 0", per)
	}
	t.Setenv(vm.NoInPlaceEnv, "1")
	if per := measure(); per < 0.9 {
		t.Errorf("profiled with %s set, a set allocates %.2f times per activation, want at least 1", vm.NoInPlaceEnv, per)
	}
}
