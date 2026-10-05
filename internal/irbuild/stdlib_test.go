package irbuild

import (
	"fmt"
	"github.com/nomi-language/nomi/internal/stdlibbindings"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/std"
)

// TestStdlibBindingsProject is the guard on the registry's DERIVATION.
//
// Every row holds a real rt function value, and both the emitted call text and
// the signature are read off that value — so the only way a row can be wrong is
// for the Go signature not to project onto scalar kinds at all. init collects
// those rather than panicking (a compiler that will not start over a registry
// defect is worse than one that refuses the affected call); this is where they
// become loud.
func TestStdlibBindingsProject(t *testing.T) {
	if len(stdlibBindingErrors) > 0 {
		t.Fatalf("stdlib bindings that do not project onto scalar kinds:\n  %s",
			strings.Join(stdlibBindingErrors, "\n  "))
	}
	if len(stdlibHostFuncs) != len(stdlibbindings.RtFuncs()) {
		t.Fatalf("registry has %d rows for %d RtFuncs bindings", len(stdlibHostFuncs), len(stdlibbindings.RtFuncs()))
	}
	// The derived call text must name a REGISTERED host package and nothing
	// else. A method value or a closure would produce a plausible-looking wrong
	// symbol, which is the one failure this derivation could still have.
	//
	// Checked against hostPackages rather than against the literal "rt.", so a
	// second host package is admitted by being registered and not by loosening
	// this. It also pins the property header() depends on: the local name must
	// be the import path's last element, because the import is emitted
	// unaliased.
	locals := map[string]bool{}
	for _, hp := range hostPackages {
		locals[hp.local] = true
		if last := hp.path[strings.LastIndex(hp.path, "/")+1:]; last != hp.local {
			t.Errorf("host package %q is spelled %q; header() emits the path UNALIASED, so the local "+
				"name must be its last element", hp.path, hp.local)
		}
	}
	for key, h := range stdlibHostFuncs {
		local, _, ok := strings.Cut(h.call, ".")
		if !ok || strings.Count(h.call, ".") != 1 || !locals[local] {
			t.Errorf("%s: derived call %q is not a plain symbol in a registered host package", key, h.call)
		}
		if h.pkg == "" {
			t.Errorf("%s: derived call %q carries no import path", key, h.call)
		}
	}
}

