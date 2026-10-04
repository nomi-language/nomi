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
// are executed too and must pass. Plain ```nomi blocks are highlighted-only
// snippets.
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
			if b.HasInfo("ignore") {
				continue
			}
			runnable++
			t.Run(fmt.Sprintf("%s:'L%d", filepath.Base(chapter), b.Line), func(t *testing.T) {
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
				if err := p.Run(context.Background(), &out, nil, false); err != nil {
					t.Fatalf("Run failed for tour block:\n%s\n\nerror: %v", b.Code, err)
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
				if err := doctest.CheckOutput(b.Expected, out.String()); err != nil {
					t.Fatalf("tour block output mismatch:\n%s\n\n%s", b.Code, err)
				}
			})
		}
	}
	if runnable == 0 {
		t.Fatal("no runnable ```nomi-run blocks found under ../tour/src/content/docs")
	}
}

// tourBlockedOnVM is every tour test case the VM cannot run, by case name,
// with the reason `nomi test` reports for it. The playground prints each one
// as BLOCKED. Shrink it as retention grows; never add to it without a named
// cause. Empty since attached `//!` tests run on the VM.
var tourBlockedOnVM = map[string]string{}
