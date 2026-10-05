package rt

import "testing"

// The Context value store. Every test here is about a property the deadline
// chain does not have, so none of them duplicates a deadline test.
//
// Two types with one Go representation, on purpose and in every test: `TraceId`
// and `Subject` are both `string` at run time, which is the case a key derived
// from Go reflection gets wrong and an address-keyed one cannot. `type TraceId
// String` and `type Subject String` are ordinary Nomi, so this is the reachable
// shape rather than a contrived one.
type k4TraceId string
type k4Subject string

var (
	tidK4TraceId = TypeID{Nomi: "ctxvals.TraceId"}
	tidK4Subject = TypeID{Nomi: "ctxvals.Subject"}
)

func TestContextValue_RootHasNothing(t *testing.T) {
	got := ContextValue(ContextRoot(), TypeOf[k4TraceId](&tidK4TraceId))
	if got.Tag != TagNone {
		t.Errorf("Context.value on a fresh root = tag %d, want None", got.Tag)
	}
}

func TestContextValue_RoundTrips(t *testing.T) {
	c := ContextWithValue(ContextRoot(), &tidK4TraceId, k4TraceId("trace-123"))
	got := ContextValue(c, TypeOf[k4TraceId](&tidK4TraceId))
	if got.Tag != TagSome || got.Some != "trace-123" {
		t.Errorf("Context.value = tag %d / %q, want Some(\"trace-123\")", got.Tag, got.Some)
	}
}

// TestContextValue_ChildShadowsAndTheParentIsUnchanged is the assertion
// context_values_test.nomi makes twice, and it is the one a fixture that only
// checked the child would pass without: an implementation that mutated the
// parent node answers correctly for `shadowed` and wrongly for `traced`.
//
// So both ends are read, and the parent is read after the child derivation.
func TestContextValue_ChildShadowsAndTheParentIsUnchanged(t *testing.T) {
	root := ContextRoot()
	traced := ContextWithValue(root, &tidK4TraceId, k4TraceId("trace-123"))
	shadowed := ContextWithValue(traced, &tidK4TraceId, k4TraceId("trace-456"))

	if got := ContextValue(shadowed, TypeOf[k4TraceId](&tidK4TraceId)); got.Some != "trace-456" {
		t.Errorf("the child does not shadow: %q, want \"trace-456\"", got.Some)
	}
	if got := ContextValue(traced, TypeOf[k4TraceId](&tidK4TraceId)); got.Some != "trace-123" {
		t.Errorf("deriving a child MUTATED its parent: %q, want \"trace-123\"", got.Some)
	}
	if got := ContextValue(root, TypeOf[k4TraceId](&tidK4TraceId)); got.Tag != TagNone {
		t.Errorf("deriving a child gave the ROOT a value: tag %d / %q, want None", got.Tag, got.Some)
	}
}

// TestContextValue_KeyIsTheIdentityAndNotTheGoType is the reason the key is a
// `*TypeID` at all. Two Nomi types, one Go `string`; a lookup for one must not
// answer with the other's value, and `n.val.(T)` alone would succeed for both.
func TestContextValue_KeyIsTheIdentityAndNotTheGoType(t *testing.T) {
	c := ContextWithValue(ContextRoot(), &tidK4TraceId, k4TraceId("trace-123"))
	if got := ContextValue(c, TypeOf[k4Subject](&tidK4Subject)); got.Tag != TagNone {
		t.Errorf("a Subject lookup found a TraceId (%q); the key is not the identity", got.Some)
	}
	c2 := ContextWithValue(c, &tidK4Subject, k4Subject("alice"))
	if got := ContextValue(c2, TypeOf[k4Subject](&tidK4Subject)); got.Some != "alice" {
		t.Errorf("Subject = %q, want \"alice\"", got.Some)
	}
	if got := ContextValue(c2, TypeOf[k4TraceId](&tidK4TraceId)); got.Some != "trace-123" {
		t.Errorf("the TraceId bound further up was lost: %q", got.Some)
	}
}

// TestContextValue_LookupWalksPastADeadlineLink pins the one-node-type decision:
// a deadline derivation between a binding and its reader must not end the value
// walk, and a value derivation must not end the deadline walk.
func TestContextValue_LookupWalksPastADeadlineLink(t *testing.T) {
	c := ContextWithValue(ContextRoot(), &tidK4TraceId, k4TraceId("trace-123"))
	c = ContextWithDeadline(c, Instant(1_000))
	if got := ContextValue(c, TypeOf[k4TraceId](&tidK4TraceId)); got.Some != "trace-123" {
		t.Errorf("a deadline link ended the value walk: %q", got.Some)
	}
}