// TestEveryScalarHostFnIsBoundOrDeclared is the coverage guard, and it is the
// deliverable rather than the table.
//
// A hand-written table cannot be complete, but it CAN be closed: every
// scalar-signature `host fn` in std/ must be either bound to an rt symbol or
// listed below with the reason it is not. So adding a scalar `host fn` to std/
// fails this test until somebody decides which it is — which is the difference
// between a registry that is a decision and one that is whatever accumulated.
//
// Only the SCALAR ones. A `host fn` over `List`, `Map`, `Iter` or an opaque
// handle is out of the scalar subset by signature, so it is detected
// structurally and needs no entry here; listing all of those would be a list
// nobody maintains.
func TestEveryScalarHostFnIsBoundOrDeclared(t *testing.T) {
	// Deliberately unbound, with the reason. Each is refused by name at its call
	// site as `stdlib host function`. The list is short because the non-scalar
	// and the GENERIC ones are detected structurally: this is the set somebody
	// had to DECIDE about. An entry leaves this map when its reason is
	// discharged, not reworded.
	unbound := map[string]string{
		// Operator impls: `impl Add<Int, Int> for Int`'s `add` IS the definition
		// of `+`. The builder lowers `+` natively, so binding these would give
		// one operation two lowerings, and for the trapping Int forms the two
		// would not agree, because the fault text carries the OPERATOR's source
		// line (`line 6: integer overflow: …`) and an explicit `Int.add(a, b)`
		// call site has no operator line to name.
		//
		// The keys carry the instantiation because every one of these is an impl
		// of a GENERIC interface; see stdKey. `int.Int.divide` has two
		// declarations, `impl Divide<Int, Int>` (this `host fn`) and
		// `impl Divide<NonZeroInt, Int>` (a Nomi body), and the instantiated key
		// separates them.
		"int.Int.Add<Int, Int>.add":                   "operator impl; `+` is lowered natively and the trap text carries the operator's line",
		"int.Int.Subtract<Int, Int>.subtract":         "operator impl; see int.Int.Add<Int, Int>.add",
		"int.Int.Multiply<Int, Int>.multiply":         "operator impl; see int.Int.Add<Int, Int>.add",
		"float.Float.Add<Float, Float>.add":           "operator impl; `+` on Float is Go's own `+`",
		"float.Float.Subtract<Float, Float>.subtract": "operator impl; see float.Float.Add<Float, Float>.add",
		"float.Float.Multiply<Float, Float>.multiply": "operator impl; see float.Float.Add<Float, Float>.add",
		"float.Float.Divide<Float, Float>.divide":     "operator impl; `/` on Float lowers to rt.DivFloat",
		"strings.String.Add<String, String>.add":      "operator impl; String `+` is lowered natively as concatenation",
		"int.Int.Divide<Int, Int>.divide":             "operator impl; `/` on Int is lowered natively and the divide-by-zero trap carries the operator's line",

		// The Decimal operator impls, unbound for a STRUCTURAL reason. A row is
		// a named rt symbol of exactly the Nomi arity, so it has NOWHERE TO PUT
		// A LINE: a bound `Divide.divide(1d, 0d)` would print
		// `line 0: decimal division by zero`, because the only way a row can
		// name a position is to bake one in.
		//
		// They lower through scalararith.go's arm instead, which has `t.Line`.
		// The nine Int/Float/String siblings are unbound for the same reason
		// and are exempted in TestStdlibRefusesUnmappedHostFnsByName. These four
		// are here because they have scalar signatures and that test does not
		// see them.
		//
		// EXPIRY: a stdlibBinding shape that can carry a call-site position.
		// TestScalarArith_TheThirteenKeysStayUnbound is the other half of this
		// and fires on the same event.
		"decimal.Decimal.Add<Decimal, Decimal>.add":           "the row cannot carry the call's line; scalararith.go lowers it",
		"decimal.Decimal.Subtract<Decimal, Decimal>.subtract": "the row cannot carry the call's line; scalararith.go lowers it",
		"decimal.Decimal.Multiply<Decimal, Decimal>.multiply": "the row cannot carry the call's line; scalararith.go lowers it",
		"decimal.Decimal.Divide<Decimal, Decimal>.divide":     "the row cannot carry the call's line; scalararith.go lowers it",

		// Three iteration protocol hosts, which count here because a std
		// signature's function type projects. None is a crossing a program
		// reaches: each is served by the VM's iteration drivers.
		"bytes.Bytes.each_while": "served by internal/vm's byte view: `Bytes.each_while(b, f)` lowers as " +
			"`Iter.each_while` over it (irqualcall.go); TestRunCommand_EachWhileRunsDirectly",
		"strings.String.each_while": "served by internal/vm's grapheme view: `String.each_while(s, f)` lowers " +
			"as `Iter.each_while` over it (irqualcall.go); TestRunCommand_EachWhileRunsDirectly",
		"iter.from_each": "file-private to std/iter, and its one caller `Iter.from` lowers to the VM's " +
			"ir.IterFrom driver (iriterbody.go), so no program reaches the crossing",
	}

	idx := stdlibLowering()
	var missing []string
	for key, f := range idx.byKey {
		if !needsABindingDecision(f) {
			continue
		}
		if _, declared := unbound[key]; declared {
			continue
		}
		missing = append(missing, key)
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("scalar-signature `host fn`s in std/ that are neither bound to an rt symbol nor declared unbound here:\n  %s\n"+
			"Bind them in internal/stdlibbindings or add them to this test's `unbound` map with the reason.",
			strings.Join(missing, "\n  "))
	}
	// THE REVERSE DIRECTION. Each exempted key must still be declared AND still
	// be unbound. The forward loop skips a bound key (needsABindingDecision is
	// false), so without the second check an entry for a key that has since
	// been bound passes silently. A stale exemption whose subject is done reads
	// as a live refusal with a live reason, which is worse than one whose
	// subject vanished.
	for key := range unbound {
		f, declared := idx.byKey[key]
		if !declared {
			t.Errorf("`unbound` names %q, which std/ does not declare", key)
			continue
		}
		if f.rtCall != "" {
			t.Errorf("`unbound` exempts %q with a reason, and it is BOUND — to %s. The "+
				"exemption is stale in the way that reads as live: somebody sizing this "+
				"key would count a refusal that does not happen. Delete the entry.", key, f.rtCall)
		}
	}
	// A stated reason is a claim nobody re-checks. So the one reason whose
	// precondition is mechanically checkable gets checked: an entry claiming the
	// key collapse must really be a key several declarations answer to, and the
	// evaluation IS the reason.
	//
	// The other reasons here are genuine judgements — a dependency rt will not
	// take, an operator with two lowerings, an OS facility that belongs in an
	// adapter — and none of them is a fact a test can confirm.
	for key, reason := range unbound {
		if reason != reasonKeyCollapse {
			continue
		}
		if n := stdKeyClaimants(std.Load())[key]; n < 2 {
			t.Errorf("`unbound[%q]` claims the key collapse, but %d declaration(s) answer to it; "+
				"the reason is wrong and the entry is hiding whatever the real obstacle is", key, n)
		}
	}
}

