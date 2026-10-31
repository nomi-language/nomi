package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// A boxed value renders as the value: in `dbg`, in `io.inspect` and in a
// failed assertion's `values:` rows.
func TestIRBoxedExistential_RendersAsTheValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "boxed_test.nomi")
	src := `import std/io

interface Show {
  fn show(value: self) -> String
}

impl Show for List<T> {
  fn show(xs: List<T>) -> String {
    "n${Iter.count(xs)}"
  }
}

test "rows" {
  a: Show = [1, 2]
  dbg(a)
  io.inspect(a)
  xs: List<Show> = [a]
  ys: List<Show> = [["s"]]
  assert xs == ys
}
`
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range res.IR {
		if m.Name() != path || len(m.Tests()) != 1 {
			continue
		}
		var out bytes.Buffer
		_, reason := vm.NewProgram(m, res.IRModules(), &out).RunTests(path)
		if reason != "" {
			t.Fatalf("the VM could not run the case: %s", reason)
		}
		got := strings.ReplaceAll(out.String(), path, "<path>")
		for _, want := range []string{
			"dbg line 15: a = [1, 2]\n",
			"  [1, 2]\n",
			"      xs\n        = [[1, 2]]\n      ys\n        = [[\"s\"]]\n",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("the report lacks %q:\n%s", want, got)
			}
		}
		return
	}
	t.Fatal("the fixture retained no case")
}

// A value enters a program interface boxed only when its runtime value does
// not name its impl: a std container instance or an enum with an `embeds`
// variant. A struct, a plain enum and a scalar enter unchanged, so holding
// one as an existential builds nothing. Counted per erasure in main.
func TestIRBoxedExistential_OnlyWhereTheValueNamesNoImpl(t *testing.T) {
	const src = `interface Show {
    fn show(value: self) -> String
}

struct P {
    x: Int
}

enum Coin {
    Penny
    Dime
}

struct Circle {
    r: Int
}

enum Shape {
    embeds Circle
    Dot
}

impl Show for P {
    fn show(p: P) -> String {
        "p${p.x}"
    }
}

impl Show for Coin {
    fn show(_c: Coin) -> String {
        "coin"
    }
}

impl Show for Int {
    fn show(n: Int) -> String {
        "i${n}"
    }
}

impl Show for Shape {
    fn show(_s: Shape) -> String {
        "shape"
    }
}

impl Show for List<T> where T: Show {
    fn show(xs: List<T>) -> String {
        xs |> Iter.map(&T.show(&1)) |> String.join(" ")
    }
}

fn plain() -> List<Show> {
    [#P{x: 1}, Coin.Penny, 3]
}

fn boxed() -> List<Show> {
    s: Shape = #Circle{r: 1}
    [[1], s, [[2]]]
}

fn main() {
    _ = plain()
    _ = boxed()
}
`
	res := lowerIROnly(t, src)
	counts := map[string]int{}
	keys := map[string][]string{}
	for _, m := range res.IR {
		for _, f := range m.Funcs() {
			for _, b := range f.Blocks() {
				for _, in := range b.Instrs() {
					if mk, ok := in.(*ir.Make); ok && mk.Kind() == ir.MakeExistential {
						counts[f.Name()]++
						keys[f.Name()] = append(keys[f.Name()], mk.Typ().Name())
					}
				}
			}
		}
	}
	if counts["plain"] != 0 {
		t.Errorf("a struct, a plain enum and an Int held as Show built %d box(es): %v", counts["plain"], keys["plain"])
	}
	want := []string{"List<Int>", "main.Shape", "List<List<Int>>"}
	got := keys["boxed"]
	if len(got) != len(want) {
		t.Fatalf("boxed built %v, want one box each named %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("box %d is named %q, want %q", i, got[i], want[i])
		}
	}
}

