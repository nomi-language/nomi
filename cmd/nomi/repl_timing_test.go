package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/vmhost"
)

// replInputCost evaluates input in s and answers what its checks and
// lowerings cost, and what it wrote to stderr.
func replInputCost(s *replSession, input string) (vmhost.SessionCost, string) {
	var errOut strings.Builder
	before := s.vm.Cost()
	s.eval(input, io.Discard, &errOut)
	after := s.vm.Cost()
	return vmhost.SessionCost{Declarations: after.Declarations - before.Declarations,
		Functions: after.Functions - before.Functions}, errOut.String()
}

// An input costs what its own text costs, however long the session: the
// hundredth input of a session builds as many declarations in its checks
// and makes as many functions in its lowering as an input of the same
// shape early in the session did. Before each input was a file, the
// hundredth input checked and lowered every definition the 99 before it
// made.
func TestReplVM_AnInputsCostDoesNotGrowWithTheSession(t *testing.T) {
	t.Run("independent inputs", func(t *testing.T) {
		s := newReplSession(io.Discard, io.Discard)
		var first, last vmhost.SessionCost
		for i := 1; i <= 100; i++ {
			cost, errs := replInputCost(s, fmt.Sprintf("fn f%d(n: Int) -> Int {\n  n + %d\n}\nv%d = f%d(%d)", i, i, i, i, i))
			if errs != "" {
				t.Fatalf("input %d: %s", i, errs)
			}
			switch i {
			case 1:
				first = cost
			case 100:
				last = cost
			}
		}
		if first.Declarations == 0 || first.Functions == 0 {
			t.Fatalf("the first input cost nothing: %+v", first)
		}
		if last != first {
			t.Errorf("the hundredth input cost %+v, the first %+v", last, first)
		}
	})
	t.Run("inputs that use earlier ones", func(t *testing.T) {
		s := newReplSession(io.Discard, io.Discard)
		if _, errs := replInputCost(s, "struct P {\n  x: Int\n}\nfn base(p: P) -> Int {\n  p.x\n}\nv0 = P{x: 1}"); errs != "" {
			t.Fatal(errs)
		}
		var third, last vmhost.SessionCost
		for i := 1; i <= 99; i++ {
			// Each input reads the first input's type, function and value
			// and the input before it's value.
			cost, errs := replInputCost(s, fmt.Sprintf("fn f%d(p: P) -> Int {\n  base(p) + %d\n}\nv%d = P{x: f%d(v0) + v%d.x}", i, i, i, i, i-1))
			if errs != "" {
				t.Fatalf("input %d: %s", i+1, errs)
			}
			switch i {
			case 2:
				third = cost
			case 99:
				last = cost
			}
		}
		if last != third {
			t.Errorf("the hundredth input cost %+v, the third %+v", last, third)
		}
	})
}

// TestReplVM_WallTimePerInput prints the wall time of inputs of a session
// that declares a function and a value per input and calls the first
// input's function: input 1 (which loads the stdlib), and the median of
// the nine inputs up to input 100 and 500. It runs only with
// NOMI_REPL_TIMING set to the number of inputs (500 for the figures in
// docs/implementation-notes.md).
func TestReplVM_WallTimePerInput(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("NOMI_REPL_TIMING"))
	if n == 0 {
		t.Skip("set NOMI_REPL_TIMING to the number of inputs")
	}
	s := newReplSession(io.Discard, io.Discard)
	times := make([]time.Duration, n+1)
	for i := 1; i <= n; i++ {
		input := fmt.Sprintf("fn f%d(n: Int) -> Int {\n  n + %d\n}\nv%d = f%d(f1(%d))", i, i, i, i, i)
		start := time.Now()
		s.eval(input, io.Discard, os.Stderr)
		times[i] = time.Since(start)
	}
	t.Logf("input 1: %v", times[1])
	for _, at := range []int{10, 100, 200, 300, 400, 500} {
		if at > n {
			break
		}
		window := append([]time.Duration(nil), times[at-8:at+1]...)
		sort.Slice(window, func(i, j int) bool { return window[i] < window[j] })
		t.Logf("input %d: %v (median of inputs %d-%d)", at, window[4], at-8, at)
	}
}
