package irbuild

import (
	"fmt"
	"sync/atomic"
	"testing"
)

// Memoized retention predicates.
//
// irRetainedStructKind and irRetainedEnumKind walk a declared type's fields
// and payloads, and through them every type those reach. Body lowering asks
// them about the same few hundred types over and over: lowering a program
// that imports std/http asked about 100,000 times for struct kinds and
// 170,000 times for enum kinds, over about 330 and 390 types, and the walks
// were two thirds of the lowering's time. Each typeDef keeps its last answer
// to each (kindMemo), so a query repeats the walk only when a typeDef may
// have changed since.
//
// A typeDef changes while it is built: its fields, variants and slots are
// resolved and laid out, a mirror copies its source's layout, and
// settleLowerable and a group's registerInsts settle its lowerability. Each
// function that writes those to a typeDef that already exists brackets the
// writes with changingTypes. While any bracket is open the memo is neither
// read nor written, so a predicate asked mid-change walks the half-built
// types exactly as it would without the memo, and opening or closing a
// bracket advances typeEpoch, which invalidates every answer stored before
// it. A function that builds a new typeDef and writes nothing else
// (buildPreludeDef, mirrorShell) opens a constructingType bracket instead,
// which turns the memo off the same way but advances no epoch: no stored
// answer can depend on a def that did not exist when it was stored.
// `mentions` and `components` (typeDef.note) are read by layout alone, never
// by the predicates, and need no bracket.
//
// The epoch and the open count are process-wide, so one program's change
// also invalidates another's answers: that costs a walk, never a wrong
// answer. Answers for the process-wide std defs, which no program changes
// once built, are stored by every program that asks, hence the atomics.
//
// Under `go test`, every answer the memo would return is recomputed and
// compared (kindMemoVerify): a typeDef write outside a bracket fails
// whichever test first reads a stale answer, rather than lowering a program
// against it. TestKindMemo_* hold the memo to its savings and the check to
// its catch.

// typeEpoch advances each time a changingTypes bracket opens or closes.
var typeEpoch atomic.Uint64

// typesChanging counts the brackets open now.
var typesChanging atomic.Int64

// kindMemoVerify recomputes each memoized answer and panics when the two
// differ. It is on under `go test`.
var kindMemoVerify = testing.Testing()

// kindMemoQueries and kindMemoWalks count, under `go test`, the queries the
// memoized predicates answered and the walks they made for them (the check's
// own walks aside), for TestKindMemo_AnswersMostQueriesWithoutAWalk.
var kindMemoQueries, kindMemoWalks atomic.Int64

// changingTypes opens a bracket around writes to existing typeDefs and
// answers the function that closes it: `defer changingTypes()()`.
func changingTypes() func() {
	typesChanging.Add(1)
	typeEpoch.Add(1)
	return func() {
		typeEpoch.Add(1)
		typesChanging.Add(-1)
	}
}

// constructingType opens a bracket around building a typeDef no memo can
// hold yet, one created in the bracket and written by nothing else, and
// answers the function that closes it.
func constructingType() func() {
	typesChanging.Add(1)
	return func() { typesChanging.Add(-1) }
}

// kindMemo is one predicate's last answer for one typeDef: one more than the
// typeEpoch it was computed at, shifted left one bit, and the answer in the
// low bit. Zero is no answer.
type kindMemo struct{ v atomic.Uint64 }

// answer is walk's answer, from the memo when no type has changed since it
// was stored. name and d label the check's panic.
func (m *kindMemo) answer(name string, d *typeDef, walk func() bool) bool {
	if kindMemoVerify {
		kindMemoQueries.Add(1)
	}
	if typesChanging.Load() != 0 {
		if kindMemoVerify {
			kindMemoWalks.Add(1)
		}
		return walk()
	}
	stamp := typeEpoch.Load() + 1
	if v := m.v.Load(); v>>1 == stamp {
		got := v&1 == 1
		if kindMemoVerify {
			if want := walk(); want != got {
				panic(fmt.Sprintf("irbuild: %s(%s) is memoized as %v and walks to %v: a typeDef changed outside a changingTypes bracket", name, d.nomi, got, want))
			}
		}
		return got
	}
	if kindMemoVerify {
		kindMemoWalks.Add(1)
	}
	got := walk()
	if typesChanging.Load() == 0 && typeEpoch.Load()+1 == stamp {
		v := stamp << 1
		if got {
			v |= 1
		}
		m.v.Store(v)
	}
	return got
}