// reasonKeyCollapse marks an `unbound` entry whose reason is that `stdKey`
// collapses several declarations onto one key, so binding it would add an rt
// function no call site can reach. A named constant rather than prose because
// the loop above CHECKS this one.
const reasonKeyCollapse = "several declarations answer to this key; see TestCollapsedStdlibKeysCostOnlyCoverage"

// stdKeyClaimants counts, per builtin key, how many std declarations answer to
// it. `byKey` cannot say: it holds one entry per key by construction, which is
// the whole reason a collapse is invisible there.
func stdKeyClaimants(lib *std.StdLib) map[string]int {
	modules := make([]string, 0, len(lib.Nodes))
	for name := range lib.Nodes {
		modules = append(modules, name)
	}
	sort.Strings(modules)
	out := map[string]int{}
	for _, module := range modules {
		anchors := stdAnchorsOf(lib.Files[module])
		for _, c := range collectStdCandidates(module, lib.Nodes[module], anchors) {
			out[c.f.key]++
		}
	}
	return out
}

// needsABindingDecision is the population TestEveryScalarHostFnIsBoundOrDeclared
// demands an answer about: a `host fn` with no rt symbol whose refusal is not
// already explained structurally.
//
// Extracted so a SECOND guard can ask the same question of a different set.
// Overload sets make "several declarations under one spelling" the normal case,
// and `byKey` holds one entry per key, so a declaration ABSORBED by a residual
// key collapse is invisible to the loop above.
// TestCollapsedStdlibKeysCostOnlyCoverage checks the absorbed set against this
// predicate, so the completeness claim holds over every declaration rather
// than over every key.
//
// Deliberately shared rather than restated: the claim is "an absorbed
// declaration would have been skipped by THAT guard", so it has to be that
// guard's predicate and not a second opinion about it.
func needsABindingDecision(f *stdFunc) bool {
	if f.decl != nil || f.rtCall != "" {
		return false // Nomi-bodied, or already bound.
	}
	switch f.why {
	case "stdlib function outside the scalar subset":
		// Out of the subset by SIGNATURE, detected structurally.
		return false
	case "stdlib generic function":
		// Also structural: a
		// `host fn` with type parameters is not waiting on a representation, it
		// is waiting on the type-parameter dispatch dictionary. `io.print<T>`,
		// `channels.send<T>` and the 20-odd `iter.*_each` callbacks are all
		// this. Skipping it keeps that list "the set somebody had to DECIDE
		// about" rather than growing it by entries that all say the same thing.
		return false
	}
	return true
}

// sameStdDecl reports whether two stdFuncs describe one DECLARATION.
//
// Pointer equality will not do: the claimant list comes from a second
// collectStdCandidates pass, so every entry is a fresh *stdFunc even where it
// describes the same source. What identifies a declaration inside one key is
// what `add` selects on — whether it has a Nomi body — plus the impl block it
// came from, and both are recorded on the stdFunc.
func sameStdDecl(a, b *stdFunc) bool {
	return (a.decl == nil) == (b.decl == nil) &&
		a.recv == b.recv && a.iface == b.iface && a.name == b.name
}

// stdKeyClaimantList is stdKeyClaimants with the declarations rather than the
// count, for the guard that has to inspect the ones a key ABSORBED.
func stdKeyClaimantList(lib *std.StdLib) map[string][]*stdFunc {
	modules := make([]string, 0, len(lib.Nodes))
	for name := range lib.Nodes {
		modules = append(modules, name)
	}
	sort.Strings(modules)
	out := map[string][]*stdFunc{}
	for _, module := range modules {
		anchors := stdAnchorsOf(lib.Files[module])
		for _, c := range collectStdCandidates(module, lib.Nodes[module], anchors) {
			out[c.f.key] = append(out[c.f.key], c.f)
		}
	}
	return out
}

// TestKeyCollapseReasonStillEvaluates is the positive control for the
// reasonKeyCollapse check above, which has no entries to check: `unbound`
// carries no reasonKeyCollapse entry. A checked reason with no current user is
// a guard that cannot fire, so the EVALUATOR is exercised directly rather than
// trusted to work the next time somebody needs it.
//
// The witness is a key the instantiated stdKey does NOT separate:
// `bytes.Bytes.to_string` is an inherent `pub host fn` plus an
// `impl Display for Bytes`, and `Display` takes no type arguments, so there is
// nothing for an instantiation to distinguish.
func TestKeyCollapseReasonStillEvaluates(t *testing.T) {
	claimants := stdKeyClaimants(std.Load())
	const witness = "bytes.Bytes.to_string"
	if n := claimants[witness]; n < 2 {
		t.Errorf("stdKeyClaimants reports %d declaration(s) for %s; std declares it twice "+
			"(inherent `pub host fn` plus `impl Display for Bytes`), so either the walk "+
			"stopped seeing one of them or std changed and this control needs a new witness",
			n, witness)
	}
	// And the direction that would rot silently: a key the instantiation DOES separate
	// must report exactly one claimant, so the reason could not be claimed for
	// it truthfully.
	const separated = "calendar.NaiveDateTime.Add<Duration, NaiveDateTime>.add"
	if n := claimants[separated]; n != 1 {
		t.Errorf("stdKeyClaimants reports %d declaration(s) for %s, want exactly 1; "+
			"the instantiation is what separates the eleven `impl Add<X, NaiveDateTime>` blocks",
			n, separated)
	}
}

