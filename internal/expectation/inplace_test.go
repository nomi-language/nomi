package expectation_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/expectation"
	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/internal/vmcmd"
	"github.com/nomi-language/nomi/std"
)

// TestExpectation_InPlaceUpdatesRunAsCopying runs programs twice, once as
// `nomi run` and `nomi test` do, writing in place every struct update and
// collection operation whose operand nothing reads again (ir.Owns), and
// once with every one copying (vm.NoInPlaceEnv), and requires the two
// transcripts and exit statuses to be byte-identical. The programs:
//
//   - corpus: every recorded corpus program, whose transcript must also
//     match its record;
//
//   - stdlib: every stdlib module with prompt cases, run as `nomi test
//     std/<module>`, whose transcript must also match its record;
//
//   - benchmarks: the Benchmarks Game programs that take a size and every
//     Are We Fast Yet program, at the runner's check sizes; a Benchmarks
//     Game program's output must also match its published file.
//
//     go test ./internal/expectation -run 'TestExpectation_InPlaceUpdatesRunAsCopying$' -count=1
func TestExpectation_InPlaceUpdatesRunAsCopying(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; runs every corpus and stdlib program twice; -short")
	}
	pinRecorderEnv(t)
	wtRoot, err := expectation.Root()
	if err != nil {
		t.Fatal(err)
	}
	// both runs run twice and answers the in-place run's transcript and
	// exit status, failing when the copying run's differ.
	both := func(t *testing.T, name string, run func() (observed, int)) (string, int, bool) {
		t.Setenv(vm.NoInPlaceEnv, "")
		inPlace, _ := run()
		t.Setenv(vm.NoInPlaceEnv, "1")
		copying, _ := run()
		a := expectation.Normalize(inPlace.transcript(), wtRoot)
		b := expectation.Normalize(copying.transcript(), wtRoot)
		if a != b || inPlace.exit != copying.exit {
			t.Errorf("%s: in place differs from copying (exit %d, copying %d):\n%s",
				name, inPlace.exit, copying.exit, expectation.LineDiff(b, a))
			return a, inPlace.exit, false
		}
		return a, inPlace.exit, true
	}

	t.Run("corpus", func(t *testing.T) {
		root := filepath.Join(repoRoot(t), "tests")
		set := committedSet(t, "corpus")
		ran := 0
		for _, rel := range nomiFilesUnder(t, root) {
			rec, recorded := set.Lookup(rel)
			if !recorded {
				// A helper file of a program, which runs as its entry.
				continue
			}
			path := filepath.Join(root, rel)
			got, exit, same := both(t, rel, func() (observed, int) { return runCommand(t, path) })
			if same && (got != rec.Transcript || exit != rec.Exit) {
				t.Errorf("%s differs from its record (exit %d, want %d):\n%s",
					rel, exit, rec.Exit, expectation.LineDiff(rec.Transcript, got))
			}
			ran++
		}
		if ran < 500 {
			t.Fatalf("ran %d corpus programs; the enumeration is wrong", ran)
		}
		t.Logf("%d corpus programs ran alike in place and copying", ran)
	})

	t.Run("stdlib", func(t *testing.T) {
		set := committedSet(t, "stdlib")
		ran := 0
		for _, module := range stdlibModulesWithPrompts(t) {
			rec, recorded := set.Lookup(module)
			if !recorded {
				t.Errorf("std/%s has prompts and no record", module)
				continue
			}
			path := fileuri.ToPath(std.Load().FileURI(module))
			got, exit, same := both(t, module, func() (observed, int) { return runTests(t, path) })
			if same && (got != rec.Transcript || exit != rec.Exit) {
				t.Errorf("std/%s differs from its record (exit %d, want %d):\n%s",
					module, exit, rec.Exit, expectation.LineDiff(rec.Transcript, got))
			}
			ran++
		}
		if ran < 50 {
			t.Fatalf("ran %d stdlib modules; the enumeration is wrong", ran)
		}
		t.Logf("%d stdlib modules ran alike in place and copying", ran)
	})

	t.Run("benchmarks", func(t *testing.T) {
		root := filepath.Join(repoRoot(t), "benchmarks")
		// The runner's check sizes (benchmarks/runner), without the
		// programs that read a FASTA file on standard input.
		for _, b := range []struct {
			file, expected string
			args           []string
		}{
			{"game/binarytrees.nomi", "binarytrees-output.txt", []string{"10"}},
			{"game/fannkuchredux.nomi", "fannkuchredux-output.txt", []string{"7"}},
			{"game/fasta.nomi", "fasta-output.txt", []string{"1000"}},
			{"game/mandelbrot.nomi", "mandelbrot-output.txt", []string{"200"}},
			{"game/nbody.nomi", "nbody-output.txt", []string{"1000"}},
			{"game/pidigits.nomi", "pidigits-output.txt", []string{"30"}},
			{"game/spectralnorm.nomi", "spectralnorm-output.txt", []string{"100"}},
			{"awfy/bounce.nomi", "", []string{"1", "1"}},
			{"awfy/list.nomi", "", []string{"1", "1"}},
			{"awfy/mandelbrot.nomi", "", []string{"1", "1"}},
			{"awfy/nbody.nomi", "", []string{"1", "1"}},
			{"awfy/permute.nomi", "", []string{"1", "1"}},
			{"awfy/queens.nomi", "", []string{"1", "1"}},
			{"awfy/sieve.nomi", "", []string{"1", "1"}},
			{"awfy/storage.nomi", "", []string{"1", "1"}},
			{"awfy/towers.nomi", "", []string{"1", "1"}},
			{"awfy/cd.nomi", "", []string{"1", "10"}},
			{"awfy/deltablue.nomi", "", []string{"1", "1"}},
			{"awfy/havlak.nomi", "", []string{"1", "1"}},
			{"awfy/json.nomi", "", []string{"1", "1"}},
			{"awfy/richards.nomi", "", []string{"1", "1"}},
		} {
			path := filepath.Join(root, b.file)
			got, exit, same := both(t, b.file, func() (observed, int) {
				var out, errOut bytes.Buffer
				code := vmcmd.Run(context.Background(), path, &out, &errOut, bytes.NewReader(nil), b.args, false)
				return observed{stdout: out.String(), stderr: errOut.String(), exit: code}, 0
			})
			if !same {
				continue
			}
			if exit != 0 {
				t.Errorf("%s exits %d:\n%s", b.file, exit, got)
				continue
			}
			if b.expected == "" {
				if !strings.Contains(got, "verified") {
					t.Errorf("%s does not verify:\n%s", b.file, got)
				}
				continue
			}
			want, err := os.ReadFile(filepath.Join(root, "game", "expected", b.expected))
			if err != nil {
				t.Fatal(err)
			}
			if got != expectation.Normalize(expectation.Transcript(string(want), ""), wtRoot) {
				t.Errorf("%s's output is not %s", b.file, b.expected)
			}
		}
	})
}
