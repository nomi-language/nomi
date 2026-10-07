package analysis_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// The defect these guard, stated once.
//
// `ProjectImplIndex.TypeMethods` is keyed by the receiver's BARE name, so every
// module declaring a type called `Error` deposits its `Error.to_string` into one
// slot and the union loop's `if !present` over a Go map range decides which
// survives. Go randomizes map iteration, so the analyzer answered `Type.method`
// differently for identical source on different runs of one binary — measured
// flipping between two stdlib modules' `Error` while checking a
// call whose receiver was std/calendar's.
//
// The fix is the identity KEY, not a stabilised order: sorting the union would
// pick one every time and leave the others unreachable.
// `TypeMethodKey` carries the receiver's Origin, which makes the collapse
// unrepresentable rather than merely detected.
//
// So these tests assert the KEY, not just an outcome. A determinism test alone
// — resolve N times, expect one answer — passes if the collapse survives and
// merely stops varying, which is the wrong fix passing the test for the bug.

// wantStdlibErrorOrigins are the two stdlib modules that declare a type named
// `Error`, as their build keys — MEASURED, not read off the file layout:
// std/random lives at std/random.nomi and its build key is still
// "std/random". Named explicitly rather than discovered, because "two modules
// declare Error" is the precondition that makes any of this reachable: if one is
// renamed or loses its `Error`, this must fail and be re-derived rather than
// quietly measure a smaller population.
var wantStdlibErrorOrigins = []string{"std/calendar", "std/random"}

// originlessStdlibReceivers are the stdlib receiver names that legitimately have
// NO identity entry, so they keep resolving through the bare table.
//
// EMPTY. A generic `host type` such as `List`, `Map` or `Task` can carry an
// Origin: `buildExternTypeShell` returns a shared `*PrimitiveType` only for
// the NON-generic case, and for a generic one it builds a fresh
// `*DistinctType` per declaration, which the `*ast.ExternType` arm of
// buildTypeShellInScope stamps with `declaredOrigin(fa)`.
//
// The list is closed and asserted exactly, not as an upper bound. A new entry
// now means a real type that stopped resolving, which is the only remaining
// reading, and only an exact assertion says so.
var originlessStdlibReceivers = []string{}

// TestTypeMethodIdentity_TwoStdlibErrorsOccupyTwoSlots is the structural
// half: with the module in the key, the two same-named receivers cannot share
// a slot. Under the bare key exactly ONE of these two lookups could succeed.
func TestTypeMethodIdentity_TwoStdlibErrorsOccupyTwoSlots(t *testing.T) {
	idx := stdlibProjectIndex(t)

	seen := map[*analysis.Symbol]string{}
	for _, origin := range wantStdlibErrorOrigins {
		sym := idx.LookupTypeMethodByIdentity(origin, "Error", "to_string")
		if sym == nil {
			t.Fatalf("no Error.to_string for origin %q; the stdlib Errors are collapsing into one slot", origin)
		}
		if prior, dup := seen[sym]; dup {
			t.Errorf("origins %q and %q resolved Error.to_string to the SAME symbol (%s:%d) — the key is not separating them",
				prior, origin, sym.SourceFile, sym.Pos.Line)
			continue
		}
		seen[sym] = origin
	}
}

