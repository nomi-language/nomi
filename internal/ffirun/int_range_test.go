package ffirun

import (
	"path/filepath"
	"strings"
	"testing"
)

const intRangeGo = `package crossing

import "time"

type Row struct{ N int64 }

func Inc(x int64) int64                    { return x + 1 }
func Inc32(x int32) int32                  { return x + 1 }
func IncU(x uint64) uint64                 { return x + 1 }
func Sum(xs []int64) int64                 { return 0 }
func Bump(r Row) Row                       { return r }
func Apply(f func(int64) int64) int64      { return f(1) }
func Wait(d time.Duration) time.Duration   { return d }
`

// An Int position bound to a fixed-width Go integer declares a type whose
// every value the Go integer holds: std's Int64, an intersection with it,
// or a subtype whose rule bounds it, declared in the binding's file or one
// it imports. A wider type is an error at the binding, naming what to
// declare (spec §19 *How Nomi types project to Go*).
func TestPrepareHoldsAnIntToItsGoIntegersRange(t *testing.T) {
	t.Setenv(cacheRootEnv, t.TempDir())
	entry := stageCrossingProject(t, intRangeGo, `import
  std::duration.Duration
  std::int.Int64
  units.{Small}
end

gopkg "crossing" as crossing

subtype Count NonNegativeInt and Int64

struct Row
  n: Int64
end

fn inc(x: Int64) -> Int go crossing.Inc
fn inc_count(x: Count) -> Count go crossing.Inc
fn inc_small(x: Small) -> Int go crossing.Inc32
fn inc_unsigned(x: NonNegativeInt and Int64) -> Int go crossing.IncU
fn sum(xs: List<Int64>) -> Int go crossing.Sum
fn bump(r: Row) -> Row go crossing.Bump
fn apply(f: (Int) -> Int64) -> Int go crossing.Apply
fn wait(d: Duration) -> Duration go crossing.Wait
`)
	mustWriteHelper(t, filepath.Join(filepath.Dir(entry), "units.nomi"),
		"pub subtype Small Int when fn n -> n >= -100 and n <= 100\n")
	if _, err := Prepare(entry); err != nil {
		t.Fatalf("Prepare refuses Int positions whose declared range fits their Go integer: %v", err)
	}

	entry = stageCrossingProject(t, intRangeGo, `import
  std::duration.Duration
  std::int.Int64
end

gopkg "crossing" as crossing

struct Row
  n: Int
end

fn inc(x: Int) -> Int go crossing.Inc
fn inc32(x: Int64) -> Int go crossing.Inc32
fn inc_unsigned(x: Int64) -> Int go crossing.IncU
fn sum(xs: List<Int>) -> Int go crossing.Sum
fn bump(r: Row) -> Row go crossing.Bump
fn apply(f: (Int) -> Int) -> Int go crossing.Apply
fn inc_duration(d: Duration) -> Int go crossing.Inc
`)
	_, err := Prepare(entry)
	if err == nil {
		t.Fatal("Prepare accepts Int positions whose declared type admits values past their Go integer")
	}
	for _, want := range []string{
		"main.nomi:12:35: Go function \"Inc\" in package \"crossing\": parameter x (Int) to Go int64: " +
			"Go int64 holds an Int in -9223372036854775808..9223372036854775807, and Int admits Ints past that: declare it `Int64` (std::int)",
		"parameter x (Int) to Go int32: Go int32 holds an Int in -2147483648..2147483647, and Int64 admits Ints past that: " +
			"declare it a subtype whose `when` bounds it within -2147483648..2147483647",
		"parameter x (Int) to Go uint64: Go uint64 holds an Int in 0..18446744073709551615, and Int64 admits Ints past that: " +
			"declare it `NonNegativeInt and Int64` (std::int)",
		"parameter xs (List<Int>) to Go []int64: element: Go int64",
		"parameter r (Row) to Go crossing.Row: field n: Go int64",
		"parameter f ((Int) -> Int) to Go func(int64) int64: callback: result: Go int64",
		"parameter d (Duration) to Go int64: Go int64 holds",
		"a Go function that takes an Int of any size takes *big.Int",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not say %q:\n%s", want, err)
		}
	}
}