// TestStdlibHostFuncsAreReachable pins that every registry row actually resolves
// through a call spelling, which is a different claim from the row being
// well-formed: a row for a function nobody can name would pass the test above
// and serve nothing.
func TestStdlibHostFuncsAreReachable(t *testing.T) {
	idx := stdlibLowering()
	for key := range stdlibHostFuncs {
		f := idx.byKey[key]
		if f == nil || f.rtCall == "" {
			continue // reported by TestStdlibRegistryMatchesStdSource
		}
		if f.recv == "" {
			// A top-level `host fn` in std is module-private by construction
			// (`strings.string_compare`, `float.float_bits`); it is reachable
			// only from inside its own module, through the sibling table.
			continue
		}
		// stdPick, not a bare lookup: the claim is that a call site supplying
		// THIS declaration's parameter kinds selects THIS declaration, which is
		// what reachability means once a spelling can name several.
		if got := stdPick(idx.byType[f.recv+"."+f.name], f.params); got != f {
			t.Errorf("%s is not reachable as %s.%s", key, f.recv, f.name)
		}
	}
}

// TestStdlibIndexKeysCarryTheirReceiver guards the receiver the stdlib index
// files each declaration under.
//
// # What it prevents
//
// A receiver read that handles *ast.SimpleType only would answer "" for a
// GENERIC receiver (`impl Equatable for List<T>`) and a QUALIFIED one
// (`impl Display for Json.Case`), filing their methods under the shape of a
// FREE function (`lists.head`, `json.to_json`), which no call site can spell.
//
// Worse, `add` files every declaration under `byKey[f.key]` with a plain
// assignment, so a receiverless key would SILENTLY ABSORB every declaration
// that mapped onto it: `json.to_json` would hold `List`, `Map` and `Maybe`'s
// three separate impls, `channels.inspect` `Channel`, `Receiver` and
// `Sender`'s, and so on. TestEveryScalarHostFnIsBoundOrDeclared iterates
// `byKey`, and a map overwrite leaves no trace, so that guard would report
// completeness over a set that had quietly lost members.
//
// # Why these two assertions and not a count
//
// A total (`byKey should hold N`) needs editing every time std grows, which
// makes it churn, and churn gets bumped reflexively. It also cannot say WHICH
// declaration was lost. Both assertions below are invariant under std growing
// and both name the colliding declarations.
//
// The witness table is the non-circular anchor. The derived half computes the
// receiver with the same function the index does, so on its own it could agree
// with a wrong answer; the witnesses are literal strings nobody derives, and
// they cover a generic receiver, a qualified one, an interface-declared
// `host fn` and three impls of one method that a receiverless key would merge.
func TestStdlibIndexKeysCarryTheirReceiver(t *testing.T) {
	witnesses := map[string]string{
		// Generic receivers. A SimpleType-only receiver read would file these
		// as `lists.head` / `maps.keys` / `sets.size`.
		"lists.List.head":       "List",
		"lists.List.equal?":     "List",
		"iter.Iter.map":         "Iter",
		"maps.Map.keys":         "Map",
		"sets.Set.size":         "Set",
		"vectors.Vector.length": "Vector",
		"ranges.Range.from":     "Range",
		"maybe.Maybe.some?":     "Maybe",
		"results.Result.ok?":    "Result",
		"channels.Sender.send":  "Sender",
		"tasks.Task.spawn":      "Task",
		"random.Generator.int":  "Generator",
		// A QUALIFIED receiver.
		"json.Json.Case.to_string": "Json.Case",
		// Three impls of one interface method, which a receiverless key would
		// merge into one `json.to_json` entry.
		"json.List.to_json":  "List",
		"json.Map.to_json":   "Map",
		"json.Maybe.to_json": "Maybe",
		// A `host fn` DEFAULT declared inside an interface, which the walk
		// reaches through its InterfaceDef arm. The interface name is the
		// receiver, which is what makes this the declaration's host key.
		"structs.Struct.update": "Struct",
		// A control: a SimpleType receiver, so a change that broke the
		// ordinary case fails this test too.
		"int.Int.to_string": "Int",
	}
	idx := stdlibLowering()
	for key, recv := range witnesses {
		f := idx.byKey[key]
		if f == nil {
			t.Errorf("byKey has no %q; std declares it on receiver %s", key, recv)
			continue
		}
		if f.recv != recv {
			t.Errorf("byKey[%q] is filed on receiver %q, want %q", key, f.recv, recv)
			continue
		}
		if got := stdPick(idx.byType[recv+"."+f.name], f.params); got != f {
			t.Errorf("%s is not reachable as the spelling a call site writes, %s.%s", key, recv, f.name)
		}
	}

	// Derived, and the two directions are different claims.
	lib := std.Load()
	modules := make([]string, 0, len(lib.Nodes))
	for name := range lib.Nodes {
		modules = append(modules, name)
	}
	sort.Strings(modules)
	claimants := map[string]map[string]bool{}
	for _, module := range modules {
		anchors := stdAnchorsOf(lib.Files[module])
		for _, c := range collectStdCandidates(module, lib.Nodes[module], anchors) {
			f := c.f
			// One: the entry this declaration's key resolves to must BE a
			// declaration on this receiver. That is the overwrite check, and it
			// is not circular — it compares the key's occupant against the
			// receiver the walk read, so an absorbed declaration fails here.
			if got := idx.byKey[f.key]; got == nil || got.recv != f.recv {
				filed := "nothing"
				if got != nil {
					filed = "a declaration on receiver " + quoted(got.recv)
				}
				t.Errorf("std/%s declares %s on receiver %s, but byKey[%q] holds %s",
					module, f.name, quoted(f.recv), f.key, filed)
			}
			if claimants[f.key] == nil {
				claimants[f.key] = map[string]bool{}
			}
			claimants[f.key][f.recv] = true
		}
	}
	// Two: no key may be claimed by two DIFFERENT receivers. Stated over
	// receivers rather than over declarations because std legitimately declares
	// one name twice for ONE receiver — `bytes.Bytes.to_string` (inherent plus
	// `impl Display`) and `decimal.Decimal.divide` (inherent plus
	// `impl Divide`) — and markAmbiguous already reports that pair as ambiguous
	// rather than ordering it. Two different receivers under one key is the
	// collapse and nothing else, so this needs no allowlist and no maintenance.
	for key, recvs := range claimants {
		if len(recvs) < 2 {
			continue
		}
		names := make([]string, 0, len(recvs))
		for r := range recvs {
			names = append(names, quoted(r))
		}
		sort.Strings(names)
		t.Errorf("byKey[%q] is claimed by %d different receivers (%s); one of them is being silently overwritten",
			key, len(recvs), strings.Join(names, ", "))
	}
}