// TestContext_ValueLinkIsInvisibleToTheDeadlineWalk is the other direction, and
// it is what makes the shared node type safe: a value node leaves `set` false,
// so contextEffectiveDeadline must skip it rather than read its zero `deadline`
// as a deadline in 1970 — which would make every bound context instantly
// expired.
func TestContext_ValueLinkIsInvisibleToTheDeadlineWalk(t *testing.T) {
	c := ContextWithValue(ContextRoot(), &tidK4TraceId, k4TraceId("trace-123"))
	if got := ContextDeadline(c); got.Tag != TagNone {
		t.Errorf("a value link produced a deadline (%v); it must be invisible to the walk", got.Some)
	}
	bounded := ContextWithDeadline(ContextRoot(), Instant(5_000))
	withVal := ContextWithValue(bounded, &tidK4TraceId, k4TraceId("t"))
	if got := ContextDeadline(withVal); got.Tag != TagSome || got.Some != Instant(5_000) {
		t.Errorf("binding a value lost the inherited deadline: tag %d / %v", got.Tag, got.Some)
	}
}

// TestContextWithFloor_SurvivesAValueLink is the guard the floor's own header
// asks for: its splice "assumes the chain's shape", and value links change that
// shape. A rebind must still tighten and never widen with value links present on
// either side of it.
func TestContextWithFloor_SurvivesAValueLink(t *testing.T) {
	prev := ContextWithValue(ContextWithDeadline(ContextRoot(), Instant(1_000)),
		&tidK4TraceId, k4TraceId("t"))
	// `next` is deliberately looser (a later deadline) and carries a value link
	// of its own, so a floor that read the wrong node would widen.
	next := ContextWithValue(ContextWithDeadline(ContextRoot(), Instant(9_000)),
		&tidK4Subject, k4Subject("alice"))

	floored := ContextWithFloor(prev, next)
	if got := ContextDeadline(floored); got.Tag != TagSome || got.Some != Instant(1_000) {
		t.Errorf("the rebind WIDENED the deadline: tag %d / %v, want Some(1000)", got.Tag, got.Some)
	}
	// And the rebound context still reads its own binding: the floor splices a
	// deadline node above `next`, so the value walk has to pass through it.
	if got := ContextValue(floored, TypeOf[k4Subject](&tidK4Subject)); got.Some != "alice" {
		t.Errorf("the floor's splice hid next's own binding: %q", got.Some)
	}
	// prev's binding is not reachable from the result, because the floor keeps
	// `next`'s chain and takes only a deadline from `prev`. Asserted so the
	// splice's scope is pinned rather than assumed.
	if got := ContextValue(floored, TypeOf[k4TraceId](&tidK4TraceId)); got.Tag != TagNone {
		t.Errorf("the floor spliced prev's VALUES in too (%q); it carries a deadline only", got.Some)
	}
}

// TestContextValue_ZeroWitnessMatchesNothing pins the one input no emitted line
// produces. A nil key that matched every node would answer with an arbitrary
// binding, which is the failure mode a `struct{}`-shaped identity has.
func TestContextValue_ZeroWitnessMatchesNothing(t *testing.T) {
	c := ContextWithValue(ContextRoot(), &tidK4TraceId, k4TraceId("trace-123"))
	if got := ContextValue(c, Type[k4TraceId]{}); got.Tag != TagNone {
		t.Errorf("a zero witness matched a binding (%q)", got.Some)
	}
}

// TestContextValue_WrongRepresentationTraps is the demonstrated firing of the
// checked assertion in ContextValue. It cannot be reached from a checked Nomi
// program — one Nomi type has one Go type — so the only way to show the guard
// works at all is to file a value under an identity that is not its own here.
func TestContextValue_WrongRepresentationTraps(t *testing.T) {
	c := ContextWithValue(ContextRoot(), &tidK4TraceId, int64(7))
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("a value filed under the wrong identity was returned silently")
		}
		err, ok := r.(*Error)
		if !ok {
			t.Fatalf("panicked with %T, want *rt.Error", r)
		}
		if !contains(err.Msg, "ctxvals.TraceId") {
			t.Errorf("the trap does not name the type: %q", err.Msg)
		}
	}()
	_ = ContextValue(c, TypeOf[k4TraceId](&tidK4TraceId))
}

// TestTypeWitness_TwoTypesAreTwoWitnesses is the typewitness half of
// dispatch.go's TestTypeIDsAreDistinctAddresses: the witness must carry the
// identity through, so two types' witnesses are unequal keys.
func TestTypeWitness_TwoTypesAreTwoWitnesses(t *testing.T) {
	a := TypeOf[k4TraceId](&tidK4TraceId)
	b := TypeOf[k4Subject](&tidK4Subject)
	if a.TID == b.TID {
		t.Fatal("two types' witnesses share one identity; every value lookup would collide")
	}
	if a.TID != TypeOf[k4TraceId](&tidK4TraceId).TID {
		t.Fatal("two witnesses for ONE type carry two identities; a store and a load would miss")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
