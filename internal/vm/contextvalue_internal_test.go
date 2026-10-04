package vm

import (
	"sync"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// TestContextValue_OneIdentityPerName pins what makes a witness written in one
// module find a value filed from another: one name, one *rt.TypeID, and a
// primitive shares rt's canonical identity.
func TestContextValue_OneIdentityPerName(t *testing.T) {
	var wg sync.WaitGroup
	ids := make([]*rt.TypeID, 16)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ids[i] = typeID("domain.Token")
		}(i)
	}
	wg.Wait()
	for _, id := range ids[1:] {
		if id != ids[0] {
			t.Fatal("one name answered two identities")
		}
	}
	if typeID("domain.Token") == typeID("other.Token") {
		t.Fatal("two same-named types in two modules share a slot")
	}
	if typeID("Int") != &rt.TIDInt {
		t.Fatal("Int's witness is not rt's canonical identity")
	}
}

// TestContextValue_HostsShadowAndLeaveTheParent drives the two intrinsics:
// a child binding shadows its parent for that type only, the parent is
// unchanged, and an unbound type answers None.
func TestContextValue_HostsShadowAndLeaveTheParent(t *testing.T) {
	root := rt.ContextRoot()
	token := typeWitness("domain.Token")
	parent, err := contextWithValueHost(nil, ir.Pos{}, []any{root, int64(1), token})
	if err != nil {
		t.Fatal(err)
	}
	child, err := contextWithValueHost(nil, ir.Pos{}, []any{parent, int64(2), token})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		ctx  any
		w    rt.Type[any]
		want any
	}{
		{child, token, some(int64(2))},
		{parent, token, some(int64(1))},
		{root, token, noneValue},
		{child, typeWitness("other.Token"), noneValue},
	} {
		got, err := contextValueHost(nil, ir.Pos{}, []any{c.ctx, c.w})
		if err != nil {
			t.Fatal(err)
		}
		if !rt.Equal(got, c.want) {
			t.Errorf("Context.value(%s) = %s, want %s", c.w.TID.Nomi, rt.RowText(got), rt.RowText(c.want))
		}
	}
	if _, err := contextValueHost(nil, ir.Pos{}, []any{child, "not a witness"}); err == nil {
		t.Error("a non-witness operand was accepted")
	}
}
