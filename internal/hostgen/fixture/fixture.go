// Package fixture is a user-shaped FFI binding set for the adapter generator:
// Go functions whose signatures reach every row of the marshaller's projection
// table the stdlib bindings do not (narrow and unsigned integers, float32,
// time.Time, embedded and tagged struct fields, pointer-to-struct Maybe, a
// named byte element, tuples with and without a trailing error, Unit, handles,
// four callback shapes, and Go maps with slice and scalar values, as a
// parameter, a result and a struct field), and the Nomi module `ffx` that
// declares them.
//
// internal/hostgen/fixture/ffxadapters holds the adapters generated from it
// and compares each against the marshaller, registered the way a generated
// FFI wrapper registers a project's bindings.
package fixture

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/nomi-language/nomi/internal/hostgen"
	"path/filepath"
	"runtime"
)

// Source is the Nomi module the bindings implement, loaded as module `ffx`.
const Source = `import std/duration.{Duration}
import std/instant.{Instant}

pub struct Point {
  x: Int
  y: Int
}

pub struct Tagged {
  label: String
  x: Int
  y: Int
  small: Int
  wide: Int
  ratio: Float
  at: Instant
  wait: Duration
  raw: Bytes
  tags: List<String>
  near: Maybe<Point>
}

pub host type Box

pub host fn echo(t: Tagged): Tagged
pub host fn narrow(a: Int, b: Int, c: Int, d: Int, e: Int, f: Int, g: Int, h: Int): Int
pub host fn biggest(): Int
pub host fn halve(f: Float): Float
pub host fn later(t: Instant, by: Duration): Instant
pub host fn zero_time(): Instant
pub host fn mirror(p: Maybe<Point>): Maybe<Point>
pub host fn octets(b: Bytes): Bytes
pub host fn flip(b: Byte): Byte
pub host fn shout(s: String): String
pub host fn negate(b: Bool): Bool
pub host fn triple(n: Int): (Int, String, Bool)
pub host fn triple_or_fail(n: Int): Result<(Int, String), String>
pub host fn nothing(): Unit
pub host fn nothing_or_fail(fail: Bool): Result<Unit, String>
pub host fn points(n: Int): List<Point>
pub host fn new_box(n: Int): Box
pub host fn box_value(b: Box): Int
pub host fn apply(f: (Int) -> Int, x: Int): Int
pub host fn check(f: (Point) -> Bool, p: Point): Bool
pub host fn attempt(f: (String) -> Result<Int, String>, s: String): Result<Int, String>
pub host fn run(f: () -> Result<Unit, String>): Result<Unit, String>
pub host fn visit(f: (Int) -> Unit, n: Int): Unit

pub struct Query {
  path: String
  params: Map<String, List<String>>
}

pub host fn query(path: String, words: List<String>): Query
pub host fn sizes(m: Map<String, List<String>>): Map<String, Int>
`

// Point is `ffx.Point`.
type Point struct {
	X int64
	Y int64
}

// Tagged reaches every struct-field rule: a tag rename, a skipped field, an
// embedded struct flattened into its parent, and one field per scalar width.
type Tagged struct {
	Name  string `nomi:"label"`
	Skip  int    `nomi:"-"`
	Point        // flattened: x, y
	Small int8
	Wide  uint32
	Ratio float32
	When  time.Time `nomi:"at"`
	Wait  time.Duration
	Raw   []byte
	Tags  []string
	Near  *Point
	hide  int // unexported, so the marshaller skips it
}

// MyByte is a named byte, which the marshaller copies element by element.
type MyByte uint8

// Label is a named string.
type Label string

// Box is an opaque handle.
type Box struct{ n int64 }

func Echo(t Tagged) Tagged {
	t.Name += "!"
	t.Small = -t.Small
	t.Wide++
	t.Ratio *= 2
	t.When = t.When.Add(t.Wait)
	t.Tags = append(t.Tags, "echoed")
	if t.Near != nil {
		p := *t.Near
		p.X++
		t.Near = &p
	}
	return t
}

func Narrow(a int8, b int16, c int32, d int, e uint, f uint16, g uint32, h uint64) int64 {
	return int64(a) + int64(b) + int64(c) + int64(d) + int64(e) + int64(f) + int64(g) + int64(h)
}

func Biggest() uint64 { return ^uint64(0) }

func Halve(f float32) float32 { return f / 2 }

func Later(t time.Time, by time.Duration) time.Time { return t.Add(by) }

func ZeroTime() time.Time { return time.Time{} }

func Mirror(p *Point) *Point {
	if p == nil {
		return nil
	}
	return &Point{X: p.Y, Y: p.X}
}

func Octets(b []MyByte) []MyByte {
	out := make([]MyByte, len(b))
	for i, x := range b {
		out[len(b)-1-i] = x + 1
	}
	return out
}

