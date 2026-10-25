package ffirun

import (
	"fmt"
	"strings"
	"testing"
)

const mapOrderGo = `package crossing

type Pt struct {
	X int64
	Y int64
}

// Many answers 50 keys, inserted in an order that is not theirs.
func Many() map[int64]int64 {
	m := map[int64]int64{}
	for i := int64(1); i <= 50; i++ {
		k := i * 37 % 101
		m[k] = k * 2
	}
	return m
}

func Grid() map[Pt]int64 {
	return map[Pt]int64{{X: 2, Y: 1}: 3, {X: 1, Y: 2}: 2, {X: 1, Y: 1}: 1, {X: 2, Y: 0}: 4, {X: 0, Y: 9}: 0}
}

func Flags() map[bool]string { return map[bool]string{true: "t", false: "f"} }
`

// A Go map crosses to Nomi with its entries in ascending key order. A Nomi
// Map keeps the order its entries were inserted in, so an adapter that
// inserted in Go's iteration order, which is random, gave one program
// different values from call to call. Each crossing runs five times in one
// process, and the test is meant to pass under -count=10.
func TestFFIRun_AGoMapCrossesInSortedKeyOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test invokes go run; skipped in -short mode")
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	entry := stageCrossingProject(t, mapOrderGo, `import std/io

gopkg "crossing" as crossing

struct Pt {
  x: Int
  y: Int
}

fn many() -> Map<Int, Int> go crossing.Many

fn grid() -> Map<Pt, Int> go crossing.Grid

fn flags() -> Map<Bool, String> go crossing.Flags

fn show() {
  io.inspect(many())
  io.inspect(grid())
  io.inspect(flags())
}

fn main() {
  show()
  show()
  show()
  show()
  show()
}
`)
	got := runCrossingProject(t, entry)

	var many []string
	for k := int64(0); k <= 100; k++ {
		for i := int64(1); i <= 50; i++ {
			if i*37%101 == k {
				many = append(many, fmt.Sprintf("%d => %d", k, k*2))
			}
		}
	}
	round := strings.Join([]string{
		"%{" + strings.Join(many, ", ") + "}",
		`%{#Pt{x: 0, y: 9} => 0, #Pt{x: 1, y: 1} => 1, #Pt{x: 1, y: 2} => 2, #Pt{x: 2, y: 0} => 4, #Pt{x: 2, y: 1} => 3}`,
		`%{False => "f", True => "t"}`,
	}, "\n")
	want := strings.Repeat(round+"\n", 5)
	want = strings.TrimSuffix(want, "\n")
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
