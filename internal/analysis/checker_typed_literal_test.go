package analysis_test

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
	"strings"
	"testing"
)

// checkTypedLiteralSource type-checks a single source string in the
// stdlib's prelude scope and returns every diagnostic the analyzer
// produces — including builder-time errors stored on
// fa.TypeErrors. checkSourceWithStdlib in the sibling stdlib_test file
// drops fa.TypeErrors, but tag-resolution diagnostics live there
// (resolveTaggedStringTag emits them during the builder walk before
// BuildTypes runs), so this helper widens the set explicitly.
//
// Note: the single-file path's ProjectImpls covers only stdlib impls, so
// a LOCAL `impl Literal for T` block isn't resolvable here — tests
// that need a local tag handler use checkTypedLiteralProject (full
// BuildProjectWithCache) instead.
func checkTypedLiteralSource(src string) []analysis.TypeError {
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	lib := std.Load()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	// AttachStdlibProjectImpls — see checker_stdlib_test.go's
	// checkSourceWithStdlib for the rationale (single-file path needs
	// a stdlib ProjectImpls so the unifier sees Int/String/etc.'s
	// stdlib impls).
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	typeErrs := analysis.BuildTypes(fa, nodes)
	checkErrs := analysis.CheckTypes(fa, nodes)
	all := append([]analysis.TypeError{}, fa.TypeErrors...)
	all = append(all, typeErrs...)
	all = append(all, checkErrs...)
	return all
}

// checkTypedLiteralProject type-checks a multi-file project whose
// entry source is `entrySrc` and whose sibling modules are provided as
// a map from module name (last path segment) to source. Mirrors the
// shape used by re_export_facade / effects integration tests but
// shaped for unit-test consumption — every diagnostic from every file
// is returned in a single slice so assertions can target the specific
// failure mode under test.
func checkTypedLiteralProject(t *testing.T, entrySrc string, siblings map[string]string) []analysis.TypeError {
	t.Helper()
	lib := std.Load()
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		key := strings.Join(modulePath, "/")
		src, ok := siblings[key]
		if !ok {
			return nil, nil
		}
		tokens := lexer.Lex(src)
		nodes, _ := parser.ParseWithRecovery(tokens)
		return nodes, nil
	}
	tokens := lexer.Lex(entrySrc)
	entryNodes, _ := parser.ParseWithRecovery(tokens)
	entryFA, cache, _ := analysis.BuildProjectWithCache(
		entryNodes, lib.Primitives, lib.Modules, lib.Files, "/project", loader,
	)
	if entryFA == nil {
		t.Fatalf("BuildProject returned nil")
	}
	all := append([]analysis.TypeError{}, entryFA.TypeErrors...)
	all = append(all, analysis.CheckTypes(entryFA, entryNodes)...)
	for _, sibFA := range cache {
		all = append(all, sibFA.TypeErrors...)
	}
	return all
}

func expectTypedLiteralError(t *testing.T, errs []analysis.TypeError, substr string) {
	t.Helper()
	for _, e := range errs {
		if strings.Contains(diagText(e), substr) {
			return
		}
	}
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	t.Fatalf("expected error containing %q, got %d errors:\n  %s", substr, len(errs), strings.Join(msgs, "\n  "))
}

// Failure mode 1: the tag identifier doesn't resolve to anything in
// scope. The literal is well-formed at the lexer/parser level but the
// analyzer surfaces a precise error pointing at the tag site.
func TestTypedLiteral_TagNotInScope(t *testing.T) {
	src := `fn main() {
  q = Nope"hi"
}`
	errs := checkTypedLiteralSource(src)
	expectTypedLiteralError(t, errs, "tag 'Nope' is not in scope")
}

// Failure mode 2: the tag identifier resolves to a PascalCase name that
// isn't a type (here a prelude interface). A tag must be a type with a
// `Literal` impl; a non-type binding fires a kind-specific
// diagnostic. (A snake_case `nope"..."` never reaches here — it's a
// plain parse error, since typed literals are PascalCase-only.)
func TestTypedLiteral_TagIsAValueNotAType(t *testing.T) {
	src := `fn main() {
  q = Display"hi"
}`
	errs := checkTypedLiteralSource(src)
	expectTypedLiteralError(t, errs, "not a typed-literal tag")
}

// Failure mode 3: the tag resolves to a type with no `Literal`
// impl. The diagnostic surfaces at the tag site instructing the user to
// write `impl Literal for <Type>`.
func TestTypedLiteral_TypeWithoutLiteralImpl(t *testing.T) {
	src := `pub struct Foo { value: Int }
fn main() {
  q = Foo"hi"
}`
	errs := checkTypedLiteralSource(src)
	expectTypedLiteralError(t, errs, "has no typed-literal handler")
}

