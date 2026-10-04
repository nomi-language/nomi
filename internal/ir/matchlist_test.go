package ir

import "testing"

func TestMatchList_AnswersAndInvalidBounds(t *testing.T) {
	at := At("list.nomi", 1, 1)
	for _, m := range []*Match{NewMatchListLenInto(at, 2, 1, 0), NewMatchListMinInto(at, 2, 1, 1)} {
		if !m.Answers() || m.Dst() != 2 || m.Subject() != 1 {
			t.Fatal(m)
		}
		if uses := m.AppendUses(nil); len(uses) != 1 || uses[0] != 1 {
			t.Fatal(uses)
		}
	}
	for _, makeMatch := range []func(){
		func() { NewMatchListLenInto(at, NoTemp, 1, 0) },
		func() { NewMatchListMinInto(at, NoTemp, 1, 1) },
		func() { NewMatchListLenInto(at, 2, 1, -1) },
		func() { NewMatchListMinInto(at, 2, 1, 0) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("invalid list predicate accepted")
				}
			}()
			makeMatch()
		}()
	}
}