func Flip(b uint8) uint8 { return ^b }

func Shout(s Label) Label { return s + "!" }

func Negate(b bool) bool { return !b }

func Triple(n int64) (int64, string, bool) { return n * 3, fmt.Sprint(n), n%2 == 0 }

func TripleOrFail(n int64) (int64, string, error) {
	if n < 0 {
		return 0, "", errors.New("negative")
	}
	return n * 3, fmt.Sprint(n), nil
}

func Nothing() {}

func NothingOrFail(fail bool) error {
	if fail {
		return errors.New("asked to fail")
	}
	return nil
}

func Points(n int64) []Point {
	out := make([]Point, n)
	for i := range out {
		out[i] = Point{X: int64(i), Y: int64(i * i)}
	}
	return out
}

func NewBox(n int64) *Box { return &Box{n: n} }

func BoxValue(b *Box) int64 { return b.n }

func Apply(f func(int64) int64, x int64) int64 { return f(f(x)) }

func Check(f func(Point) bool, p Point) bool { return f(p) && f(Point{X: p.Y, Y: p.X}) }

func Attempt(f func(string) (int64, error), s string) (int64, error) {
	n, err := f(s)
	if err != nil {
		return 0, fmt.Errorf("attempt: %w", err)
	}
	return n + 1, nil
}

func Run(f func() error) error { return f() }

// Query is `ffx.Query`: a Go map whose values are slices, as a struct field.
type Query struct {
	Path   string
	Params map[string][]string
}

// MakeQuery groups words by their first letter.
func MakeQuery(path string, words []string) Query {
	params := map[string][]string{}
	for _, w := range words {
		if w == "" {
			continue
		}
		params[w[:1]] = append(params[w[:1]], w)
	}
	return Query{Path: path, Params: params}
}

// Sizes takes a Go map whose values are slices and answers one whose values
// are scalars.
func Sizes(m map[string][]string) map[string]int64 {
	out := make(map[string]int64, len(m))
	for k, v := range m {
		out[k] = int64(len(v))
	}
	return out
}

func Visit(f func(int64), n int64) {
	for i := range n {
		f(i)
	}
}

// Funcs is the binding table, keyed as a module's top-level `host fn` is
// registered: `<module>.<name>`.
func Funcs() map[string]any {
	return map[string]any{
		"ffx.echo":            Echo,
		"ffx.narrow":          Narrow,
		"ffx.biggest":         Biggest,
		"ffx.halve":           Halve,
		"ffx.later":           Later,
		"ffx.zero_time":       ZeroTime,
		"ffx.mirror":          Mirror,
		"ffx.octets":          Octets,
		"ffx.flip":            Flip,
		"ffx.shout":           Shout,
		"ffx.negate":          Negate,
		"ffx.triple":          Triple,
		"ffx.triple_or_fail":  TripleOrFail,
		"ffx.nothing":         Nothing,
		"ffx.nothing_or_fail": NothingOrFail,
		"ffx.points":          Points,
		"ffx.new_box":         NewBox,
		"ffx.box_value":       BoxValue,
		"ffx.apply":           Apply,
		"ffx.check":           Check,
		"ffx.attempt":         Attempt,
		"ffx.run":             Run,
		"ffx.visit":           Visit,
		"ffx.query":           MakeQuery,
		"ffx.sizes":           Sizes,
	}
}

// BoxPrototype is the registered host type's prototype.
var BoxPrototype = (*Box)(nil)

// Table is the generator input for ffxadapters: the rows above, and the `ffx`
// module beside the stdlib it imports from.
func Table() hostgen.Table {
	tb := hostgen.Table{
		Package: "ffxadapters",
		Header:  "// Source: internal/hostgen/fixture. Regenerate with `go generate ./internal/hostgen/fixture/ffxadapters`.\n",
		Source: func(module string) ([]byte, bool) {
			if module == "ffx" {
				return []byte(Source), true
			}
			return stdSource(module)
		},
		Types:    []hostgen.TypeRow{{Name: "ffx.Box", Prototype: BoxPrototype}},
		ModuleOf: func(hostgen.FuncRow) (string, error) { return "ffx", nil },
	}
	funcs := Funcs()
	names := make([]string, 0, len(funcs))
	for n := range funcs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		tb.Funcs = append(tb.Funcs, hostgen.FuncRow{Name: n, Fn: funcs[n]})
	}
	return tb
}

// stdSource reads the standard library from the source tree beside this
// file; the fixture may not import nomi/std, because internal/ffirun's tests
// read it and nomi/std imports internal/ffirun through the front end.
var stdSource = func() func(string) ([]byte, bool) {
	_, file, _, _ := runtime.Caller(0)
	return hostgen.DirSource(filepath.Join(filepath.Dir(file), "..", "..", "..", "std"))
}()