// TestCollapsedStdlibKeysCostOnlyCoverage is the invariant that makes a
// collapsed spelling SAFE rather than merely known.
//
// # Where the seam is
//
// `stdKey` carries the interface's instantiation, so the builtin key separates
// the eleven `impl Add<X, NaiveDateTime>` blocks, and the residual key
// collapse is only the cases a type argument cannot separate.
//
// De-collapsing the KEY does not de-collapse the SPELLING, and this is the part
// a reader has to get right: `NaiveDateTime.add` is what a call site writes, and
// it names eleven declarations. So the fence is at byType and byIface, which
// are overload SETS selected by the full parameter-kind signature.
//
// # The claim
//
// Two declarations a call site CAN tell apart (different parameter kinds) must
// each route to themselves. Two it CANNOT (identical parameter kinds) must both
// be refused, so no site is routed to whichever won a map assignment. Return
// types are not a discriminator: Nomi has no return-type overloading.
//
// # ONE LICENSED EXCEPTION, AND IT IS ASSERTED POSITIVELY RATHER THAN HOLED
//
// A pair a CALL SITE cannot tell apart may still be one the SOURCE decided.
// `analysis/type_method_identity.go`'s preferTypeMethodSymbol: "Inherent
// (`impl Type { pub fn f }`) beats interface-impl (`impl Iface for Type
// { fn f }`), the same way the per-file table resolves the clash. Not a
// duplicate: the source said which one wins." So for an INHERENT-versus-
// INTERFACE-IMPL group the winner may lower, and this test's job changes from
// "both refused" to three assertions that are strictly stronger than that over
// the group:
//
//	the WINNER is the inherent one          — not whichever won a map assignment
//	every LOSER is refused, under `shadowedByInherent` — its own reason, not silence
//	stdPick answers with the WINNER         — selection agrees with the rule
//
// A group with two inherents, or two impls, is still an ambiguity and still
// requires every member refused. So the exception is exactly as wide as the
// front end's rule and no wider, and the third assertion is what makes it safe:
// a `stdPick` that returned the first arrival would pass a plain "nothing
// lowers" check and fails this one.
//
// Asserted over the built index rather than an allowlist, so it stays true as
// std grows and names the offender when it stops being true. It FATALS if the
// indistinguishable population empties, so fixing the remaining cases rewrites
// this guard rather than silently disarming it.
func TestCollapsedStdlibKeysCostOnlyCoverage(t *testing.T) {
	idx := stdlibLowering()

	// The residual BUILTIN-key collapse: a key several declarations answer to,
	// which with an instantiated stdKey means an interface that takes no type arguments
	// (`bytes.Bytes.to_string`, inherent plus `impl Display`). byKey holds one
	// entry by construction, so the claimants have to be read from source.
	//
	// TWO claims here. `byKey` is `map[string]*stdFunc` with a plain
	// assignment, so a multiply-claimed key ABSORBS its losers silently, and
	// TestEveryScalarHostFnIsBoundOrDeclared iterates `byKey`, so it asks its
	// completeness question of fewer keys than there are declarations.
	// Bounding that by argument is not enough when the argument is "the
	// absorbed one happens to be non-scalar", so the absorbed set is EVALUATED
	// against that guard's own predicate.
	claimants := stdKeyClaimantList(std.Load())
	keyCollapsed, absorbed := 0, 0
	for key, fs := range claimants {
		if len(fs) < 2 {
			continue
		}
		keyCollapsed++
		// A SURVIVOR MAY LOWER ONLY WHEN THE SOURCE DECIDED THE CONTEST, and
		// the condition is checked rather than assumed. `stdPrecedenceWinner`
		// answers non-nil for exactly one shape — one inherent declaration
		// against interface-impl siblings, the front end's own rule — and it is
		// then asserted that the survivor IS that winner. So a lowering
		// survivor produced by a plain map assignment fails here.
		if f := idx.byKey[key]; f != nil && f.lowerable() {
			winner := stdPrecedenceWinner(fs)
			switch {
			case winner == nil:
				t.Errorf("byKey[%q] is claimed by %d declarations that no precedence rule separates, "+
					"and the surviving entry LOWERS (rtCall=%q body=%v); a call site would be "+
					"routed to whichever one won a map assignment",
					key, len(fs), f.rtCall, f.body)
			case !sameStdDecl(f, winner):
				t.Errorf("byKey[%q] LOWERS but the surviving entry is not the declaration the "+
					"inherent-beats-interface-impl rule selects (survivor iface=%q, winner iface=%q); "+
					"the key is routing by arrival rather than by the source's own answer",
					key, f.iface, winner.iface)
			}
		}
		// One survives; the rest are absorbed. The absorbed ones are what this
		// claim is about — they are invisible to
		// TestEveryScalarHostFnIsBoundOrDeclared, which iterates `byKey`.
		//
		// The survivor is not a coin flip: `add` keeps the declaration that
		// IS a `host fn` (a `host fn` is never displaced by a Nomi body), so
		// the survivor is determined and is visible to that guard by
		// construction. Checking it here as well would demand the other
		// guard's question be answered TWICE for one declaration, in two
		// places, with only one of them consulting the `unbound` map.
		absorbed += len(fs) - 1
		survivor := idx.byKey[key]
		for _, f := range fs {
			if f.key == key && survivor != nil && sameStdDecl(f, survivor) {
				continue
			}
			if needsABindingDecision(f) {
				t.Errorf("byKey[%q] is claimed by %d declarations and an ABSORBED one (%s on receiver %s, "+
					"refused %q) would have needed a BINDING DECISION; a key collapse must only ever "+
					"absorb declarations TestEveryScalarHostFnIsBoundOrDeclared would have skipped anyway, "+
					"or that guard is reporting completeness over a set that quietly lost members",
					key, len(fs), f.name, quoted(f.recv), f.why)
			}
		}
	}

	sets, indistinguishable, decided := 0, 0, 0
	check := func(where string, fs []*stdFunc) {
		if len(fs) < 2 {
			return
		}
		sets++
		bySig := map[string][]*stdFunc{}
		order := make([]string, 0, len(fs))
		for _, f := range fs {
			// GROUPED BY IDENTITY, not by Nomi spelling. `nomiKinds` renders a
			// kind's SHORT name, so `calendar.Error` and `random.Error` both
			// print `(Error)`. The two kinds are DISTINCT (different `*typeDef`)
			// and `stdPick`, which compares actual kinds through
			// `sameParamKinds`, routes each to itself, so grouping by spelling
			// would report this guard's own display collapse as a routing
			// defect.
			//
			// The identity is the kind VALUE, which is comparable and is what
			// stdPick compares. Rendered alongside for the message, because a
			// pointer in a failure tells a reader nothing.
			sig := stdSigIdentity(f.params)
			if _, seen := bySig[sig]; !seen {
				order = append(order, sig)
			}
			bySig[sig] = append(bySig[sig], f)
		}
		sort.Strings(order)
		for _, sig := range order {
			group := bySig[sig]
			if len(group) == 1 {
				// Distinguishable: the positive half of the claim, and the half
				// the overload sets bought. A call site supplying these kinds
				// must reach THIS declaration and no sibling.
				if got := stdPick(fs, group[0].params); got != group[0] {
					t.Errorf("%s: %s takes %s but selection answered %v; "+
						"a distinguishable overload must route to itself",
						where, group[0].key, sig, describeStdFunc(got))
				}
				continue
			}
			indistinguishable++
			// PRECEDENCE-DECIDED, or a genuine ambiguity. The three assertions
			// over the first case are strictly stronger than "all refused":
			// the winner is the INHERENT one, every loser carries its own
			// reason, and selection answers with the winner.
			if winner := stdPrecedenceWinner(group); winner != nil {
				decided++
				if got := stdPick(fs, winner.params); got != winner {
					t.Errorf("%s: %d declarations take %s and the inherent one is %q, but "+
						"selection answered %v; the front end's inherent-beats-interface-impl "+
						"rule and this table now disagree",
						where, len(group), sig, winner.key, describeStdFunc(got))
				}
				for _, f := range group {
					if f == winner {
						continue
					}
					// A REASON, and `shadowedByInherent` only when the loser
					// had none of its own. markAmbiguous deliberately does not
					// overwrite a refusal a function already carried — the
					// specific reason is the useful one — so a loser that is
					// also non-scalar keeps saying so, and demanding this exact
					// string would be asserting markAmbiguous's precedence
					// backwards.
					if f.why == "" {
						t.Errorf("%s: %q lost the inherent/interface-impl contest and carries NO "+
							"reason; a declaration the source ruled against must say so, or it is "+
							"invisible to every guard that asks what refused",
							where, f.key+" ("+f.iface+")")
					}
					if f.lowerable() {
						t.Errorf("%s: %q lost the contest and LOWERS anyway (rtCall=%q body=%v); "+
							"both halves share one stdKey, so two bodies would take one Go name",
							where, f.key, f.rtCall, f.body)
					}
				}
				continue
			}
			for _, f := range group {
				if f.lowerable() {
					t.Errorf("%s: %d declarations take %s and %q LOWERS (rtCall=%q body=%v); "+
						"a call site supplying those kinds would be routed to whichever one won",
						where, len(group), sig, f.key, f.rtCall, f.body)
				}
			}
		}
	}
	spellings := make([]string, 0, len(idx.byType))
	for spelling := range idx.byType {
		spellings = append(spellings, spelling)
	}
	sort.Strings(spellings)
	for _, spelling := range spellings {
		check("byType["+spelling+"]", idx.byType[spelling])
	}
	ifaceKeys := make([]string, 0, len(idx.byIface))
	for key := range idx.byIface {
		ifaceKeys = append(ifaceKeys, key)
	}
	sort.Strings(ifaceKeys)
	for _, key := range ifaceKeys {
		byRecv := idx.byIface[key]
		recvs := make([]string, 0, len(byRecv))
		index := map[string]kind{}
		for recv := range byRecv {
			recvs = append(recvs, recv.nomi())
			index[recv.nomi()] = recv
		}
		sort.Strings(recvs)
		for _, name := range recvs {
			check("byIface["+key+"] at "+name, byRecv[index[name]])
		}
	}

	if indistinguishable == 0 {
		t.Fatal("no std spelling holds two declarations with the same parameter kinds, so this " +
			"guard asserted nothing about routing. Either the last indistinguishable pair was " +
			"resolved, in which case restate this over the empty set, keeping the positive half, " +
			"or the walk stopped seeing impl blocks.")
	}
	// The EXCEPTION's own anti-vacuity guard, and it is the more fragile of the
	// two: exactly one spelling in std has an inherent/interface-impl collision
	// (`Bytes.to_string`; the other same-signature collisions are `Error.inspect`
	// and `Error.to_string`, each two modules' `Error`, which is same-class). If
	// a std edit removes it, the three assertions above assert NOTHING and would
	// go on passing.
	if decided == 0 {
		t.Fatal("no std spelling holds an inherent declaration against an interface-impl one " +
			"with the same parameter kinds, so the inherent-beats-interface-impl assertions above " +
			"ran over nothing. `Bytes.to_string` is the only such spelling; if std removed it, " +
			"either restate this over a synthetic index or delete addOverload's precedence arm " +
			"along with this clause — do not leave a rule nothing exercises.")
	}
	t.Logf("%d builtin key(s) still claimed by more than one declaration, absorbing %d declaration(s), "+
		"none of which needed a binding decision; %d overload set(s), holding %d indistinguishable "+
		"group(s): %d decided by the inherent-beats-interface-impl rule, %d refused as ambiguous",
		keyCollapsed, absorbed, sets, indistinguishable, decided, indistinguishable-decided)
}

