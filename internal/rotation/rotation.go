// Package rotation picks the inputs of a test's rotating set. A harness
// that checks generated or sampled inputs keeps a fixed set as its
// regression baseline, and checks a rotating set beside it: inputs from a
// start seed that changes every UTC day, so each day checks different ones.
//
// NOMI_GEN_SEED=<n> pins the start, which makes a run repeatable, and
// NOMI_GEN_COUNT=<n> sets how many seeds the set holds. What seed start+k
// builds depends on that seed alone, so NOMI_GEN_SEED=<seed>
// NOMI_GEN_COUNT=1 checks exactly what a failure names. A harness whose one
// seed builds an input per file (a layout variant of each seed file) also
// names the input, and NOMI_GEN_INPUT=<name> checks only that one.
package rotation

import (
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"
)

// dayStride separates two days' start seeds, so consecutive days' sets of
// up to dayStride seeds never overlap. The first day's start is far above
// any fixed set's seeds.
const dayStride = 1000

// Set is one test's rotating set: Count seeds from Start.
type Set struct {
	Start int64
	Count int
	// Pinned reports that NOMI_GEN_SEED chose Start.
	Pinned bool
	// test is the -run pattern and pkg the package path that reproduce an
	// input.
	test, pkg string
}

// For is the rotating set of the test tb, of count seeds unless
// NOMI_GEN_COUNT says otherwise. pkg is the package's path for the
// reproducing command (`./vmhost`). It logs the start seed.
func For(tb testing.TB, pkg string, count int) Set {
	tb.Helper()
	s := Set{Count: count, test: "^" + tb.Name() + "$", pkg: pkg}
	if v := os.Getenv("NOMI_GEN_SEED"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			tb.Fatalf("NOMI_GEN_SEED=%q, want a seed >= 0", v)
		}
		s.Start, s.Pinned = n, true
	} else {
		s.Start = TodayStart(time.Now())
	}
	if v := os.Getenv("NOMI_GEN_COUNT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			tb.Fatalf("NOMI_GEN_COUNT=%q, want a count >= 1", v)
		}
		s.Count = n
	}
	how := "today's (UTC) start"
	if s.Pinned {
		how = "pinned by NOMI_GEN_SEED"
	}
	tb.Logf("rotating set: %d seeds from %d, %s; repeat it with NOMI_GEN_SEED=%d go test %s -run '%s' -count=1",
		s.Count, s.Start, how, s.Start, s.pkg, s.test)
	return s
}

// TodayStart is the start seed of the UTC day holding now.
func TodayStart(now time.Time) int64 {
	day := now.UTC().Unix() / (24 * 60 * 60)
	return dayStride * (day + 1)
}

// Seeds are the set's seeds, in order.
func (s Set) Seeds() []int64 {
	out := make([]int64, s.Count)
	for k := range out {
		out[k] = s.Start + int64(k)
	}
	return out
}

// Failure is the text a failure of what seed built starts with: where it
// came from, why it may not be the change under test's fault, and the
// command that checks exactly that input.
func (s Set) Failure(seed int64) string { return s.FailureOf(seed, "") }

// FailureOf is Failure for the input named input among those seed builds.
func (s Set) FailureOf(seed int64, input string) string {
	env := fmt.Sprintf("NOMI_GEN_SEED=%d NOMI_GEN_COUNT=1", seed)
	if input != "" {
		env += fmt.Sprintf(" NOMI_GEN_INPUT='%s'", input)
	}
	return fmt.Sprintf("rotating set, seed %d: these inputs change every day, so this may be a bug that was "+
		"already there and today's inputs found, not one the change under test made.\n"+
		"Reproduce exactly this input with:\n\t%s go test %s -run '%s' -count=1 -v\n",
		seed, env, s.pkg, s.test)
}

// Wants reports whether the set checks the input named input: every input,
// unless NOMI_GEN_INPUT names one.
func Wants(input string) bool {
	only := os.Getenv("NOMI_GEN_INPUT")
	return only == "" || only == input
}
