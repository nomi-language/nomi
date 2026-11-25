package irbuild

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A block of a generic interface over a generic type is registered for an
// instance when the instance's impls are first asked for (impltables.go).
// `impl Deeper<Box<Box<T>>> for Box<T>` names a larger instance of its
// receiver at each instance, so registering every instance's blocks when
// the instance is built made a chain that only the instance bounds stopped,
// and each program here declined. Each runs under a deadline, so a
// regression that brings the chain back fails and does not hang, and each
// runs again linked through objects.
func TestOnDemandImpl_ABlockAtALargerReceiverRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const deeper = `import std::io

interface Deeper<T>
    fn deeper(value: self) -> T
end

struct Box<T>
    t: T
end

impl Deeper<Box<Box<T>>> for Box<T>
    fn deeper(b: Box<T>) -> Box<Box<T>>
        Box{t: Box{t: b.t}}
    end
end
`
	cases := []struct{ name, src, want string }{
		{
			// The program the gap was pinned by.
			name: "ACallAtTheReceiver",
			src: deeper + `
fn main()
    io.print(Deeper.deeper(Box{t: 1}).t.t)
end
`,
			want: "1\n",
		},
		{
			// Each element is boxed as the interface's instance, and the
			// call dispatches on it.
			name: "ThroughAnInterfaceValueInAList",
			src: deeper + `
fn main()
    items: List<Deeper<Box<Box<Int>>>> = [Box{t: 1}, Box{t: 2}]
    Iter.each(items, fn item -> io.print(Deeper.deeper(item).t.t))
end
`,
			want: "1\n2\n",
		},
		{
			// Two blocks of one interface for one receiver, at different
			// arguments, one of them growing; a bounded call selects each.
			name: "TwoBlocksOfOneInterfaceOneGrowing",
			src: `import std::io

interface Holds<T>
    fn get(value: self) -> T
end

struct Box<T>
    t: T
end

impl Holds<Int> for Box<T>
    fn get(_b: Box<T>) -> Int
        7
    end
end

impl Holds<Box<Box<T>>> for Box<T>
    fn get(b: Box<T>) -> Box<Box<T>>
        Box{t: Box{t: b.t}}
    end
end

fn held<C, T>(c: C) -> T where C: Holds<T>
    C.get(c)
end

fn main()
    b = Box{t: "a"}
    n: Int = held(b)
    io.print(n)
    deep: Box<Box<String>> = held(b)
    io.print(deep.t.t)
end
`,
			want: "7\na\n",
		},
		{
			// The interface's default, inherited by the growing block, called
			// type-qualified by the receiver and by the interface.
			name: "AnInheritedDefaultOnAGrowingBlock",
			src: `import std::io

interface Deeper<T>
    fn deeper(value: self) -> T

    fn twice(value: self) -> (T, T)
        (Deeper.deeper(value), Deeper.deeper(value))
    end
end

struct Box<T>
    t: T
end

impl Deeper<Box<Box<T>>> for Box<T>
    fn deeper(b: Box<T>) -> Box<Box<T>>
        Box{t: Box{t: b.t}}
    end
end

fn main()
    pair = Box.twice(Box{t: 1})
    io.print(pair.second.t.t)
    other = Deeper.twice(Box{t: "a"})
    io.print(other.first.t.t)
end
`,
			want: "1\na\n",
		},
		{
			// The values are boxed as the interface only where a list of Box
			// converts to a list of the interface, and where an Option a std
			// generic built is bound as an Option of it.
			name: "BoxedOnlyThroughAStdGeneric",
			src: deeper + `
fn first_deep(xs: List<Deeper<Box<Box<Int>>>>) -> Int
    case xs
        [x, ..] -> Deeper.deeper(x).t.t
        [] -> 0
    end
end

fn main()
    boxes = [Box{t: 3}]
    io.print(first_deep(boxes))
    held: Option<Deeper<Box<Box<Int>>>> = Option.map(Some(4), fn n -> Box{t: n})
    case held
        Some(d) -> io.print(Deeper.deeper(d).t.t)
        None -> io.print(0)
    end
end
`,
			want: "3\n4\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer lowersWithin(t, time.Minute)()
			verifyLambdaProgram(t, c.src, c.want)
			t.Run("linked through objects", func(t *testing.T) {
				defer LinkThroughObjects()()
				verifyLambdaProgram(t, c.src, c.want)
			})
		})
	}
}

// TestImplTablesAreReadThroughTheAccessor fails for a read or write of the
// impl tables outside impltables.go. A block of a generic interface over a
// generic type's instance is registered only when an impl of the instance
// is asked for through that file, so a direct read elsewhere would miss it.
func TestImplTablesAreReadThroughTheAccessor(t *testing.T) {
	tables := map[string]bool{"implsByIface": true, "instImpls": true, "implItemsByDecl": true, "pendingImpls": true}
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") || path == "impltables.go" {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		ast.Inspect(f, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && tables[sel.Sel.Name] {
				t.Errorf("%s: reads the impl table %s directly; go through impltables.go's accessors",
					fset.Position(sel.Pos()), sel.Sel.Name)
			}
			return true
		})
	}
	if checked < 100 {
		t.Fatalf("checked %d files, too few for the package", checked)
	}
	t.Logf("checked %d files", checked)
}