// Failure mode 4: a Dynamic slot's type doesn't implement the tag's
// per-slot value-type interface. The error must point at the slot
// expression's source location, not the literal as a whole. Here the
// `DisplayTag` handler expects Display on its slots; passing a function
// (no Display impl) into a slot triggers the conformance failure. Uses
// a project build so the LOCAL impl resolves.
func TestTypedLiteral_SlotTypeDoesNotImplementInterface(t *testing.T) {
	entry := `import std/literals.{Fragment, Literal}

pub type DisplayTag

impl Literal for DisplayTag {
  fn from_fragments(fragments: List<Fragment<Display>>): String {
    Iter.reduce(fragments, |acc = "", _frag| { acc })
  }
}

fn helper(x: Int): Int { x + 1 }

fn main() {
  q = DisplayTag"value: ${helper}"
}`
	errs := checkTypedLiteralProject(t, entry, nil)
	expectTypedLiteralError(t, errs, "does not implement 'Display'")
}

// A block-form tag whose type and `impl Literal` block are both in
// scope type-checks clean and the use site reads as a constructor. Uses
// a project build so the local impl resolves.
func TestTypedLiteral_BlockFormTypeAttachedTag(t *testing.T) {
	entry := `import {
  std/io
  std/literals.{Fragment, Literal}
  std/literals.Fragment.{Dynamic, Static}
}

pub struct Box { contents: String }

impl Literal for Box {
  fn from_fragments(fragments: List<Fragment<String>>): Box {
    body = Iter.reduce(fragments, |acc = "", frag|
      case frag {
        .Static(s) -> acc + s
        .Dynamic(v) -> acc + v
      })
    Box{contents: body}
  }
}

fn main() {
  b = Box"hello"
  io.print(b.contents)
}`
	errs := checkTypedLiteralProject(t, entry, nil)
	for _, e := range errs {
		if strings.Contains(e.Message, "typed-literal") {
			t.Fatalf("expected clean check for block-form typed literal; got %s", e.Error())
		}
	}
}

// An imported tag type should resolve as a type at the literal site, not as
// the import binding that brought it into scope.
func TestTypedLiteral_ImportedTagType(t *testing.T) {
	entry := `import tags: Tag

fn main() {
  q = Tag"hello"
}`
	siblings := map[string]string{
		"tags": `import {
  std/literals: Fragment, Literal
  std/strings: String
}

pub type Tag String

impl Literal for Tag {
  fn from_fragments(_fragments: List<Fragment<String>>): Tag {
    Tag("ok")
  }
}
`,
	}
	errs := checkTypedLiteralProject(t, entry, siblings)
	for _, e := range errs {
		if strings.Contains(e.Message, "typed-literal") || strings.Contains(e.Message, "not a type") {
			t.Fatalf("expected imported tag type to resolve cleanly; got %s", e.Error())
		}
	}
}

// A PascalCase tag pointing at a non-existent name surfaces a precise
// not-in-scope diagnostic.
func TestTypedLiteral_TagMustBeAType(t *testing.T) {
	src := `fn main() {
  q = NotAType"hi"
}`
	errs := checkTypedLiteralSource(src)
	expectTypedLiteralError(t, errs, "is not in scope")
}

// pickDecls is a receiver carrying BOTH an `impl Literal for Pick` handler and
// an inherent `impl Pick { pub fn from_fragments }`, in the order given.
//
// The two return DIFFERENT types on purpose. That is what turns "which body
// runs" into a soundness question: the checker types `Pick"x"` by the handler's
// return type while the call runs the inherent one, so a program could
// type-check and fault at run time.
func pickDecls(inherentFirst bool) string {
	iface := "impl Literal for Pick {\n" +
		"  fn from_fragments(fragments: List<Fragment<String>>): Int {\n    7\n  }\n}\n"
	inherent := "impl Pick {\n" +
		"  pub fn from_fragments(fragments: List<Fragment<String>>): String {\n    \"inherent\"\n  }\n}\n"
	if inherentFirst {
		return "pub type Pick\n\n" + inherent + "\n" + iface
	}
	return "pub type Pick\n\n" + iface + "\n" + inherent
}