// quoted renders a receiver name for a diagnostic, naming the receiverless case
// rather than printing an empty pair of quotes nobody can read.
func quoted(recv string) string {
	if recv == "" {
		return "(none — filed as a free function)"
	}
	return `"` + recv + `"`
}

// TestStdlibRefusalNamesAreDistinct keeps the blocker tally usable.
//
// The constructs the stdlib index can report are separate keys on purpose, and a
// change that collapsed two of them would make the tally read as one big gap.
// Listed here so the set is visible in one place and a further name cannot
// arrive without a reader noticing.
func TestStdlibRefusalNamesAreDistinct(t *testing.T) {
	want := []string{
		"ambiguous stdlib impl",
		"ambiguous stdlib method",
		"stdlib function outside the scalar subset",
		"stdlib function without a body",
		"stdlib function reaching the front end",
		"stdlib generic function",
		// A `host fn` carrying a declared parameter default. Kept apart from
		// `stdlib host function` because the two are not the same amount of
		// work in the same direction: that one is waiting on an rt symbol, and
		// this one is waiting on a DECISION about which side owns the default's
		// value — a crossing into Go applies none, so the Go implementation
		// decides and the declaration is documentation of it. Adding an rt
		// symbol would NOT lower it. See stdCandidateFor.
		"stdlib host function with a defaulted parameter",
		"stdlib host function",
		"stdlib return type mismatch",
		// A std function that lowers, and whose recursion Nomi guarantees to be
		// constant-stack while a stdlib gen has no trampoline to give it. std
		// produces none TODAY and the shape is not hypothetical: `random.draw_list`,
		// `random.pick_weighted`, `random.total_weight` and both `each_while` in
		// std/ranges are tail self-calls masked behind `stdlib generic function`.
		// See stdRecursiveClosure.
		"stdlib tail-recursive function",
		"unlowered stdlib function",
	}
	idx := stdlibLowering()
	seen := map[string]bool{}
	for _, f := range idx.byKey {
		if f.why != "" {
			seen[f.why] = true
		}
	}
	// Every name the walk actually produced must be in the list; a name it does
	// not produce is allowed (it is reachable only from a call site or from a
	// future std edit) but must still be declared here.
	declared := map[string]bool{}
	for _, w := range want {
		declared[w] = true
	}
	var stray []string
	for name := range seen {
		if !declared[name] {
			stray = append(stray, name)
		}
	}
	sort.Strings(stray)
	if len(stray) > 0 {
		t.Fatalf("the stdlib walk reported refusal names this test does not declare: %v", stray)
	}
	if !seen["stdlib host function"] || !seen["stdlib function outside the scalar subset"] {
		t.Fatalf("the two load-bearing refusals were not produced at all; got %v", seen)
	}
}

