package vmhost_test

import (
	"bytes"
	"context"
	"fmt"
	"github.com/nomi-language/nomi/internal/doctest"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/vmhost"
	"github.com/nomi-language/nomi/vmrunner"
	"os"
	"regexp"
	"strings"
	"testing"
)

// specMainRe matches a top-level `fn main` declaration: at column 0, so a
// `fn main` indented inside a prose-shaped fragment does not count.
var specMainRe = regexp.MustCompile(`(?m)^fn main\(`)

// TestSpecPrograms loads and runs every complete program in the language
// specification, docs/spec.md.
//
// parser.TestSpecExamples_NoMalformedBlock only parses the spec's ```nomi
// blocks, because most of them are fragments: statements that use names
// declared in an earlier block, signature lists, `...` elisions, restatements
// of stdlib declarations, and examples the compiler is meant to reject. None
// of those can be type-checked on their own. A block is a complete program
// exactly when it declares `fn main` at column 0 and elides nothing, and that
// rule needs no judgement: every such block must load (parse, analyze, and
// lower with nothing BLOCKED) and run to completion. The spec states expected
// output in comments, which this test does not read.
func TestSpecPrograms(t *testing.T) {
	const specPath = "../docs/spec.md"
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read %s: %v", specPath, err)
	}
	programs := 0
	for _, b := range doctest.ExtractBlocks(string(data), "nomi") {
		if !specMainRe.MatchString(b.Code) || strings.Contains(b.Code, "...") {
			continue
		}
		programs++
		b := b
		t.Run(fmt.Sprintf("L%d", b.Line), func(t *testing.T) {
			p, err := vmhost.LoadSource("main.nomi", b.Code)
			if err != nil {
				t.Fatalf("%s:%d: spec program does not load:\n%s\n\nerror: %v", specPath, b.Line, b.Code, err)
			}
			var out bytes.Buffer
			if err := p.Run(context.Background(), &out, nil, false); err != nil {
				t.Fatalf("%s:%d: spec program failed:\n%s\n\noutput:\n%s\nerror: %v", specPath, b.Line, b.Code, out.String(), err)
			}
		})
	}
	// The spec holds ten complete programs; a rule that matched none would
	// pass vacuously.
	if programs < 10 {
		t.Fatalf("found %d complete programs in %s, want at least 10", programs, specPath)
	}
}

// TestSpecProgramsOnADebugMachine runs every complete spec program on a
// plain machine and on a debug machine with no breakpoints, and requires the
// same output and the same outcome. internal/expectation's
// TestExpectation_ADebugMachineRunsEveryProgramAsAPlainOneDoes is the same
// check over the corpus and the tour.
func TestSpecProgramsOnADebugMachine(t *testing.T) {
	data, err := os.ReadFile("../docs/spec.md")
	if err != nil {
		t.Fatal(err)
	}
	run := func(src string) string {
		p, err := vmhost.LoadSource("main.nomi", src)
		if err != nil {
			return "load: " + err.Error()
		}
		var out bytes.Buffer
		err = p.Run(context.Background(), &out, nil, false)
		return fmt.Sprintf("%s\n-- %v", out.String(), err)
	}
	programs := 0
	for _, b := range doctest.ExtractBlocks(string(data), "nomi") {
		if !specMainRe.MatchString(b.Code) || strings.Contains(b.Code, "...") {
			continue
		}
		programs++
		plain := run(b.Code)
		restore := vm.DebugEveryMachine()
		debug := run(b.Code)
		restore()
		if plain != debug {
			t.Errorf("docs/spec.md:%d runs differently on a debug machine:\nplain:\n%s\ndebug:\n%s", b.Line, plain, debug)
		}
	}
	if programs < 10 {
		t.Fatalf("found %d complete programs, want at least 10", programs)
	}
}

// TestSpecProgramsFromABuiltImage builds every complete spec program as
// `nomi build` does (an image pruned to what main reaches) and runs it as a
// built executable does (vmrunner.Run), and requires the output `nomi run`
// gives.
func TestSpecProgramsFromABuiltImage(t *testing.T) {
	data, err := os.ReadFile("../docs/spec.md")
	if err != nil {
		t.Fatal(err)
	}
	programs := 0
	for _, b := range doctest.ExtractBlocks(string(data), "nomi") {
		if !specMainRe.MatchString(b.Code) || strings.Contains(b.Code, "...") {
			continue
		}
		p, err := vmhost.LoadSource("main.nomi", b.Code)
		if err != nil {
			t.Fatalf("docs/spec.md:%d: %v", b.Line, err)
		}
		var want bytes.Buffer
		if err := p.Run(context.Background(), &want, nil, false); err != nil {
			t.Fatalf("docs/spec.md:%d: %v", b.Line, err)
		}
		img, _, err := p.BuildImage()
		if err != nil {
			// A program with a `todo` or `dbg` left in it is not built.
			continue
		}
		programs++
		var got, errOut bytes.Buffer
		if code := vmrunner.Run(img, &got, &errOut, strings.NewReader(""), nil, false); code != 0 || got.String() != want.String() {
			t.Errorf("docs/spec.md:%d from its built image: exit %d, stderr %q\nstdout:\n%s\nnomi run printed:\n%s",
				b.Line, code, errOut.String(), got.String(), want.String())
		}
	}
	if programs < 10 {
		t.Fatalf("built %d complete programs, want at least 10", programs)
	}
}