// std's Display and Debug box as a program interface does: a container or an
// `embeds` enum's value held as one is boxed, named by its static type, so
// two empty lists of different element types are two values; a struct, a
// plain enum and a scalar are held unchanged and build nothing.
func TestIRBoxedExistential_StdInterfacesBoxOnlyWhereTheValueNamesNoImpl(t *testing.T) {
	const src = `struct P {
    x: Int
}

enum Coin {
    Penny
    Dime
}

struct Circle {
    r: Int
}

enum Shape {
    embeds Circle
    Dot
}

impl Display for P {
    fn to_string(p: P) -> String {
        "p${p.x}"
    }
}

impl Display for Coin {
    fn to_string(_c: Coin) -> String {
        "coin"
    }
}

impl Display for Shape {
    fn to_string(_s: Shape) -> String {
        "shape"
    }
}

fn plain() -> List<Display> {
    [#P{x: 1}, Coin.Penny, 3, "s"]
}

fn plain_debug() -> List<Debug> {
    [#P{x: 1}, Coin.Penny, 3]
}

fn boxed() -> List<Display> {
    s: Shape = #Circle{r: 1}
    ints: List<Int> = []
    strings: List<String> = []
    [ints, strings, s, %{"a" => 1}]
}

fn boxed_debug() -> List<Debug> {
    s: Shape = #Circle{r: 1}
    [[1], Some(2), s]
}

fn main() {
    _ = plain()
    _ = plain_debug()
    _ = boxed()
    _ = boxed_debug()
}
`
	res := lowerIROnly(t, src)
	keys := map[string][]string{}
	for _, m := range res.IR {
		for _, f := range m.Funcs() {
			for _, b := range f.Blocks() {
				for _, in := range b.Instrs() {
					if mk, ok := in.(*ir.Make); ok && mk.Kind() == ir.MakeExistential {
						keys[f.Name()] = append(keys[f.Name()], mk.Typ().Name())
					}
				}
			}
		}
	}
	for _, name := range []string{"plain", "plain_debug"} {
		if len(keys[name]) != 0 {
			t.Errorf("%s: a struct, a plain enum and a scalar held as a std interface built boxes %v", name, keys[name])
		}
	}
	for name, want := range map[string][]string{
		"boxed":       {"List<Int>", "List<String>", "main.Shape", "Map<String, Int>"},
		"boxed_debug": {"List<Int>", "maybe.Maybe<Int>", "main.Shape"},
	} {
		got := keys[name]
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s built boxes %v, want one each named %v", name, got, want)
		}
	}
}

