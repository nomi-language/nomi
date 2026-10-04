package analysis_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// TestStdlibOperatorOutputIsDeterminedByReceiverAndRhs holds the invariant
// that lets the impl-coherence key leave out an operator impl's output type.
//
// For every operator impl the stdlib ships, it groups by
// (interface, receiver, right-hand type) and requires each group to hold at
// most one output type. A group of two would be a stdlib program the
// coherence key rejects.
//
// It reads the same index `detectImplCollisions` reads (ProjectImplIndex:
// IfaceMethodImpls for the population, ImplBlockInterfaceKey for the header,
// ImplBlockReceiver for the receiver) through the real BuildProjectWithCache
// pipeline, so it cannot disagree with the check the way a source-text grep
// can: a grep misses `where`-clause headers, multi-line headers and every
// synthesized impl.
//
// A violating group appears in the failure list with its receiver,
// right-hand type and both output types. The test reports `groups` and
// `impls` separately; `impls > groups` is the condition it flags. With `-v`
// it prints the counts.
func TestStdlibOperatorOutputIsDeterminedByReceiverAndRhs(t *testing.T) {
	idx := stdlibProjectImplIndex(t)

	type site struct {
		out    string
		header string
		recv   string
	}
	groups := map[string][]site{}
	impls := 0
	perInterface := map[string]int{}

	for _, iface := range []string{"Add", "Subtract", "Multiply", "Divide"} {
		byMethod := idx.IfaceMethodImpls[iface]
		for _, fns := range byMethod {
			for _, fn := range fns {
				header := idx.ImplBlockInterfaceKey[fn]
				if header == "" {
					continue
				}
				recv := idx.ImplBlockReceiver[fn]
				if recv == "" {
					continue
				}
				rhs := analysis.OperatorInterfaceRhs(header)
				out := operatorOutputArg(header)
				impls++
				perInterface[iface]++
				key := iface + " " + recv + " + " + rhs
				groups[key] = append(groups[key], site{out: out, header: header, recv: recv})
			}
		}
	}

	if impls == 0 {
		t.Fatal("no stdlib operator impls reached the project index, so this test measured nothing — " +
			"the index shape or the interface names changed")
	}

	var violations []string
	for key, sites := range groups {
		outs := map[string]bool{}
		for _, s := range sites {
			outs[s.out] = true
		}
		if len(outs) < 2 {
			continue
		}
		headers := make([]string, 0, len(sites))
		for _, s := range sites {
			headers = append(headers, s.header)
		}
		sort.Strings(headers)
		violations = append(violations, fmt.Sprintf("%s -> %d outputs: %s", key, len(outs), strings.Join(headers, ", ")))
	}
	sort.Strings(violations)

	names := make([]string, 0, len(perInterface))
	for n := range perInterface {
		names = append(names, n)
	}
	sort.Strings(names)
	counts := make([]string, 0, len(names))
	for _, n := range names {
		counts = append(counts, fmt.Sprintf("%s=%d", n, perInterface[n]))
	}
	t.Logf("stdlib operator impls: %d in %d (interface, receiver, rhs) groups [%s]",
		impls, len(groups), strings.Join(counts, " "))

	if len(violations) > 0 {
		t.Fatalf("the stdlib varies an operator's OUTPUT type at a fixed (receiver, right-hand type), "+
			"so the coherence key cannot drop the output without rejecting the stdlib. "+
			"Either the key change is wrong or these impls are:\n  %s",
			strings.Join(violations, "\n  "))
	}
	if impls != len(groups) {
		t.Fatalf("%d operator impls fell into %d groups; every group is singleton in the tree this "+
			"test was written against, and a non-singleton group with ONE output is a plain duplicate "+
			"impl that detectImplCollisions should already have reported", impls, len(groups))
	}
}

