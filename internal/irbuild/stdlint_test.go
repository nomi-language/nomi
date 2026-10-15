package irbuild

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// TestStdlibLowersTheSameWithoutLint holds what makes lintEmbeddedStdlib
// sound to turn off in a built binary. `ir.Lint` is not only an assertion:
// where a lambda, field accessor, host crossing or default accessor fails it,
// the builder declines the body. So the embedded stdlib may skip lint only if
// no stdlib body fails it.
//
// The stdlib is lowered with lint off, and then every module it retained must
// pass `ir.LintModule`, which lints each function and every lambda graph
// nested in one. A graph that would have failed a lint the builder checks
// while lowering is retained in that lowering, so it fails here. The
// lowering must also retain exactly what the lowering with lint on retains,
// function by function and instruction by instruction, and leave the same
// bodies unlowered for the same reasons.
func TestStdlibLowersTheSameWithoutLint(t *testing.T) {
	if !lintEmbeddedStdlib {
		t.Fatal("lintEmbeddedStdlib is off under go test")
	}
	// Warmed first, for TestIRParamShapeAgreesWithTheVMForStd's reason.
	buildStdlibIndex()
	on := buildStdlibIndex()

	lintEmbeddedStdlib = false
	defer func() { lintEmbeddedStdlib = true }()
	off := buildStdlibIndex()
	lintEmbeddedStdlib = true

	for pkg, m := range off.irModules {
		if err := ir.LintModule(m); err != nil {
			t.Errorf("%s, lowered without lint, fails it:\n%v", pkg, err)
		}
	}
	if got, want := stdLoweringPrint(off), stdLoweringPrint(on); got != want {
		t.Errorf("the stdlib lowered without lint differs from the lowering with it:\n%s", firstDifference(got, want))
	}
}

// stdLoweringPrint prints what a stdlib lowering retained and declined, in a
// stable order.
func stdLoweringPrint(idx *stdlibIndex) string {
	var b strings.Builder
	pkgs := make([]string, 0, len(idx.irModules))
	for pkg := range idx.irModules {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)
	for _, pkg := range pkgs {
		m := idx.irModules[pkg]
		fmt.Fprintf(&b, "module %s\n", pkg)
		for _, f := range m.Funcs() {
			fmt.Fprintf(&b, "  func %s\n", f.Name())
			for _, bl := range f.Blocks() {
				for _, in := range bl.Instrs() {
					fmt.Fprintf(&b, "    %v\n", in)
				}
			}
		}
		for _, c := range m.Cells() {
			fmt.Fprintf(&b, "  cell %v\n", c.Sym())
		}
	}
	unlowered := make([]string, 0)
	for pkg, why := range idx.unlowered {
		for name, reason := range why {
			unlowered = append(unlowered, pkg+"."+name+": "+reason)
		}
	}
	sort.Strings(unlowered)
	for _, u := range unlowered {
		fmt.Fprintf(&b, "unlowered %s\n", u)
	}
	return b.String()
}

func firstDifference(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(g) && i < len(w); i++ {
		if g[i] != w[i] {
			return fmt.Sprintf("line %d: without lint %q, with lint %q", i+1, g[i], w[i])
		}
	}
	return fmt.Sprintf("without lint %d lines, with lint %d", len(g), len(w))
}