// A container of an `embeds` enum's values held as `Display` renders each
// element through the enum's impl, as the interpolation of the container
// does, and not through the impl of the struct an element embeds.
func TestIRBoxedExistential_DisplayOfAContainerOfAnEmbedsEnum(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
struct Circle {
  r: Int
}
enum Shape {
  embeds Circle
  Dot
}
impl Display for Circle {
  fn to_string(c: Circle) -> String {
    "circle ${c.r}"
  }
}
impl Display for Shape {
  fn to_string(s: Shape) -> String {
    case s {
      #.Circle{r} -> "shape ${r}"
      .Dot -> "dot"
    }
  }
}
fn shape(r: Int) -> Shape {
  #Circle{r}
}
fn show(d: Display) -> String {
  "${d}"
}
fn main() {
  d: Display = [shape(1), Shape.Dot]
  io.print(Display.to_string(d))
  io.print(show(d))
  io.print("${[shape(1), Shape.Dot]}")
  n: Display = [[shape(2)], []]
  io.print(Display.to_string(n))
  v: Display = #[shape(3)]
  io.print(Display.to_string(v))
  m: Display = %{shape(4) => [shape(5)]}
  io.print(Display.to_string(m))
  xs: List<Display> = [[shape(6)], [7]]
  io.print("${xs}")
}`, "[shape 1, dot]\n[shape 1, dot]\n[shape 1, dot]\n[[shape 2], []]\n#[shape 3]\n%{shape 4 => [shape 5]}\n[[shape 6], [7]]\n")
}

// `==` and `!=` on two interface values are one comparison each of the
// interface-value shape, which the machine answers by the operands'
// run-time types; `==` on two values of a concrete type is unchanged.
func TestIRExistentialEquality_IsOneInterfaceComparison(t *testing.T) {
	const src = `interface Show {
    fn show(value: self) -> String
}

struct P {
    x: Int
}

impl Show for P {
    fn show(p: P) -> String {
        "p${p.x}"
    }
}

fn held(a: Show, b: Show) -> Bool {
    a == b and not (a != b)
}

fn plain(a: P, b: P) -> Bool {
    a == b
}

fn main() {
    _ = held(#P{x: 1}, #P{x: 1})
    _ = plain(#P{x: 1}, #P{x: 1})
}
`
	res := lowerIROnly(t, src)
	shapes := map[string][]ir.ValShape{}
	for _, m := range res.IR {
		for _, f := range m.Funcs() {
			for _, b := range f.Blocks() {
				for _, in := range b.Instrs() {
					if c, ok := in.(*ir.Compare); ok {
						shapes[f.Name()] = append(shapes[f.Name()], c.Shape())
					}
				}
			}
		}
	}
	if got := shapes["held"]; len(got) != 2 || got[0] != ir.ValIface || got[1] != ir.ValIface {
		t.Errorf("held compares %v, want two interface-value comparisons", got)
	}
	if got := shapes["plain"]; len(got) != 1 || got[0] != ir.ValStruct {
		t.Errorf("plain compares %v, want one struct comparison", got)
	}
}

// `Equatable.equal` on two interface values is the one comparison `==` is,
// and `Hashable.hash` on one is one call of the machine's hash of a value,
// both written out and inside a body instantiated at the interface through
// an Equatable or Hashable bound.
func TestIRExistentialBounds_CompareAndHashAsTheMachineDoes(t *testing.T) {
	const src = `interface Show {
    fn show(value: self) -> String
}

struct P {
    x: Int
}

impl Show for P {
    fn show(p: P) -> String {
        "p${p.x}"
    }
}

fn same<T>(a: T, b: T) -> Bool where T: Equatable {
    Equatable.equal(a, b)
}

fn bucket<T>(x: T) -> Int where T: Hashable {
    Hashable.hash(x)
}

fn held(a: Show, b: Show) -> Bool {
    Equatable.equal(a, b) and Hashable.hash(a) == Hashable.hash(b)
}

fn main() {
    a: Show = #P{x: 1}
    _ = held(a, a)
    _ = same(a, a)
    _ = bucket(a)
}
`
	res := lowerIROnly(t, src)
	compares, hashes := map[string][]ir.ValShape{}, map[string]int{}
	for _, m := range res.IR {
		for _, f := range m.Funcs() {
			for _, b := range f.Blocks() {
				for _, in := range b.Instrs() {
					if c, ok := in.(*ir.Compare); ok && c.Shape() == ir.ValIface {
						compares[f.Name()] = append(compares[f.Name()], c.Shape())
					}
					if c, ok := in.(*ir.Call); ok && c.Crosses() && c.Callee().Name() == irHashValueHost {
						hashes[f.Name()]++
					}
				}
			}
		}
	}
	if len(compares["held"]) != 1 || hashes["held"] != 2 {
		t.Errorf("held has %d interface comparisons and %d value hashes, want 1 and 2", len(compares["held"]), hashes["held"])
	}
	var sameCompares, bucketHashes int
	for name, cs := range compares {
		if strings.HasPrefix(name, "same") {
			sameCompares += len(cs)
		}
	}
	for name, n := range hashes {
		if strings.HasPrefix(name, "bucket") {
			bucketHashes += n
		}
	}
	if sameCompares != 1 || bucketHashes != 1 {
		t.Errorf("same<Show> has %d interface comparisons and bucket<Show> %d value hashes, want 1 each (%v %v)", sameCompares, bucketHashes, compares, hashes)
	}
}

// A Maybe, a Result or a Set held as Display renders as its interpolation
// does: the box names std's impl instantiated at the container's kind, an
// `embeds` enum's elements through the enum's own impl.
func TestIRBoxedExistential_DisplayOfAStdContainerWithItsOwnImpl(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
struct Circle {
  r: Int
}
enum Shape {
  embeds Circle
  Dot
}
impl Display for Shape {
  fn to_string(s: Shape) -> String {
    case s {
      #.Circle{r} -> "shape ${r}"
      .Dot -> "dot"
    }
  }
}
fn shape(r: Int) -> Shape {
  #Circle{r}
}
fn show(d: Display) -> String {
  "${d}"
}
fn main() {
  s: Display = %[1, 2]
  io.print(Display.to_string(s))
  m: Display = Some(shape(1))
  io.print(show(m))
  r: Result<Int, String> = Err("bad")
  e: Display = r
  io.print(show(e))
  xs: List<Display> = [Some(1), Some([shape(2)])]
  io.print("${xs}")
  n: Display = Some(1)
  o: Display = Some(1)
  io.print(Bool.to_string(n == o))
}`, "%[1, 2]\nSome(shape 1)\nErr(bad)\n[Some(1), Some([shape 2])]\nTrue\n")
}

// A source of an `embeds` enum's values entering a declared
// `Iter<Display>` boxes each element as it is read, so Display reaches the
// enum's impl and not the struct an element embeds: a List, a Set and a
// lazy Iter.
func TestIRBoxedExistential_EmbedsEnumSourceAsIterOfDisplay(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
struct Circle {
  r: Int
}
enum Shape {
  embeds Circle
  Dot
}
impl Display for Shape {
  fn to_string(s: Shape) -> String {
    case s {
      #.Circle{r} -> "shape ${r}"
      .Dot -> "dot"
    }
  }
}
fn one() -> Shape {
  #Circle{r: 1}
}
fn show(xs: Iter<Display>) -> String {
  xs |> Iter.map(Display.to_string) |> String.join(",")
}
fn main() {
  shapes: List<Shape> = [one(), Shape.Dot]
  io.print(show(shapes))
  io.print(show(%[one()]))
  io.print(show(Iter.map(shapes, fn s -> s)))
}`, "shape 1,dot\nshape 1\nshape 1,dot\n")
}

// An `embeds` enum with a hand-written `impl Debug`, held boxed as a
// program interface's existential or as Debug's, renders through its own
// impl, also for a value widened into its `embeds` variant and inside a
// list: the box names the enum, and the renderer offers the box to the impl
// recorded under that name first. A Circle held as itself renders through
// Circle's impl.
func TestIRBoxedExistential_DebugOfEmbedsEnumWithItsOwnDebug(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

interface Says {
    fn says(s: self) -> String
}

struct Circle {
    r: Int
}

impl Debug for Circle {
    fn inspect(c: Circle) -> String {
        "circle ${c.r}"
    }
}

impl Says for Circle {
    fn says(_c: Circle) -> String {
        "circle"
    }
}

enum Shape {
    embeds Circle
    Dot
}

impl Says for Shape {
    fn says(_s: Shape) -> String {
        "shape"
    }
}

impl Debug for Shape {
    fn inspect(s: Shape) -> String {
        case s {
            #.Circle{r} -> "a shape of radius ${r}"
            .Dot -> "a dot"
        }
    }
}

fn shape(r: Int) -> Shape {
    #Circle{r}
}

fn main() {
    s: Says = shape(4)
    io.print(Debug.inspect(s))
    d: Says = Shape.Dot
    io.print(Debug.inspect(d))
    c: Says = #Circle{r: 2}
    io.print(Debug.inspect(c))
    io.print(Debug.inspect([s, d, c]))
    g: Debug = shape(6)
    io.print(Debug.inspect(g))
    io.inspect(s)
    dbg(s)
}`, "a shape of radius 4\na dot\ncircle 2\n[a shape of radius 4, a dot, circle 2]\n"+
		"a shape of radius 6\na shape of radius 4\ndbg line 58: s = a shape of radius 4\n")
}