// stdSigIdentity keys a signature on its kinds' IDENTITY rather than their
// display names, so two same-named types from two modules do not collapse.
//
// A kind is comparable and is exactly what `sameParamKinds` — and therefore
// `stdPick` — compares, so this groups precisely the declarations routing cannot
// tell apart. The Nomi spelling rides along for the failure message.
func stdSigIdentity(ks []kind) string {
	parts := make([]string, 0, len(ks))
	for _, k := range ks {
		parts = append(parts, fmt.Sprintf("%s@%p", k.nomi(), k.def))
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func TestStdlibRegistryMatchesStdSource(t *testing.T) {
	idx := stdlibLowering()
	for key, row := range stdlibHostFuncs {
		f, declared := idx.byKey[key]
		if !declared {
			t.Errorf("registry row %q names no stdlib declaration", key)
			continue
		}
		if f.decl != nil {
			t.Errorf("registry row %q names a Nomi-BODIED function; an rt symbol would shadow the body", key)
			continue
		}
		if f.rtCall != row.call {
			t.Errorf("registry row %q: signature disagrees with the declaration in std, so it was refused as %q (want rt call %s, params %s/%s, result %s/%s)",
				key, f.why, row.call, registryKinds(row.params), registryKinds(f.params), row.result.nomi(), f.result.nomi())
		}
	}
}

// registryKinds renders a signature's kinds by their Nomi spelling.
func registryKinds(ks []kind) string {
	parts := make([]string, 0, len(ks))
	for _, k := range ks {
		parts = append(parts, k.nomi())
	}
	return "(" + strings.Join(parts, ", ") + ")"
}
