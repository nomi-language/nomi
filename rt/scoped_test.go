package rt

import (
	"context"
	"testing"
	"time"
)

// A function value's body runs under the fields it kept and leaves its
// caller's frame as it was. Calling one that kept the caller's own fields
// is the caller's frame itself, so a program that replaces no field
// allocates nothing per call.
func TestEnterKeptFields(t *testing.T) {
	root := NewFrame(context.Background())
	PublishScoped(root, map[string]any{"label": "base"})
	kept := KeptFields(EnterScopedField(root, "label", "made"))

	if fr, release := EnterKeptFields(root, KeptFields(root)); fr != root || release != nil {
		t.Fatal("the caller's own fields entered a new frame")
	}
	if allocs := testing.AllocsPerRun(100, func() { EnterKeptFields(root, KeptFields(root)) }); allocs != 0 {
		t.Fatalf("entering the caller's own fields allocated %v times", allocs)
	}
	if fr, release := EnterKeptFields(root, nil); fr != root || release != nil {
		t.Fatal("a value that kept nothing entered a new frame")
	}

	fr, release := EnterKeptFields(root, kept)
	if release != nil || ScopedField[string](fr, "label") != "made" || ScopedField[string](root, "label") != "base" {
		t.Fatal("the kept field is not in force in the body, or leaked to the caller")
	}

	// A deadline in force at the call still bounds a body whose context had
	// none.
	bounded := EnterContext(root, ContextWithTimeout(ContextRoot(), Duration(time.Hour)))
	fr, _ = EnterKeptFields(bounded, kept)
	if !chainBound(ActiveContext(fr)).set() || ScopedField[string](fr, "label") != "made" {
		t.Fatal("the caller's deadline does not bound the body")
	}

	// A kept deadline bounds the body's waits, and the release ends it.
	withDeadline := KeptFields(EnterContext(root, ContextWithTimeout(ContextRoot(), Duration(time.Hour))))
	fr, release = EnterKeptFields(root, withDeadline)
	if release == nil || !fr.underDeadline || root.underDeadline {
		t.Fatal("the kept deadline does not bound the body's waits, or reached the caller")
	}
	release()

	// While boot runs and while a `once` initializer runs, nothing is kept.
	if KeptFields(EnterBoot(root)) != nil {
		t.Fatal("a function value made during boot kept fields")
	}
	if KeptFields(root.forcingOnce(new(onceID))) != nil {
		t.Fatal("a function value made in a once initializer kept fields")
	}
}

func TestScopedFieldSnapshotsAreIndependent(t *testing.T) {
	root := &Frame{}
	PublishScoped(root, map[string]any{"label": "root", "port": int64(3000)})
	child := EnterScopedField(root, "label", "child")
	sibling := EnterScopedField(root, "port", int64(4000))
	if ScopedField[string](root, "label") != "root" || ScopedField[string](child, "label") != "child" || ScopedField[string](sibling, "label") != "root" {
		t.Fatal("field override leaked between lineages")
	}
	PublishScoped(root, map[string]any{"label": "new"})
	if ScopedField[string](child, "label") != "child" || ScopedField[int64](child, "port") != 3000 {
		t.Fatal("publication changed a captured snapshot")
	}
}
