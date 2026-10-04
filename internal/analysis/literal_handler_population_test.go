package analysis_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// fromFragmentsDecl is one `fn from_fragments` declaration and the impl block
// that carries it. Iface is "" for an inherent `impl T { ... }` block.
type fromFragmentsDecl struct {
	File  string
	Iface string
	Line  int
}

// censusFromFragments groups every `from_fragments` declaration under the
// receiver type it is declared for, over every `.nomi` file under `roots`.
//
// Receivers are keyed by BARE name across all roots, which over-reports rather
// than under-reports: two unrelated projects each declaring a type of one name
// are merged, so a clash this counter shows may not be a clash in any single
// project, while a clash in some project cannot be hidden from it. That
// direction is the one a zero needs.
func censusFromFragments(t *testing.T, roots ...string) (map[string][]fromFragmentsDecl, int) {
	t.Helper()
	byType := map[string][]fromFragmentsDecl{}
	files := 0
	for _, root := range roots {
		err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if fi.IsDir() || !strings.HasSuffix(p, ".nomi") {
				return nil
			}
			files++
			src, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(src)))
			for _, n := range nodes {
				blk, ok := n.(*ast.ImplBlock)
				if !ok {
					continue
				}
				recv := analysis.TypeExprBaseName(blk.Receiver)
				iface := analysis.TypeExprBaseName(blk.Interface)
				for _, item := range blk.Items {
					var name string
					var line int
					switch it := item.(type) {
					case *ast.FuncDef:
						name, line = it.Name, it.Line
					case *ast.ExternFunc:
						name, line = it.Name, it.Line
					default:
						continue
					}
					if name != "from_fragments" {
						continue
					}
					byType[recv] = append(byType[recv], fromFragmentsDecl{File: p, Iface: iface, Line: line})
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	return byType, files
}

// clashingReceivers names every receiver whose `from_fragments` declarations
// span two or more distinct OWNERS, where an owner is the inherent block ("")
// or an interface name — the population checkTaggedString's ambiguity
// diagnostic rejects, in both of its spellings.
//
// Owners rather than a raw count, and both halves of that are measured.
//
// The narrow predecessor required `iface > 0 && inherent > 0`, so a receiver
// carrying an `impl Literal` handler AND a second interface impl declaring
// `from_fragments` read as ZERO clashes, while that construct type-checked
// with no single answer for which block runs.
//
// A raw `len(decls) > 1` overshoots in the other direction: two unrelated
// projects that each declare `impl Literal for Sql` (as
// `tests/11-interfaces-and-impls/impl_blocks_test.nomi:99` does) merge into
// one row, because receivers are keyed by bare name across roots. Two impls of ONE
// interface for one receiver in one program is `detectImplCollisions`'s check,
// not this one; the owner set separates the two questions without giving up the
// bare-name over-reporting the census header argues for.
func clashingReceivers(byType map[string][]fromFragmentsDecl) []string {
	var out []string
	for recv, decls := range byType {
		owners := map[string]bool{}
		for _, d := range decls {
			owners[d.Iface] = true
		}
		if len(owners) > 1 {
			out = append(out, recv)
		}
	}
	sort.Strings(out)
	return out
}

func renderCensus(byType map[string][]fromFragmentsDecl) string {
	names := make([]string, 0, len(byType))
	for k := range byType {
		names = append(names, k)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "%s\n", n)
		for _, d := range byType[n] {
			iface := d.Iface
			if iface == "" {
				iface = "(inherent)"
			}
			fmt.Fprintf(&b, "    %-12s %s:%d\n", iface, d.File, d.Line)
		}
	}
	return b.String()
}

// TestLiteralHandler_NoTypeDeclaresFromFragmentsUnderTwoOwners is the
// POPULATION behind checkTaggedString's ambiguity diagnostic, kept as a
// re-runnable measurement rather than a sentence in a report.
//
// The population is ZERO — every `from_fragments` in the repo is declared
// inside an `impl Literal for X` block and nothing else on those receivers
// declares the name — so the diagnostic rejects no program that exists today.
// A zero-member
// category is worth exactly as much as the proof that its counter could have
// shown a member, so the positive controls below run the same counter over
// synthetic clashes and require it to report them. Without that half,
// "0 clashes" and "the walk found nothing" are the same reading.
//
// TWO CONTROLS, and the second one is here because the first version of this
// counter PASSED while the construct it was supposed to bound was live. It
// required an inherent declaration beside an interface-impl one, so a receiver
// carrying `impl Literal for Pick` AND a second interface impl declaring
// `from_fragments` counted as zero, and that program type-checked with a
// binding annotated `String` that the other block's `Int` could fill. A
// control per spelling is the
// cost of the counter meaning what its name says.
//
// The counter measures DECLARATIONS. It cannot see a resolution, which is the
// complementary instrument's job (TestTypedLiteral_AmbiguousHandlerIsRejected,
// TestTypedLiteral_ASecondInterfaceProviderIsRejected and
// vmhost.TestTourDoctests) — and the two disagreeing is how the first version
// of the check was caught reporting every stdlib handler as its own rival.
func TestLiteralHandler_NoTypeDeclaresFromFragmentsUnderTwoOwners(t *testing.T) {
	byType, files := censusFromFragments(t, "../../std", "../../tests")
	if files == 0 {
		t.Fatal("walked no .nomi files — the roots moved, so the zero below is vacuous")
	}
	if len(byType) == 0 {
		t.Fatalf("found no `from_fragments` declaration in %d files; the repo has several, so this counter is broken", files)
	}
	if clashes := clashingReceivers(byType); len(clashes) != 0 {
		t.Errorf("a type now declares `from_fragments` under two different owners: %v\n"+
			"That construct is REJECTED by checkTaggedString's ambiguity diagnostic, so this is a\n"+
			"source change that no longer compiles, not a stale count. Census:\n%s",
			clashes, renderCensus(byType))
	}
	t.Logf("%d files, %d receivers declare `from_fragments`, 0 clashes:\n%s", files, len(byType), renderCensus(byType))

	const handler = "impl Literal for Pick {\n" +
		"  fn from_fragments(fragments: List<Fragment<String>>): String {\n    \"literal\"\n  }\n}\n\n"
	for _, ctl := range []struct {
		name  string
		rival string
	}{{
		name: "an inherent rival",
		rival: "impl Pick {\n" +
			"  pub fn from_fragments(fragments: List<Fragment<String>>): String {\n    \"inherent\"\n  }\n}\n",
	}, {
		name: "a second interface impl",
		rival: "impl Other for Pick {\n" +
			"  fn from_fragments(fragments: List<Fragment<String>>): Int {\n    7\n  }\n}\n",
	}} {
		t.Run("positive control: "+ctl.name, func(t *testing.T) {
			dir := t.TempDir()
			src := "pub type Pick\n\n" + handler + ctl.rival
			if err := os.WriteFile(filepath.Join(dir, "pick.nomi"), []byte(src), 0o644); err != nil {
				t.Fatalf("write positive control: %v", err)
			}
			control, _ := censusFromFragments(t, dir)
			if got := clashingReceivers(control); len(got) != 1 || got[0] != "Pick" {
				t.Fatalf("the counter cannot report a clash it is shown: got %v, want [Pick].\n"+
					"The zero above is then a property of this instrument, not of the repo", got)
			}
		})
	}
}