// TestStdlibExternOperatorImplsAreUncheckedButCollisionFree guards a gap in
// detectImplCollisions.
//
// Many of std's operator impl items are `host fn`, so they are
// `*ast.ExternFunc` nodes and land in IfaceMethodImplExterns, a parallel
// index that detectImplCollisions does not walk. `std/calendar`'s
// `impl Add<Years, Date> for Date { host fn add(...) }` is one of them. Extern
// impl items take dispatch slots exactly like `fn` impls, so two extern impls
// sharing a dispatch key would collide with no compile-time check in front of
// them.
//
// The check does not cover the extern index because the FuncDef side is keyed
// on the qualified receiver (`calendar.Date`, filled by
// PopulateQualifiedReceivers), while ImplBlockReceiverExtern holds bare names.
// Extending the check to it as it stands would reject two extern impls for two
// same-named types in different modules. Closing the gap means qualifying
// extern receivers first.
//
// Until then this test guards the absence: a stdlib edit that introduces an
// extern operator collision fails here even though the analyzer would accept
// it.
func TestStdlibExternOperatorImplsAreUncheckedButCollisionFree(t *testing.T) {
	idx := stdlibProjectImplIndex(t)

	groups := map[string][]string{}
	externs := 0
	for _, iface := range []string{"Add", "Subtract", "Multiply", "Divide"} {
		for method, exts := range idx.IfaceMethodImplExterns[iface] {
			for _, ext := range exts {
				header := idx.ImplBlockInterfaceKeyExtern[ext]
				recv := idx.ImplBlockReceiverExtern[ext]
				if header == "" || recv == "" {
					continue
				}
				externs++
				key := iface + "." + method + " " + recv + " + " + analysis.OperatorInterfaceRhs(header)
				groups[key] = append(groups[key], header)
			}
		}
	}

	t.Logf("stdlib EXTERN operator impls (invisible to detectImplCollisions): %d in %d "+
		"(interface, method, receiver, rhs) groups", externs, len(groups))
	if externs == 0 {
		t.Fatal("no extern operator impls reached the index. Either the stdlib stopped writing " +
			"`host fn` operator items — in which case this hole is closed and the test should say " +
			"so — or the extern index shape changed and this measured nothing")
	}

	var collisions []string
	for key, headers := range groups {
		if len(headers) < 2 {
			continue
		}
		sort.Strings(headers)
		collisions = append(collisions, key+": "+strings.Join(headers, ", "))
	}
	sort.Strings(collisions)
	if len(collisions) > 0 {
		t.Fatalf("two stdlib EXTERN impls share one dispatch key. detectImplCollisions does not "+
			"walk the extern index, so nothing rejects this at compile time and two bodies "+
			"claim one dispatch slot:\n  %s", strings.Join(collisions, "\n  "))
	}
}

// TestStdlibHasNoNonOperatorInterfaceInstantiations holds the same invariant
// for interface type arguments. The language spec states
// the rule this freezes (docs/spec.md, "Generic Interfaces"): a
// generic interface's type parameter "is *determined* by each implementor (one
// impl per type), so it is never written on the interface name where the
// implementor is already known (`impl Iter for List<T>`, not `impl Iter<T> for
// List<T>`)". Operator interfaces are the deliberate exception, and what earns
// the exception is the right-hand type.
//
// So a NON-operator impl header carrying type arguments is already outside the
// spec, and the stdlib never writes one. One would be listed by name
// below.
func TestStdlibHasNoNonOperatorInterfaceInstantiations(t *testing.T) {
	idx := stdlibProjectImplIndex(t)

	operators := map[string]bool{"Add": true, "Subtract": true, "Multiply": true, "Divide": true}
	var offenders []string
	headers := 0
	for iface, byMethod := range idx.IfaceMethodImpls {
		if operators[iface] {
			continue
		}
		for method, fns := range byMethod {
			for _, fn := range fns {
				header := idx.ImplBlockInterfaceKey[fn]
				if header == "" {
					continue
				}
				headers++
				if !strings.Contains(header, "<") {
					continue
				}
				offenders = append(offenders, fmt.Sprintf("%s for %s (method `%s`)",
					header, idx.ImplBlockReceiver[fn], method))
			}
		}
	}
	sort.Strings(offenders)
	t.Logf("non-operator interface impl headers surveyed: %d", headers)
	if headers == 0 {
		t.Fatal("no non-operator impl headers reached the index, so this test measured nothing")
	}
	if len(offenders) > 0 {
		t.Fatalf("the stdlib writes type arguments on a non-operator interface name, which the "+
			"runtime dispatch key drops entirely — the coherence key now rejects two of these for one "+
			"receiver, so these are the population at risk:\n  %s", strings.Join(offenders, "\n  "))
	}
}

// stdlibProjectImplIndex drives the real BuildProjectWithCache pipeline over a
// trivial entry program and returns the project impl index. The entry's own impl
// set is empty, so everything the index holds came from the stdlib fold.
func stdlibProjectImplIndex(t *testing.T) *analysis.ProjectImplIndex {
	t.Helper()
	lib := std.Load()
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex("fn main() {}"))
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		return nil, nil
	}
	entryFA, _, _ := analysis.BuildProjectWithCache(
		entryNodes, lib.Primitives, lib.Modules, lib.Files, "", loader,
	)
	if entryFA == nil || entryFA.ProjectImpls == nil {
		t.Fatal("BuildProjectWithCache produced no project impl index")
	}
	return entryFA.ProjectImpls
}

// operatorOutputArg reads the SECOND type argument out of an operator impl's
// interface header — the output. It is deliberately local to this test: nothing
// in the compiler reads the output off a header, which is the whole point of the
// change these tests defend, so an exported helper for it would advertise a
// discriminator that does not exist.
func operatorOutputArg(header string) string {
	open := strings.Index(header, "<")
	if open < 0 {
		return ""
	}
	depth, start := 0, open+1
	args := []string{}
	for i := start; i < len(header); i++ {
		switch header[i] {
		case '<':
			depth++
		case '>':
			if depth == 0 {
				args = append(args, strings.TrimSpace(header[start:i]))
				start = i + 1
			} else {
				depth--
			}
		case ',':
			if depth == 0 {
				args = append(args, strings.TrimSpace(header[start:i]))
				start = i + 1
			}
		}
	}
	if len(args) < 2 {
		return ""
	}
	return args[1]
}