// TestTypedLiteral_AmbiguousHandlerIsRejected pins the fix for the roadmap's
// "the checker and execution disagree about which `from_fragments` an
// ambiguous typed literal calls" defect.
//
// The construct is REJECTED rather than resolved, and the argument is that
// neither pick is available. `Tag"…"` IS `Tag.from_fragments([…])` (spec §16),
// which resolves inherent-beats-interface-impl; but preferTypeMethodSymbol's
// justification for that rule is "the source said which one wins", and at a
// typed literal the source names no method at all — `from_fragments` is the
// compiler's. Picking the handler makes the sugar differ from its own
// desugaring; picking the inherent one lets an unrelated helper silently
// disable a type's `impl Literal`. Go cannot express the clash (two methods of
// one name on one type is `method redeclared`), so rejecting converges.
//
// BOTH DECLARATION ORDERS, because order is exactly what the first version of
// this fix got wrong: the per-file type-method table was first-writer-wins, so
// the check fired only when the inherent block came first. Both file layouts
// too — same file and sibling file — because the local and cross-file
// resolutions are different tables.
func TestTypedLiteral_AmbiguousHandlerIsRejected(t *testing.T) {
	const want = "is ambiguous"
	for _, inherentFirst := range []bool{false, true} {
		name := "interface impl declared first"
		if inherentFirst {
			name = "inherent block declared first"
		}
		t.Run(name+", same file", func(t *testing.T) {
			entry := "import std/literals.{Fragment, Literal}\n\n" + pickDecls(inherentFirst) +
				"\nfn main() {\n  q = Pick\"x\"\n}\n"
			expectTypedLiteralError(t, checkTypedLiteralProject(t, entry, nil), want)
		})
		t.Run(name+", sibling file", func(t *testing.T) {
			entry := "import pick: Pick\n\nfn main() {\n  q = Pick\"x\"\n}\n"
			siblings := map[string]string{
				"pick": "import std/literals.{Fragment, Literal}\n\n" + pickDecls(inherentFirst),
			}
			expectTypedLiteralError(t, checkTypedLiteralProject(t, entry, siblings), want)
		})
	}
}

// rivalIfaceDecls is a receiver carrying an `impl Literal for Pick` handler and
// a SECOND INTERFACE impl that also declares `from_fragments`, in the order
// given. Both providers are interface-impl methods, which is what distinguishes
// this population from pickDecls's.
//
// The two return DIFFERENT types for the same reason pickDecls's do: it makes
// "which body runs" a soundness question rather than a coin flip. Without the
// check, `s: String = Pick"x"` type-checked against the `Literal` handler's
// `String` and could run the other body, which answers `7`.
func rivalIfaceDecls(rivalFirst bool) string {
	decl := "interface Other {\n  fn from_fragments(fragments: List<Fragment<String>>): Int\n}\n"
	iface := "impl Literal for Pick {\n" +
		"  fn from_fragments(fragments: List<Fragment<String>>): String {\n    \"literal\"\n  }\n}\n"
	rival := "impl Other for Pick {\n" +
		"  fn from_fragments(fragments: List<Fragment<String>>): Int {\n    7\n  }\n}\n"
	if rivalFirst {
		return "pub type Pick\n\n" + decl + "\n" + rival + "\n" + iface
	}
	return "pub type Pick\n\n" + decl + "\n" + iface + "\n" + rival
}

// TestTypedLiteral_ASecondInterfaceProviderIsRejected is the OTHER HALF of the
// roadmap's typed-literal ambiguity defect, and it was still live at 66aee591
// after the inherent half was closed.
//
// A second interface may legally declare a method name a receiver already
// implements — `defineImplBlockAnnotations` says so in as many words, and the
// type-method table keeps ONE representative for the pair. So `Pick"x"` had
// more than one answer: `checkTaggedString` typed it by the `(Literal, Pick)`
// handler while the type-qualified call it desugars to is ambiguous.
//
// BOTH ORDERS, and the order is the whole point rather than diligence. The
// per-file table is first-writer-wins between two interface impls, so with
// `impl Literal for Pick` written FIRST the table hands back the handler
// itself — a lookup that cannot see its own rival. Any check that asks that
// table passes this test in one order and fails it in the other, which is the
// shape of the bug this replaced.
func TestTypedLiteral_ASecondInterfaceProviderIsRejected(t *testing.T) {
	const want = "`impl Other for Pick` also declares `from_fragments`"
	for _, rivalFirst := range []bool{false, true} {
		name := "Literal impl declared first"
		if rivalFirst {
			name = "rival impl declared first"
		}
		t.Run(name+", same file", func(t *testing.T) {
			entry := "import std/literals.{Fragment, Literal}\n\n" + rivalIfaceDecls(rivalFirst) +
				"\nfn main() {\n  q = Pick\"x\"\n}\n"
			expectTypedLiteralError(t, checkTypedLiteralProject(t, entry, nil), want)
		})
		t.Run(name+", sibling file", func(t *testing.T) {
			entry := "import pick: Pick\n\nfn main() {\n  q = Pick\"x\"\n}\n"
			siblings := map[string]string{
				"pick": "import std/literals.{Fragment, Literal}\n\n" + rivalIfaceDecls(rivalFirst),
			}
			expectTypedLiteralError(t, checkTypedLiteralProject(t, entry, siblings), want)
		})
	}
}

