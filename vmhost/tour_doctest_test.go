package vmhost_test

import (
	"bytes"
	"context"
	"fmt"
	"github.com/nomi-language/nomi/internal/doctest"
	"github.com/nomi-language/nomi/vmhost"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTourDoctests runs every ```nomi-run block in tour/*.md on the VM,
// through the same nomi/vmhost path the playground's nomiRun takes, and
// asserts ordinary program output against
// hidden `<!-- expect ... -->` blocks. If a block contains tests, those tests
// are executed too and must pass. A block whose run stops (a `todo`
// reached, a trap, an `Err` from main) must be a ```nomi-run error block,
// which fails on purpose: it must fail to compile, or compile and stop while
// it runs, and its expect block holds what the tour shows (the compile
// error, or the output then the failure: doctest.Shown). A ```nomi-run no-run
// block must compile and is not run. Any other info word fails. Plain
// ```nomi blocks are highlighted-only snippets.
// Content lives in the Astro Starlight site at tour/src/content/docs/.
//
// A test case the VM cannot run fails the block unless tourBlockedOnVM lists
// it, and a listed case that runs fails too, so the list is exactly the
// playground's BLOCKED set.
func TestTourDoctests(t *testing.T) {
	unblocked := map[string]bool{}
	for name := range tourBlockedOnVM {
		unblocked[name] = true
	}
	defer func() {
		for name := range unblocked {
			t.Errorf("tour case %q is listed in tourBlockedOnVM but was not blocked; "+
				"the VM runs it now, so remove it from the list", name)
		}
	}()
	root := filepath.Clean("../tour/src/content/docs")
	var chapters []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && (strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".mdx")) {
			chapters = append(chapters, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk tour content: %v", err)
	}

	runnable := 0
	for _, chapter := range chapters {
		data, err := os.ReadFile(chapter)
		if err != nil {
			t.Fatalf("read %s: %v", chapter, err)
		}
		for _, b := range doctest.ExtractBlocks(string(data), "nomi-run") {
			b := b
			name := fmt.Sprintf("%s:'L%d", filepath.Base(chapter), b.Line)
			if unknown := b.UnknownInfo(); len(unknown) > 0 {
				t.Errorf("%s: ```nomi-run carries %q; the words a tour block may carry are %q and %q",
					name, unknown, doctest.InfoError, doctest.InfoNoRun)
				continue
			}
			if b.HasInfo(doctest.InfoError) {
				t.Run(name, func(t *testing.T) { checkTourErrorBlock(t, b) })
				continue
			}
			if b.HasInfo(doctest.InfoNoRun) {
				t.Run(name, func(t *testing.T) {
					if msg := tourCompileError(b.Code); msg != "" {
						t.Fatalf("a no-run tour block must compile:\n%s\n\nerror:\n%s", b.Code, msg)
					}
					if b.Expected != nil {
						t.Fatalf("a no-run tour block is never run, so it has no <!-- expect ... --> block:\n%s", b.Code)
					}
				})
				continue
			}
			runnable++
			t.Run(name, func(t *testing.T) {
				var out bytes.Buffer
				// Mirror the tour playground: `// FILE: <name>` markers
				// split the block into virtual sibling files (plus an
				// optional `// FILE: nomi.toml` manifest); with no
				// markers, the block is a single-file program.
				entrySrc, entryName, virtualFiles, manifest, splitErr := vmhost.SplitMultiFile(b.Code)
				if splitErr != nil {
					t.Fatalf("SplitMultiFile failed for tour block:\n%s\n\nerror: %v", b.Code, splitErr)
				}
				hasTests, err := vmhost.SourceContainsTests(entrySrc)
				if err != nil {
					t.Fatalf("SourceContainsTests failed for tour block:\n%s\n\nerror: %v", b.Code, err)
				}
				// The VM, as the playground's nomiRun runs a block: one
				// lowering serves the program and its tests.
				p, err := vmhost.LoadSource(entryName, entrySrc,
					vmhost.WithVirtualFiles(virtualFiles),
					vmhost.WithVirtualManifest(manifest))
				if err != nil {
					t.Fatalf("LoadSource failed for tour block:\n%s\n\nerror: %v", b.Code, err)
				}
				// A run that stops (a `todo` reached, a trap, an `Err` from
				// main) is compared as the tour shows it: the output, then
				// the failure. A case the VM cannot run is never expected.
				failure := ""
				if err := p.Run(context.Background(), &out, nil, false); err != nil {
					if _, blocked := vmhost.IsBlocked(err); blocked {
						t.Fatalf("the VM cannot run this tour block:\n%s\n\nerror: %v", b.Code, err)
					}
					failure = vmhost.FormatFailure(err)
					shown := doctest.Shown(out.String(), failure)
					if strings.Contains(shown, "-->") {
						t.Fatalf("the run's text holds `-->`, which would end the hidden <!-- expect --> comment in the page:\n%s", shown)
					}
					t.Fatalf("Run failed for tour block:\n%s\n\nerror: %v\n\nIf it stops on purpose, mark its fence `nomi-run error` and give it an <!-- expect ... --> block holding what the tour shows:\n%s", b.Code, err, shown)
				}
				if hasTests {
					var testOut bytes.Buffer
					for _, result := range p.Cases(&testOut, vmhost.TestOptions{}) {
						if result.Blocked != nil {
							if _, listed := tourBlockedOnVM[result.Name]; listed {
								delete(unblocked, result.Name)
								continue
							}
							t.Errorf("tour block test %q is blocked on the VM:\n%s\n\n%s", result.Name, b.Code, strings.Join(result.Blocked, "\n"))
							continue
						}
						if result.Err != nil {
							t.Fatalf("tour block test %q failed:\n%s\n\noutput:\n%s\nerror: %v", result.Name, b.Code, testOut.String(), result.Err)
						}
					}
				}
				if b.Expected == nil && out.String() != "" {
					t.Fatalf("tour block produced output but has no hidden <!-- expect ... --> block:\n%s\n\noutput:\n%s", b.Code, out.String())
				}
				shown := doctest.Shown(out.String(), failure)
				if err := doctest.CheckOutput(b.Expected, shown); err != nil {
					if failure != "" {
						t.Fatalf("Run failed for tour block, and not as its <!-- expect --> block says:\n%s\n\n%s\n\nthe tour shows:\n%s", b.Code, err, shown)
					}
					t.Fatalf("tour block output mismatch:\n%s\n\n%s", b.Code, err)
				}
			})
		}
	}
	if runnable == 0 {
		t.Fatal("no runnable ```nomi-run blocks found under ../tour/src/content/docs")
	}
}

// checkTourErrorBlock holds a ```nomi-run error block to its purpose: it
// must fail, either to compile or while it runs, and its hidden
// <!-- expect ... --> block must be what the tour's runner shows for it,
// line for line: the compile error, or the run's output then its failure.
func checkTourErrorBlock(t *testing.T, b doctest.Block) {
	t.Helper()
	msg := tourCompileError(b.Code)
	if msg == "" {
		msg = tourRunFailure(t, b.Code)
	}
	if msg == "" {
		t.Fatalf("an error tour block compiles and runs; drop `error` from its fence or make it fail again:\n%s", b.Code)
	}
	if strings.Contains(msg, "-->") {
		t.Fatalf("the error text holds `-->`, which would end the hidden <!-- expect --> comment in the page:\n%s", msg)
	}
	if b.Expected == nil {
		t.Fatalf("an error tour block needs a hidden <!-- expect ... --> block holding its error:\n%s\n\nerror:\n%s", b.Code, msg)
	}
	if err := doctest.CheckOutput(b.Expected, msg); err != nil {
		t.Fatalf("an error tour block fails with another error:\n%s\n\n%s\n\nthe whole error:\n%s", b.Code, err, msg)
	}
}

// tourRunFailure runs a block that compiles and answers what the tour shows
// when the run stops (the output, then the failure), or "" when it runs to
// the end.
func tourRunFailure(t *testing.T, code string) string {
	t.Helper()
	entrySrc, entryName, virtualFiles, manifest, err := vmhost.SplitMultiFile(code)
	if err != nil {
		t.Fatalf("SplitMultiFile failed for tour block:\n%s\n\nerror: %v", code, err)
	}
	p, err := vmhost.LoadSource(entryName, entrySrc,
		vmhost.WithVirtualFiles(virtualFiles),
		vmhost.WithVirtualManifest(manifest))
	if err != nil {
		t.Fatalf("LoadSource failed for tour block:\n%s\n\nerror: %v", code, err)
	}
	var out bytes.Buffer
	err = p.Run(context.Background(), &out, nil, false)
	if err == nil {
		return ""
	}
	if _, blocked := vmhost.IsBlocked(err); blocked {
		t.Fatalf("the VM cannot run this tour block:\n%s\n\nerror: %v", code, err)
	}
	return doctest.Shown(out.String(), vmhost.FormatFailure(err))
}

// tourCompileError is the error text the tour's runner (cmd/nomi-wasm's
// nomiRun) shows for a block that does not compile, or "" when it compiles.
func tourCompileError(code string) string {
	entrySrc, entryName, virtualFiles, manifest, err := vmhost.SplitMultiFile(code)
	if err != nil {
		return err.Error()
	}
	if _, err := vmhost.SourceContainsTests(entrySrc); err != nil {
		return err.Error()
	}
	if _, err := vmhost.LoadSource(entryName, entrySrc,
		vmhost.WithVirtualFiles(virtualFiles),
		vmhost.WithVirtualManifest(manifest)); err != nil {
		return err.Error()
	}
	return ""
}

// tourBlockedOnVM is every tour test case the VM cannot run, by case name,
// with the reason `nomi test` reports for it. The playground prints each one
// as BLOCKED. Shrink it as retention grows; never add to it without a named
// cause. Empty since attached `//!` tests run on the VM.
var tourBlockedOnVM = map[string]string{}
