package hostpair

import (
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ffirun"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// Two key rules held in agreement here.
//
// Neither is a consumer choice, which is what separates them from the two
// that remain in disagreement_test.go. Getting the first wrong puts two
// declarations under one key, and the wrapper's second rt.RegisterExternFunc
// call then fails, so a program with two `impl Add<X, T> for T` blocks does
// not start. Getting the second wrong puts one declaration under a key no
// lookup forms, so the binding is registered and unreachable.
//
// What each looks like against a running program:
//
//	$ nomi run  # two `impl Add<X, Score> for Score`, each `fn add ... go`
//	nomi run: FFI export registration for "Score.add" in package "addbinding"
//	failed (declared at main.nomi:22:51):
//	RegisterExternFunc("Score.add"): name already registered
//
//	$ nomi run  # `impl Box<T> { pub fn origin(): Int go go_box.Origin }`
//	line 17: unknown builtin 'Box.origin'
//
// The second is the interesting one to have on record. Its symptom depends on
// something CALLING the declaration; with nothing calling it the registration
// is simply dead and the wrong key is indistinguishable from correctness.
// That is why TestAgree_GenericReceiverKeyIsOneTheRuntimeForms asserts about
// the KEY SET rather than about program behaviour: it checks that the key
// ffirun produces is a member of the candidate list Pairing.Keys builds, which
// is false for a wrong key whether or not anything calls it.

// implKeysFor is the ffirun-derived key of every func pairing in one file,
// sorted.
//
// Read off the ffirunPairing STRUCT rather than out of its String() rendering,
// because an interface-qualified key contains a space (`Add<Bonus, Score>`)
// and splitting the rendered line on whitespace truncated it — which is how
// the first version of this test reported `Score.Add<Bonus,`.
func implKeysFor(t *testing.T, name, source string) []string {
	t.Helper()
	root := writeProject(t, map[string]string{name: source})
	packages, err := ffirun.DiscoverInScope(root, root)
	if err != nil {
		t.Fatalf("ffirun.DiscoverInScope: %v", err)
	}
	var out []string
	for _, p := range flattenFfirun(packages) {
		if p.Kind != "func" {
			continue
		}
		out = append(out, p.Key)
	}
	sort.Strings(out)
	return out
}

// TestAgree_TwoInstantiationsOfOneGenericInterfaceGetTwoKeys checks that two
// instantiations of one generic interface for one receiver get two keys.
//
// ffirun.implOwnerKey reads v.Interface, so the two `impl Add<X, Score> for
// Score` blocks produce two keys instead of one. The check is not merely that
// they differ — it is that ffirun's two keys are exactly hostpair's two
// BindingKeys, and that each is a member of the candidate list the runtime
// forms for its own declaration. Two distinct keys that the runtime does not
// look for would be registered and unreachable.
func TestAgree_TwoInstantiationsOfOneGenericInterfaceGetTwoKeys(t *testing.T) {
	const source = `gopkg "example.com/app/ffi" as ffi

pub struct Score {
  points: Int
}

pub struct Bonus {
  points: Int
}

impl Add<Score, Score> for Score {
  fn add(a: Score, b: Score): Score go ffi.AddScores
}

impl Add<Bonus, Score> for Score {
  fn add(a: Score, b: Bonus): Score go ffi.AddBonus
}
`
	fromFfirun := implKeysFor(t, "scoring.nomi", source)
	want := []string{
		"Score.Add<Bonus, Score>.add",
		"Score.Add<Score, Score>.add",
	}
	if strings.Join(fromFfirun, "|") != strings.Join(want, "|") {
		t.Fatalf("ffirun keys = %v, want %v", fromFfirun, want)
	}

	_, pairings := hostpairKeysForSource(t, "scoring.nomi", source)
	var runtimeKeys []string
	for _, p := range pairings {
		if p.Kind != KindFunc {
			continue
		}
		runtimeKeys = append(runtimeKeys, p.BindingKey())
		if !contains(p.Keys(), p.BindingKey()) {
			t.Errorf("BindingKey %q is not among the candidates %v", p.BindingKey(), p.Keys())
		}
	}
	sort.Strings(runtimeKeys)
	if strings.Join(runtimeKeys, "|") != strings.Join(want, "|") {
		t.Fatalf("hostpair BindingKeys = %v, want %v", runtimeKeys, want)
	}

	// The BuilderKey carries the module and the BindingKey does not. That is
	// the module-prefix disagreement in disagreement_test.go, and asserting it
	// here keeps this test from quietly becoming a claim that it closed.
	for _, p := range pairings {
		if p.Kind != KindFunc {
			continue
		}
		if !strings.HasPrefix(p.BuilderKey(), "scoring.") {
			t.Errorf("BuilderKey %q lost its module prefix", p.BuilderKey())
		}
		if strings.HasPrefix(p.BindingKey(), "scoring.") {
			t.Errorf("BindingKey %q gained a module prefix; the module-prefix disagreement is not this test's", p.BindingKey())
		}
	}
}

// TestAgree_GenericReceiverKeyIsOneTheRuntimeForms checks that a generic
// receiver keys on its base name, and it asserts about the REGISTRY rather
// than about behaviour.
//
// Keyed on the receiver's full type string, ffirun would produce
// `Box<T>.origin` while the runtime forms only `boxes.Box.origin` and
// `Box.origin`, and the intersection would be empty. The assertion below is
// that the intersection is non-empty AND that ffirun's key is the one the
// runtime tries — a statement about the two key sets, and so false for a
// wrong key even with nothing calling the binding.
//
// The fixture uses `struct Box<T>` rather than a generic distinct because
// `type Box<T> Int` is a parse error, and a fixture that does not parse derives
// nothing and would pass for the wrong reason.
func TestAgree_GenericReceiverKeyIsOneTheRuntimeForms(t *testing.T) {
	const source = `gopkg "example.com/app/ffi" as ffi

pub struct Box<T> {
  value: T
}

impl Box<T> {
  fn peek(b: Box<T>): Int go ffi.Peek
}
`
	fromFfirun := implKeysFor(t, "boxes.nomi", source)
	if len(fromFfirun) != 1 {
		t.Fatalf("expected exactly one ffirun key, got %v", fromFfirun)
	}
	ffirunKey := fromFfirun[0]
	if strings.ContainsAny(ffirunKey, "<>") {
		t.Fatalf("ffirun key %q still carries the receiver's type parameters", ffirunKey)
	}
	if ffirunKey != "Box.peek" {
		t.Fatalf("ffirun key = %q, want Box.peek", ffirunKey)
	}

	_, pairings := hostpairKeysForSource(t, "boxes.nomi", source)
	if len(pairings) != 1 {
		t.Fatalf("expected exactly one hostpair pairing, got %d", len(pairings))
	}
	p := pairings[0]
	if p.Receiver != "Box" {
		t.Fatalf("hostpair receiver = %q, want the base name Box", p.Receiver)
	}
	if p.BindingKey() != ffirunKey {
		t.Fatalf("hostpair BindingKey %q != ffirun key %q", p.BindingKey(), ffirunKey)
	}
	if !contains(p.Keys(), ffirunKey) {
		t.Fatalf("ffirun registers %q, which is in none of the runtime's candidates %v", ffirunKey, p.Keys())
	}
}

// TestAgree_RuntimeCandidatesMatchHostpairKeys holds the candidate list's
// shape: Pairing.Keys is ordered most specific first, and BindingKey is a
// member of it for every shape.
func TestAgree_RuntimeCandidatesMatchHostpairKeys(t *testing.T) {
	rows := []Pairing{
		{Module: "calendar", Receiver: "NaiveDateTime", Interface: "Add<Duration, NaiveDateTime>", Name: "add"},
		{Module: "scoring", Receiver: "Score", Interface: "Add<Bonus, Score>", Name: "add"},
		{Module: "regex", Receiver: "Regex", Name: "compile"},
		{Module: "boxes", Receiver: "Box", Name: "peek"},
		{Receiver: "Score", Interface: "Add<Bonus, Score>", Name: "add"},
		{Receiver: "RawBox", Name: "label"},
		{Module: "random", Name: "below_state"},
		{Name: "echo"},
	}
	for _, p := range rows {
		keys := p.Keys()
		if !contains(keys, p.BindingKey()) {
			t.Errorf("%s: BindingKey %q is not among %v", p.BuilderKey(), p.BindingKey(), keys)
		}
		if keys[0] != p.BuilderKey() {
			t.Errorf("%s: BuilderKey is not the first candidate %q", p.BuilderKey(), keys[0])
		}
		seen := map[string]bool{}
		for _, k := range keys {
			if seen[k] {
				t.Errorf("%s: candidate list repeats %q", p.BuilderKey(), k)
			}
			seen[k] = true
		}
	}
}

// TestAgree_CalendarAddLadderDerivesElevenDistinctKeys checks the key rule on
// the real std/calendar.nomi rather than on a fixture shaped like it.
//
// None of calendar's declarations names a Go symbol, so hostpair derives no
// PAIRINGS from it. The key half is derived anyway, straight from the parsed
// impl blocks, because the key half is what can collapse: the eleven
// `impl Add<X, NaiveDateTime>` blocks each declare `add`, and they must get
// eleven keys rather than one.
//
// Eleven is asserted as an exact count, not a floor. If std/calendar grows a
// twelfth rung this test fails and the count is updated deliberately.
func TestAgree_CalendarAddLadderDerivesElevenDistinctKeys(t *testing.T) {
	src, ok := std.ReadFile("calendar")
	if !ok {
		t.Fatal("std.ReadFile(\"calendar\") returned nothing")
	}
	nodes, err := parser.Parse(lexer.Lex(string(src)))
	if err != nil {
		t.Fatalf("parse std/calendar.nomi: %v", err)
	}

	byKey := map[string][]string{}
	preFix := map[string][]string{}
	var order []string
	for _, n := range nodes {
		block, isImpl := n.(*ast.ImplBlock)
		if !isImpl || block.Interface == nil {
			continue
		}
		if receiverBaseName(block.Receiver) != "NaiveDateTime" {
			continue
		}
		iface := interfaceInstantiation(block.Interface)
		if !strings.HasPrefix(iface, "Add<") {
			continue
		}
		p := Pairing{
			Kind:      KindFunc,
			Module:    "calendar",
			Receiver:  "NaiveDateTime",
			Interface: iface,
			Name:      "add",
		}
		key := p.BindingKey()
		if _, seen := byKey[key]; !seen {
			order = append(order, key)
		}
		byKey[key] = append(byKey[key], iface)

		// The same declaration under a rule that drops the interface, keying
		// on v.Receiver.TypeString() and nothing else.
		bare := Pairing{Kind: KindFunc, Module: "calendar", Receiver: "NaiveDateTime", Name: "add"}
		preFix[bare.BindingKey()] = append(preFix[bare.BindingKey()], iface)
	}

	if len(order) != 11 {
		t.Fatalf("the Add<X, NaiveDateTime> ladder derives %d distinct keys, want 11:\n%s",
			len(order), strings.Join(order, "\n"))
	}
	for key, ifaces := range byKey {
		if len(ifaces) != 1 {
			t.Errorf("key %q still carries %d declarations: %v", key, len(ifaces), ifaces)
		}
	}
	// The control that says the eleven is the key rule's doing and not the
	// ladder's shape. Under the interface-dropping rule every rung lands on one
	// key, so a reading that "the declarations are distinct anyway" is ruled
	// out.
	if len(preFix) != 1 || len(preFix["NaiveDateTime.add"]) != 11 {
		t.Errorf("the interface-dropping rule produced %d keys with %d declarations on NaiveDateTime.add; expected 1 key carrying all 11",
			len(preFix), len(preFix["NaiveDateTime.add"]))
	}
	t.Logf("11 distinct keys, one per rung:\n%s", strings.Join(order, "\n"))
}

// TestAgree_StdRandomGenericReceiverIsBoundForAReason was here and is DELETED.
//
// It asked "std/random was unbound only by luck — is it now bound for a
// reason?" and answered in two halves. Half one derived std/random's three
// go-bound top-level declarations and asserted `impl Generator<T>` carried
// none, so the generic-receiver key could not bite. Half two rewrote
// `Generator.step` in memory to be go-bound and asserted the derived key was
// `Generator.step` rather than `Generator<T>.step`.
//
// Half one's population is gone: std/random declares `host fn` now and names
// no Go symbol, so DeriveSource over it yields nothing and the assertion could
// only ever compare two empty lists.
//
// Half two is not lost. TestAgree_GenericReceiverKeyIsOneTheRuntimeForms
// above makes exactly that claim over a fixture — `impl Box<T>` with a
// go-bound member — and asserts all three parts this one did: the receiver is
// the base name, ffirun's key carries no type parameters, and the key is a
// member of the runtime's candidate list.

func contains(keys []string, want string) bool {
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}
