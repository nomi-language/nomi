package ffirun

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stageCrossingProject writes a project whose own Go package (crossing,
// imported as `crossing`) holds goSrc, with main.nomi as the entry.
func stageCrossingProject(t *testing.T, goSrc, entrySrc string) string {
	t.Helper()
	root := t.TempDir()
	mustWriteHelper(t, filepath.Join(root, "go.mod"), fmt.Sprintf(`module crossing

go 1.26.3

require github.com/nomi-language/nomi v0.0.0

replace github.com/nomi-language/nomi => %s
`, repoRoot(t)))
	mustWriteHelper(t, filepath.Join(root, "nomi.toml"), `[module]
name = "crossing"
entry_points = ["main"]
`)
	mustWriteHelper(t, filepath.Join(root, "crossing.go"), goSrc)
	entry := filepath.Join(root, "main.nomi")
	mustWriteHelper(t, entry, entrySrc)
	return entry
}

// runCrossingProject prepares entry's wrapper and runs its main.
func runCrossingProject(t *testing.T, entry string) string {
	t.Helper()
	res, err := Prepare(entry)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	cmd := exec.Command("go", "run", "-mod=mod", res.WrapperPath, wrapperModeFlag, "vmrun", entry)
	cmd.Dir = res.WrapperDir
	userCwd, _ := os.Getwd()
	cmd.Env = append(os.Environ(), "NOMI_FFIRUN_USER_CWD="+userCwd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go run wrapper: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

const distinctGo = `package crossing

import "time"

type Row struct {
	Name string
	N    int64
}

func Inc(x int64) int64                         { return x + 1 }
func Inc32(x int32) int32                       { return x + 1 }
func Shout(s string) string                     { return s + "!" }
func Twice(f float64) float64                   { return f * 2 }
func Append9(xs []int64) []int64                { return append(xs, 9) }
func IncMaybe(p *int64) *int64                  { if p == nil { return nil }; y := *p + 1; return &y }
func Tenfold(x int64) (int64, error)            { return x * 10, nil }
func Pair(x int64) (int64, string)              { return x, "p" }
func Apply(x int64, f func(int64) int64) int64  { return f(x) + 100 }
func Bump(r Row) Row                            { r.N++; return r }
func Rows(rs []Row) []Row                       { return append(rs, Row{Name: "go", N: 7}) }
func Double(d time.Duration) time.Duration      { return d * 2 }
func IncAll(m map[string]int64) map[string]int64 { m["b"] = m["a"] + 1; return m }
`

// A distinct type crosses as its inner type does, at every depth: the
// validator accepts it where the inner type's projection matches, and the
// adapters convert it both ways, over a scalar, a List, a struct and
// another distinct.
func TestFFIRun_DistinctTypesRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test invokes go run; skipped in -short mode")
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	entry := stageCrossingProject(t, distinctGo, `import {
  std/io
  std/duration.Duration
  std/instant.Instant
}

gopkg "crossing" as crossing

type Id Int

type Name String

type Ratio Float

type Small Int

type Ids List<Int>

type Inner Int

type Outer Inner

type Timeout Duration

struct Row {
  name: String
  n: Id
}

struct Plain {
  name: String
  n: Int
}

type Wrapped Plain

fn inc(x: Id) -> Id go crossing.Inc

fn inc32(x: Small) -> Small go crossing.Inc32

fn shout(s: Name) -> Name go crossing.Shout

fn twice(f: Ratio) -> Ratio go crossing.Twice

fn append9(xs: List<Id>) -> List<Id> go crossing.Append9

fn append9_ids(xs: Ids) -> Ids go crossing.Append9

fn inc_maybe(x: Maybe<Id>) -> Maybe<Id> go crossing.IncMaybe

fn tenfold(x: Id) -> Result<Id, String> go crossing.Tenfold

fn pair(x: Id) -> #(Id, String) go crossing.Pair

fn apply(x: Id, f: (Id) -> Id) -> Id go crossing.Apply

fn bump(r: Row) -> Row go crossing.Bump

fn bump_wrapped(r: Wrapped) -> Wrapped go crossing.Bump

fn rows(rs: List<Wrapped>) -> List<Wrapped> go crossing.Rows

fn inc_all(m: Map<String, Id>) -> Map<String, Id> go crossing.IncAll

fn inc_outer(x: Outer) -> Outer go crossing.Inc

fn double(d: Timeout) -> Timeout go crossing.Double

fn inc_timeout(d: Timeout) -> Timeout go crossing.Inc

fn inc_duration(d: Duration) -> Duration go crossing.Inc

fn inc_instant(i: Instant) -> Instant go crossing.Inc

fn main() {
  io.inspect(inc(Id(5)))
  io.inspect(inc32(Small(5)))
  io.inspect(shout(Name("a")))
  io.inspect(twice(Ratio(1.5)))
  io.inspect(append9([Id(1), Id(2)]))
  io.inspect(append9_ids(Ids([1, 2])))
  io.inspect(inc_maybe(Some(Id(1))))
  io.inspect(inc_maybe(None))
  io.inspect(tenfold(Id(4)))
  io.inspect(pair(Id(4)))
  io.inspect(apply(Id(1), fn Id(k) -> Id(k * 2)))
  io.inspect(bump(#Row{name: "a", n: Id(1)}))
  io.inspect(bump_wrapped(Wrapped(#Plain{name: "w", n: 1})))
  io.inspect(rows([Wrapped(#Plain{name: "n", n: 0})]))
  io.inspect(inc_all(%{"a" => Id(1)}))
  io.inspect(inc_outer(Outer(Inner(5))))
  io.inspect(double(Timeout(Duration.seconds(1))))
  io.inspect(inc_timeout(Timeout(Duration.seconds(1))))
  io.inspect(inc_duration(Duration.seconds(1)))
  io.inspect(inc_instant(Instant.from_nanos(5)) |> Instant.as_nanos())
}
`)
	got := runCrossingProject(t, entry)
	want := strings.Join([]string{
		`Id(6)`,
		`Small(6)`,
		`Name("a!")`,
		`Ratio(3.0)`,
		`[Id(1), Id(2), Id(9)]`,
		`Ids([1, 2, 9])`,
		`Some(Id(2))`,
		`None`,
		`Ok(Id(40))`,
		`#(Id(4), "p")`,
		`Id(102)`,
		`#Row{name: "a", n: Id(2)}`,
		`Wrapped(#Plain{name: "w", n: 2})`,
		`[Wrapped(#Plain{name: "n", n: 0}), Wrapped(#Plain{name: "go", n: 7})]`,
		`%{"a" => Id(1), "b" => Id(2)}`,
		`Outer(Inner(6))`,
		`Timeout(2s)`,
		`Timeout(1s1ns)`,
		`1s1ns`,
		`6`,
	}, "\n")
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

const dynamicGo = `package crossing

import (
	"fmt"

	"github.com/nomi-language/nomi/rt"
)

type Any interface{}

func Describe(x any) string                              { return fmt.Sprintf("%T %v", x, x) }
func Echo(x any) any                                     { return x }
func Seven() any                                         { return int64(7) }
func EchoNamed(x Any) Any                                { return x }
func EchoEmpty(x interface{}) interface{}                { return x }
func EchoRt(d rt.Dynamic) rt.Dynamic                     { return d }
func Built() rt.Dynamic                                  { return rt.Dynamic{Inner: map[string]any{"k": []any{int64(1), "x"}}} }
func Tail(xs []any) []any                                { return append(xs, "tail") }
func Keyed(m map[string]any) map[string]any              { m["k"] = int64(3); return m }
func Apply(x any, f func(any) any) any                   { return f(x) }
func Checked(x any) (any, error)                         { return x, nil }
`

// A Dynamic crosses as a Go empty interface, which holds the value the
// Dynamic wraps, or as rt.Dynamic itself: at a parameter, a result, in a
// List, a Map, a Result and a callback.
func TestFFIRun_DynamicRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test invokes go run; skipped in -short mode")
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	entry := stageCrossingProject(t, dynamicGo, `import {
  std/io
  std/json.Json
  std/dynamic.Dynamic
}

gopkg "crossing" as crossing

fn describe(x: Dynamic) -> String go crossing.Describe

fn echo(x: Dynamic) -> Dynamic go crossing.Echo

fn seven() -> Dynamic go crossing.Seven

fn echo_named(x: Dynamic) -> Dynamic go crossing.EchoNamed

fn echo_empty(x: Dynamic) -> Dynamic go crossing.EchoEmpty

fn echo_rt(x: Dynamic) -> Dynamic go crossing.EchoRt

fn built() -> Dynamic go crossing.Built

fn tail(xs: List<Dynamic>) -> List<Dynamic> go crossing.Tail

fn keyed(m: Map<String, Dynamic>) -> Map<String, Dynamic> go crossing.Keyed

fn apply(x: Dynamic, f: (Dynamic) -> Dynamic) -> Dynamic go crossing.Apply

fn checked(x: Dynamic) -> Result<Dynamic, String> go crossing.Checked

fn main() {
  Ok(json) = Json.decode("{\"a\": [1, \"x\", null], \"n\": 2.5}") else {
    return
  }
  d = Json.to_dynamic(json)
  io.print(describe(d))
  io.inspect(echo(d) |> Dynamic.path("a") |> Result.flat_map(fn l -> Dynamic.index(l, 1)) |> Result.flat_map(Dynamic.as_string))
  io.inspect(seven() |> Dynamic.as_int())
  io.inspect(echo_named(d) |> Dynamic.field("n") |> Result.flat_map(Dynamic.as_float))
  io.inspect(echo_empty(d) |> Dynamic.has("a"))
  io.inspect(echo_rt(d) |> Dynamic.field("n") |> Result.flat_map(Dynamic.as_float))
  io.inspect(built() |> Dynamic.path("k") |> Result.flat_map(fn l -> Dynamic.index(l, 1)) |> Result.flat_map(Dynamic.as_string))
  io.inspect(tail([seven()]) |> Iter.map(Dynamic.as_string) |> Iter.to_list())
  io.inspect(keyed(%{"a" => d}) |> Map.get("k") |> Maybe.map(Dynamic.as_int))
  io.inspect(apply(d, fn x -> Dynamic.field(x, "n") |> Result.with_default(x)) |> Dynamic.as_float())
  io.inspect(checked(seven()) |> Result.flat_map(fn x -> Dynamic.as_int(x) |> Result.map_err(fn _ -> "")))
}
`)
	got := runCrossingProject(t, entry)
	want := strings.Join([]string{
		`map[string]interface {} map[a:[1 x <nil>] n:2.5]`,
		`Ok("x")`,
		`Ok(7)`,
		`Ok(2.5)`,
		`True`,
		`Ok(2.5)`,
		`Ok("x")`,
		`[Err(#Error{path: [], expected: "String", got: "Int"}), Ok("tail")]`,
		`Some(Ok(3))`,
		`Ok(2.5)`,
		`Ok(7)`,
	}, "\n")
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

const refusedGo = `package crossing

type Row struct {
	Name string
	N    int64
}

func Inc(x int64) int64             { return x + 1 }
func Sum(xs []int64) int64          { return 0 }
func Bump(r Row) Row                { return r }
func Apply(f func(int64) int64) int64 { return f(1) }
`

// What does not cross is refused at load: Dynamic against a Go type that is
// no empty interface, a distinct whose inner type does not project to the
// Go type, a marker, and a distinct over a struct whose fields differ.
func TestPrepareRefusesDistinctsAndDynamicThatDoNotCross(t *testing.T) {
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	entry := stageCrossingProject(t, refusedGo, `import {
  std/dynamic.Dynamic
}

gopkg "crossing" as crossing

type Name String

type Expired

struct Thin {
  name: String
}

type Wrapped Thin

struct Holder {
  name: String
  n: Dynamic
}

fn dyn_inc(x: Dynamic) -> Dynamic go crossing.Inc

fn dyn_sum(xs: List<Dynamic>) -> Int go crossing.Sum

fn dyn_apply(f: (Dynamic) -> Int) -> Int go crossing.Apply

fn name_inc(x: Name) -> Int go crossing.Inc

fn expired_inc(x: Expired) -> Int go crossing.Inc

fn bump_wrapped(r: Wrapped) -> Wrapped go crossing.Bump

fn bump_holder(r: Holder) -> Int go crossing.Bump
`)
	_, err := Prepare(entry)
	if err == nil {
		t.Fatal("Prepare accepts bindings whose distinct or Dynamic types do not cross")
	}
	msg := err.Error()
	hint := "; Dynamic crosses only as a Go empty interface (any) or rt.Dynamic, so declare the type the Go value has"
	for _, want := range []string{
		`"Inc" in package "crossing": parameter 1 "x" projects to Int, but Nomi declares Dynamic` + hint,
		`"Inc" in package "crossing": return projects to Int, but Nomi declares Dynamic` + hint,
		`"Sum" in package "crossing": parameter 1 "xs" projects to List<Int>, but Nomi declares List<Dynamic>` + hint,
		`"Apply" in package "crossing": parameter 1 "f" projects to (Int) -> Int, but Nomi declares (Dynamic) -> Int` + hint,
		`"Inc" in package "crossing": parameter 1 "x" projects to Int, but Nomi declares Name`,
		`"Inc" in package "crossing": parameter 1 "x" projects to Int, but Nomi declares Expired`,
		`"Bump" in package "crossing": parameter 1 "r" struct field "n" exists in Go Row but not in Nomi Thin`,
		`"Bump" in package "crossing": return struct field "n" exists in Go Row but not in Nomi Thin`,
		`"Bump" in package "crossing": parameter 1 "r" struct field "n" projects to Int, but Nomi Holder declares Dynamic` + hint,
		`"Bump" in package "crossing": return projects to a struct (Go Row), but Nomi declares Int`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the error does not say %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, `Nomi declares Name`+hint) {
		t.Errorf("a distinct over String carries the Dynamic hint:\n%s", msg)
	}
}