// TestTypeMethodIdentity_EveryKeyCarriesAnOrigin is the assertion that makes
// nondeterminism unrepresentable rather than unobserved: no entry may be keyed
// by a bare name. It also fails on the EMPTY table, so a pass cannot come from
// the pass never running — which is exactly how a guard over an
// identity-establishing map degrades into a no-op (the `InterfaceType.Origin`
// precedent: a field present and populated, but inert because resolution never
// reached it).
func TestTypeMethodIdentity_EveryKeyCarriesAnOrigin(t *testing.T) {
	idx := stdlibProjectIndex(t)

	if len(idx.TypeMethodByIdentity) == 0 {
		t.Fatal("identity table is empty — the populate pass did not run, so every other assertion here is vacuous")
	}
	// Then the coverage: every bare (receiver, method) pair must be reachable
	// by identity EXCEPT the origin-less host types, named exactly. A
	// pass-that-populates-nothing fails on the emptiness check above; a
	// pass-that-populates-half fails here.
	//
	// With originlessStdlibReceivers now EMPTY, this asserts total coverage,
	// and total coverage is exactly the shape that could pass for the wrong
	// reason: no fallbacks because no receivers. So the bare table's own
	// non-emptiness is asserted first.
	if len(idx.TypeMethods) == 0 {
		t.Fatal("the bare receiver table is empty — a total-coverage result here " +
			"would be vacuous, since there is nothing that could fall back")
	}
	byPair := map[[2]string]bool{}
	for key := range idx.TypeMethodByIdentity {
		byPair[[2]string{key.Type, key.Method}] = true
	}
	fellBack := map[string]bool{}
	for typeName, byMethod := range idx.TypeMethods {
		for method := range byMethod {
			if !byPair[[2]string{typeName, method}] {
				fellBack[typeName] = true
			}
		}
	}
	got := make([]string, 0, len(fellBack))
	for name := range fellBack {
		got = append(got, name)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(originlessStdlibReceivers, ",") {
		t.Errorf("receivers falling back to the bare table changed.\nwant: %v\ngot:  %v\n"+
			"A NEW name is either a new origin-less host type (extend the list, after checking no second module declares it) "+
			"or a real type that stopped resolving (a regression).", originlessStdlibReceivers, got)
	}
	for key := range idx.TypeMethodByIdentity {
		if key.Origin == "" {
			t.Errorf("entry keyed with no origin: %s.%s — a bare key readmits the collapse", key.Type, key.Method)
		}
		if key.Type == "" || key.Method == "" {
			t.Errorf("malformed identity key: %+v", key)
		}
	}
}

// TestTypeMethodIdentity_NoConflictingDuplicates reads the duplicate-insertion
// guard. With the module in the key, two different symbols contending for one
// key is a genuine duplicate declaration rather than the name collision the
// bare table suffered, so the expected population is empty — and the test above
// is what stops an empty population from being a vacuous pass.
func TestTypeMethodIdentity_NoConflictingDuplicates(t *testing.T) {
	idx := stdlibProjectIndex(t)

	if len(idx.TypeMethodIdentityConflicts) == 0 {
		return
	}
	rendered := make([]string, 0, len(idx.TypeMethodIdentityConflicts))
	for _, key := range idx.TypeMethodIdentityConflicts {
		rendered = append(rendered, key.Origin+"."+key.Type+"."+key.Method)
	}
	sort.Strings(rendered)
	t.Errorf("two distinct symbols contended for a fully-qualified type-method key: %s", strings.Join(rendered, ", "))
}

// TestTypeMethodIdentity_TwoModulesEachResolveTheirOwnMethod is the language
// half, in the shape a name-keyed table collapses: two sibling modules each
// declaring `Error` with its own `impl Display`, both reachable from one entry,
// each called type-qualified.
//
// Without the fix, in 8 runs of 8: this project does not type-check, and it
// fails the same way every time. Whichever module won the slot, the OTHER call
// received that module's `Error` as its parameter type and was refused
// `argument 1: expected Error, got Error` — a TRUE statement (TypesEqual
// compares (Origin, Name), and the two really are different types) about a call
// that should be accepted. With exactly two candidates one of them is always
// wrong, which is why the outcome is stable even though the winner is not.
func TestTypeMethodIdentity_TwoModulesEachResolveTheirOwnMethod(t *testing.T) {
	errs := buildProjectExpectingErrors(t, `import {
  alpha
  beta
}

fn demo(): String {
  a = alpha.Error.Bad("a")
  b = beta.Error.Worse("b")
  alpha.Error.to_string(a) + beta.Error.to_string(b)
}
`, map[string]string{
		"alpha": `pub enum Error {
  Bad String
}

impl Display for Error {
  fn to_string(e: Error): String {
    case e {
      Error.Bad(s) -> "alpha: " + s
    }
  }
}
`,
		"beta": `pub enum Error {
  Worse String
}

impl Display for Error {
  fn to_string(e: Error): String {
    case e {
      Error.Worse(s) -> "beta: " + s
    }
  }
}
`,
	})

	for _, e := range errs {
		if e.Code == analysis.UnusedBindingCode || e.Message == "" {
			continue
		}
		t.Errorf("two same-named types from two modules did not both resolve: %s", e.Message)
	}
}

// TestTypeMethodIdentity_MismatchNamesBothDeclaringModules is the crossed
// direction: alpha's `Error` handed to beta's `to_string`, which must be
// refused, and refused in words a reader can act on.
//
// Without the fix, this program produced NO DIAGNOSTIC AT ALL. So the
// before-state was not merely an illegible refusal — the crossed call was
// silently ACCEPTED, because both spellings resolved through the one collapsed
// slot to the same method and the argument then matched its own module's
// parameter type. Two same-named types from two modules were interchangeable.
//
// The message half alone is the trap: a legible message over a wrong resolution
// looks complete precisely because it finally makes sense. Mutation-checked —
// with `LookupTypeMethodByIdentity` neutered and this message intact, the
// sibling test above fails reporting `expected alpha.Error, got beta.Error`,
// which is perfectly legible and perfectly wrong. So this test is worthless on
// its own and is paired with that one.
func TestTypeMethodIdentity_MismatchNamesBothDeclaringModules(t *testing.T) {
	errs := buildProjectExpectingErrors(t, `import {
  alpha
  beta
}

fn demo(): String {
  a = alpha.Error.Bad("a")
  beta.Error.to_string(a)
}
`, map[string]string{
		"alpha": `pub enum Error {
  Bad String
}

impl Display for Error {
  fn to_string(e: Error): String {
    case e {
      Error.Bad(s) -> "alpha: " + s
    }
  }
}
`,
		"beta": `pub enum Error {
  Worse String
}

impl Display for Error {
  fn to_string(e: Error): String {
    case e {
      Error.Worse(s) -> "beta: " + s
    }
  }
}
`,
	})

	const want = "argument 1: expected beta.Error, got alpha.Error"
	for _, e := range errs {
		if e.Message == want {
			return
		}
	}
	got := make([]string, 0, len(errs))
	for _, e := range errs {
		if e.Message != "" {
			got = append(got, e.Message)
		}
	}
	t.Fatalf("expected the mismatch to name both declaring modules.\nwant: %s\ngot:  %s", want, strings.Join(got, " | "))
}

// stdlibProjectIndex builds the real project index over the whole stdlib, via
// the same BuildProject path `nomi run` takes, and returns its ProjectImplIndex.
// The stdlib is the only place same-named receivers actually exist, so a
// synthetic index would not exercise the collapse.
func stdlibProjectIndex(t *testing.T) *analysis.ProjectImplIndex {
	t.Helper()
	tmp := t.TempDir()
	entry := filepath.Join(tmp, "main.nomi")
	// Import both so nothing here depends on the entry's import closure;
	// the index covers every analyzed file regardless, and naming them makes
	// that independence explicit rather than incidental.
	src := `import {
  std/calendar
  std/random
}

fn main(): Unit { Unit }
`
	if err := os.WriteFile(entry, []byte(src), 0644); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	tokens := lexer.Lex(src)
	entryNodes, _ := parser.ParseWithRecovery(tokens)
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		path := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		fileTokens := lexer.Lex(string(data))
		nodes, _ := parser.ParseWithRecovery(fileTokens)
		return nodes, nil
	}
	lib := std.Load()
	fa := analysis.BuildProject(entryNodes, lib.Primitives, lib.Modules, lib.Files, tmp, loader)
	if fa == nil || fa.ProjectImpls == nil {
		t.Fatal("no project impl index")
	}
	return fa.ProjectImpls
}