// TestTypedLiteral_ASecondInterfaceNotDeclaringFromFragmentsIsNotARival is the
// fence for the arm above, and it records that the population it fences is
// EMPTY through `Analyze`.
//
// `interfacesDeclaringMethod` admits an interface only when the interface
// DECLARES the method, so an `impl Quiet for Pick` block carrying an extra
// `from_fragments` item that `Quiet` does not declare is not reported by arm 2.
// Measured: that construct is rejected outright, one check earlier, with
// `impl 'Quiet' for 'Pick': function 'from_fragments' is not part of the
// interface`. So arm 2's interface-declares-it precondition costs nothing, and
// the assertion here is that the earlier check is what speaks — if it is ever
// relaxed, this fails and arm 2 needs widening rather than the silent pick the
// unstamped item would get.
func TestTypedLiteral_ASecondInterfaceNotDeclaringFromFragmentsIsNotARival(t *testing.T) {
	entry := "import std/literals.{Fragment, Literal}\n\npub type Pick\n\n" +
		"interface Quiet {\n  fn hush(p: Pick): String\n}\n\n" +
		"impl Literal for Pick {\n" +
		"  fn from_fragments(fragments: List<Fragment<String>>): String {\n    \"literal\"\n  }\n}\n\n" +
		"impl Quiet for Pick {\n" +
		"  fn hush(_p: Pick): String {\n    \"hush\"\n  }\n\n" +
		"  fn from_fragments(fragments: List<Fragment<String>>): Int {\n    7\n  }\n}\n\n" +
		"fn main() {\n  q = Pick\"x\"\n}\n"
	expectTypedLiteralError(t, checkTypedLiteralProject(t, entry, nil), "is not part of the interface")
}

// TestTypedLiteral_UnrivalledHandlerIsNotReportedAsItsOwnRival is the fence the
// ambiguity check needs, and it is here because the check FAILED it once.
//
// The first version compared the resolved type-method symbol's `Node` against
// the resolved impl by POINTER. An AST pointer is not a declaration's identity
// across parses — the stdlib is parsed once for std.Load() and again for the
// project index — so every stdlib handler read as a rival to itself and every
// `DateTime"…"` in the tour was reported ambiguous (vmhost.TestTourDoctests).
// A local handler shares one parse and would have passed, which is why the
// stdlib case is the one asserted.
func TestTypedLiteral_UnrivalledHandlerIsNotReportedAsItsOwnRival(t *testing.T) {
	entry := "import std/calendar.NaiveDateTime\n\n" +
		"fn main() {\n  q = try NaiveDateTime\"2026-11-01T01:30:00\"\n}\n"
	for _, e := range checkTypedLiteralProject(t, entry, nil) {
		if strings.Contains(e.Message, "is ambiguous") {
			t.Fatalf("a stdlib handler with no rival was reported as ambiguous against itself: %s", e.Error())
		}
	}
}

// TestTypeQualifiedCall_InherentBeatsInterfaceImplInEitherOrder checks that a
// type-qualified call prefers the inherent method over an interface impl's
// method of the same name, whichever block is written first.
//
// A first-writer-wins per-file type-method table would make this depend on
// source order: with `impl Literal for Pick` above `impl Pick`,
// `n: Int = Pick.from_fragments([…])` would type-check against the INTERFACE
// method's `Int` return while the call returns the inherent one's `String`,
// faulting at run time with `cannot add String and Int`.
// PopulateTypeMethodIdentities reads FROM that table, so
// preferTypeMethodSymbol has to see both symbols for its inherent-beats-impl
// arm to apply to a same-file clash.
//
// The assertion is that the ANNOTATION is rejected in both orders, i.e. the call
// is typed `String` (the inherent method) either way. Asserting the absence of
// an error would pass for the wrong reason in one order.
func TestTypeQualifiedCall_InherentBeatsInterfaceImplInEitherOrder(t *testing.T) {
	for _, inherentFirst := range []bool{false, true} {
		name := "interface impl declared first"
		if inherentFirst {
			name = "inherent block declared first"
		}
		t.Run(name, func(t *testing.T) {
			entry := "import std/literals.{Fragment, Literal}\n\n" + pickDecls(inherentFirst) +
				"\nfn main() {\n  n: Int = Pick.from_fragments([Fragment.Static(\"x\")])\n  q = n\n}\n"
			expectTypedLiteralError(t, checkTypedLiteralProject(t, entry, nil), "expected Int, got String")
		})
	}
}
