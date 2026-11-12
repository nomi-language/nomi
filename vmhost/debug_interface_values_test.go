package vmhost_test

import "testing"

// Debug.inspect of a program interface value names every implementer's and
// every boxed value's Debug from the checker's record of what enters the
// interface, so a body lowered before the one that boxes the value (a
// derived Debug, a function declared first) renders it, a List or a Set
// held as the interface included, and so does a generic program type's
// instance (Bx<Int>) held as one.
func TestRun_DebugOfInterfaceValues(t *testing.T) {
	out := runOutput(t, `import std::io

interface Show
    fn show(value: self) -> String
end

impl Show for List<T>
    fn show(_xs: List<T>) -> String
        "list"
    end
end

impl Show for Set<T>
    fn show(_xs: Set<T>) -> String
        "set"
    end
end

interface Labeled
    fn label(value: self) -> String
end

struct Bx<T>
    v: T
end

impl Labeled for Bx<T>
    fn label(_b: Bx<T>) -> String
        "bx"
    end
end

struct Drv
    v: Show
end

derive Debug for Drv

fn look(s: Show) -> String
    Debug.inspect(s)
end

fn main()
    io.print(Debug.inspect(Drv{v: [1, 2]}))
    io.print(Debug.inspect(Drv{v: Set[3]}))
    xs: List<Show> = [[1], Set[2]]
    io.print(Debug.inspect(xs))
    io.print(look([4]))
    l: Labeled = Bx{v: 3}
    io.print(Debug.inspect(l))
end
`)
	want := "Drv{v: [1, 2]}\nDrv{v: Set[3]}\n[[1], Set[2]]\n[4]\nBx{v: 3}\n"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}
