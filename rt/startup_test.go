package rt

import (
	"context"
	"reflect"
	"testing"
)

func TestStartupCopiesInputs(t *testing.T) {
	env, args := []string{"MODE=test", "TOKEN=a=b"}, []string{"first"}
	s := NewStartup(env, args)
	env[0], args[0] = "MODE=changed", "changed"
	if s.Args.Head != "first" {
		t.Fatal("arguments were not copied")
	}
	entries := MapEntries(s.Env)
	if len(entries) != 2 || entries[0].Val != "test" || entries[1].Val != "a=b" {
		t.Fatalf("environment: %#v", entries)
	}
}

func TestBootCleanupAttemptsAllInReverseOrder(t *testing.T) {
	fr := NewFrame(context.Background())
	var calls []int
	DeferBoot(fr, func() { calls = append(calls, 1) })
	DeferBoot(fr, func() { calls = append(calls, 2); panic("cleanup failed") })
	DeferBoot(fr, func() { calls = append(calls, 3) })
	func() {
		defer func() {
			if recover() == nil {
				t.Error("failure was hidden")
			}
		}()
		RunBootCleanup(fr)
	}()
	if !reflect.DeepEqual(calls, []int{3, 2, 1}) {
		t.Fatal(calls)
	}
	func() { defer func() { _ = recover() }(); RunBootCleanup(fr) }()
	if len(calls) != 3 {
		t.Fatal("cleanup ran twice")
	}
}

func TestScopedContextDoesNotChangeParent(t *testing.T) {
	fr := NewFrame(context.Background())
	child := EnterContext(fr, ContextRoot())
	if fr.scopedContext != nil || child.scopedContext == nil {
		t.Fatal("context override mutated parent")
	}
	if EnterScope(child).scopedContext != child.scopedContext {
		t.Fatal("scope lost context")
	}
}
