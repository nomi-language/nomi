package vmhost_test

import (
	"bytes"
	"context"
	"fmt"
	"github.com/nomi-language/nomi/internal/doctest"
	"github.com/nomi-language/nomi/internal/tourgo"
	"github.com/nomi-language/nomi/internal/virtualproject"
	"github.com/nomi-language/nomi/vmhost"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
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
// block must compile and is not run. A `read-only:<glob>` word marks data
// files the run must not read, and the run must read every data file not
// marked (checkTourReads). Any other info word fails. Plain
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
				t.Errorf("%s: ```nomi-run carries %q; the words a tour block may carry are %q",
					name, unknown, doctest.InfoWords())
				continue
			}
			if msg := readOnlyGlobErrors(b); msg != "" {
				t.Errorf("%s: %s", name, msg)
				continue
			}
			// A manual block runs nothing when its page loads: the page
			// shows its expect block as the recorded output until the
			// reader presses Run (rehype-shiki.mjs, tour-client.mjs).
			if b.HasInfo(doctest.InfoManual) && b.Expected == nil {
				t.Errorf("%s: a manual block's page shows its <!-- expect ... --> block until the reader runs it, so it needs one:\n%s", name, b.Code)
				continue
			}
			if tools := b.Tools(); len(tools) > 0 {
				t.Run(name, func(t *testing.T) { checkTourToolBlock(t, b, tools) })
				continue
			}
			if b.HasInfo(doctest.InfoError) {
				t.Run(name, func(t *testing.T) { checkTourErrorBlock(t, b) })
				continue
			}
			if b.HasInfo(doctest.InfoNoRun) {
				t.Run(name, func(t *testing.T) {
					if msg := tourCompileError(t, b.Code); msg != "" {
						t.Fatalf("a no-run tour block must compile:\n%s\n\nerror:\n%s", b.Code, msg)
					}
					if b.Expected != nil {
						t.Fatalf("a no-run tour block is never run, so it has no <!-- expect ... --> block:\n%s", b.Code)
					}
				})
				continue
			}
			if unlinked := tourgo.Unlinked(b.Code); len(unlinked) > 0 {
				// A block that imports a tour module runs only on the
				// module's browser build, which TestTourWasmAnswersTheTourBlocks
				// runs under Node; here it must compile.
				t.Run(name, func(t *testing.T) {
					if msg := tourCompileError(t, b.Code); msg != "" {
						t.Fatalf("a tour block that imports %v must compile:\n%s\n\nerror:\n%s", unlinked, b.Code, msg)
					}
					if b.Expected == nil {
						t.Fatalf("a tour block that imports %v needs a hidden <!-- expect ... --> block, which the staged wasm is held to:\n%s", unlinked, b.Code)
					}
				})
				continue
			}
			runnable++
			t.Run(name, func(t *testing.T) {
				files, data := tourFileSystem(t, b)
				var out bytes.Buffer
				// Mirror the tour playground: `// FILE: <name>` markers
				// split the block into files (vmhost.LoadMarked, as the
				// playground's nomiRun loads it); with no markers, the
				// block is a single-file program.
				// The VM, as the playground's nomiRun runs a block: one
				// lowering serves the program and its tests.
				// Standard error interleaves with the output, as the tour's
				// runner shows it (cmd/nomi-wasm's nomiRun).
				// Its `go` bindings, as the playground answers them.
				// The modules the tour links that it imports, attached
				// as the playground attaches them (tourgo.Source).
				src := tourgo.Source(b.Code)
				hosts, err := tourgo.MarkedHosts(src)
				if err != nil {
					t.Fatalf("the tour links no Go for this block's bindings:\n%s\n\nerror: %v", b.Code, err)
				}
				p, hasTests, err := vmhost.LoadMarked(src, t.TempDir(), vmhost.WithHosts(hosts), vmhost.WithErrorOutput(&out), vmhost.WithFileSystem(files))
				if err != nil {
					t.Fatalf("LoadMarked failed for tour block:\n%s\n\nerror: %v", b.Code, err)
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
				// A manual block's expect block holds what its page shows,
				// its tests' report included, since the page shows it as
				// the block's recorded output before any run.
				report := ""
				if hasTests {
					var testOut bytes.Buffer
					cases := p.Cases(&testOut, vmhost.TestOptions{})
					if b.HasInfo(doctest.InfoManual) {
						report = vmhost.CaseReport(cases) + "\n"
					}
					for _, result := range cases {
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
				shown := doctest.Shown(out.String()+report, failure)
				if err := doctest.CheckOutput(b.Expected, shown); err != nil {
					if failure != "" {
						t.Fatalf("Run failed for tour block, and not as its <!-- expect --> block says:\n%s\n\n%s\n\nthe tour shows:\n%s", b.Code, err, shown)
					}
					t.Fatalf("tour block output mismatch:\n%s\n\n%s", b.Code, err)
				}
				checkTourReads(t, b, files, data)
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
	msg := tourCompileError(t, b.Code)
	if msg == "" {
		msg = tourRunFailure(t, b)
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

// checkTourToolBlock holds a ```nomi-run tool:<module>/<name> block to
// what this binary can check of it. A tool of a module the block holds
// (the block's own, or one under a directory of its own, which its go.mod
// requires) is Nomi alone, so this binary runs the tools and the block
// (checkOwnToolBlock).
// Otherwise its tools are tour modules' (the block imports each one), whose
// Go side only the tour's wasm links, so the block's run, which the tools'
// files feed, is TestTourWasmAnswersTheTourBlocks' to compare with its
// expect block, which it must have.
func checkTourToolBlock(t *testing.T, b doctest.Block, tools []doctest.Tool) {
	t.Helper()
	if b.HasInfo(doctest.InfoNoRun) {
		t.Fatalf("a tool runs before the block's run, so a block with a tool: word is run; drop `no-run`:\n%s", b.Code)
	}
	tourModule := func(name string) bool {
		return slices.ContainsFunc(tourgo.Modules, func(m tourgo.Module) bool { return m.Name == name })
	}
	if !slices.ContainsFunc(tools, func(tool doctest.Tool) bool { return tourModule(tool.Module) }) {
		checkOwnToolBlock(t, b, tools)
		return
	}
	imported := tourgo.Imported(b.Code)
	for _, tool := range tools {
		if !slices.Contains(imported, tool.Module) {
			t.Fatalf("the block runs a tool of %s, which is not a tour module it imports (it imports %v):\n%s", tool.Module, imported, b.Code)
		}
	}
	if len(tourgo.Unlinked(b.Code)) == 0 {
		t.Fatalf("this binary links the Go of every module the block imports, so it could run the block's tools; check its run here:\n%s", b.Code)
	}
	if b.Expected == nil {
		t.Fatalf("a block with a tool: word needs a hidden <!-- expect ... --> block, which the staged wasm is held to:\n%s", b.Code)
	}
}

// checkOwnToolBlock holds a block whose tools are its own modules' to its
// expect block, run as the page runs it: each tool on the source the one
// before left (vmhost.ToolMarked), what it prints first, then the block,
// whose tests must pass. A tool that fails stops there, and the block must
// be an error block showing what the tool printed and its failure.
func checkOwnToolBlock(t *testing.T, b doctest.Block, tools []doctest.Tool) {
	t.Helper()
	if b.Expected == nil {
		t.Fatalf("a block with a tool: word needs a hidden <!-- expect ... --> block:\n%s", b.Code)
	}
	src := b.Code
	var out bytes.Buffer
	failure := ""
	for _, tool := range tools {
		written, err := vmhost.ToolMarked(src, t.TempDir(), tool.Module, tool.Name, &out, vmhost.WithErrorOutput(&out))
		if err != nil {
			failure = vmhost.FormatFailure(err)
			break
		}
		src = vmhost.ReplaceSections(src, written)
	}
	if failure == "" {
		p, hasTests, err := vmhost.LoadMarked(src, t.TempDir(), vmhost.WithErrorOutput(&out))
		if err != nil {
			failure = err.Error()
		} else {
			if err := p.Run(context.Background(), &out, nil, false); err != nil {
				failure = vmhost.FormatFailure(err)
			}
			if hasTests && failure == "" {
				// The page prints the tests' report after the run's
				// output (vmhost.CaseReport); every case here must pass.
				var testOut bytes.Buffer
				cases := p.Cases(&testOut, vmhost.TestOptions{})
				for _, result := range cases {
					if result.Blocked != nil || result.Err != nil {
						t.Fatalf("tour block test %q does not pass:\n%s\n\noutput:\n%s", result.Name, b.Code, testOut.String())
					}
				}
				fmt.Fprintln(&out, vmhost.CaseReport(cases))
			}
		}
	}
	if (failure != "") != b.HasInfo(doctest.InfoError) {
		t.Fatalf("the block's tools and run fail exactly when it is an error block; failure: %q\n%s\n\noutput:\n%s", failure, b.Code, out.String())
	}
	shown := doctest.Shown(out.String(), failure)
	if strings.Contains(shown, "-->") {
		t.Fatalf("the run's text holds `-->`, which would end the hidden <!-- expect --> comment in the page:\n%s", shown)
	}
	if err := doctest.CheckOutput(b.Expected, shown); err != nil {
		t.Fatalf("the block's tools and run show something else:\n%s\n\n%s\n\nthe page shows:\n%s", b.Code, err, shown)
	}
}

// A tour block of sibling projects, rates/ and csvdata/, runs as the page
// runs it: csvdata's tool in rates/, then rates' program and its test.
func TestTourDoctest_SiblingProjectsBlock(t *testing.T) {
	code := strings.TrimSuffix(siblingsBlock(t, func(s string) string { return strings.Replace(s, "9200", "9300", 1) }), "\n")
	page := "```nomi-run manual tool:csvdata/generate\n" + code + "\n```\n<!-- expect\n" +
		"wrote tables.nomi, tables_test.nomi\n9300\nPound \"sterling\"\nYen ${not interpolated}\nFalse\n" +
		"ok generated tables are current\ntest result: ok. 1 passed, 0 failed\n-->\n"
	blocks := doctest.ExtractBlocks(page, "nomi-run")
	if len(blocks) != 1 {
		t.Fatalf("%d blocks", len(blocks))
	}
	checkOwnToolBlock(t, blocks[0], blocks[0].Tools())
}

// tourRunFailure runs a block that compiles and answers what the tour shows
// when the run stops (the output, then the failure), or "" when it runs to
// the end.
func tourRunFailure(t *testing.T, b doctest.Block) string {
	t.Helper()
	code := b.Code
	files, data := tourFileSystem(t, b)
	defer checkTourReads(t, b, files, data)
	var out bytes.Buffer
	src := tourgo.Source(code)
	hosts, err := tourgo.MarkedHosts(src)
	if err != nil {
		t.Fatalf("the tour links no Go for this block's bindings:\n%s\n\nerror: %v", code, err)
	}
	p, _, err := vmhost.LoadMarked(src, t.TempDir(), vmhost.WithHosts(hosts), vmhost.WithErrorOutput(&out), vmhost.WithFileSystem(files))
	if err != nil {
		t.Fatalf("LoadMarked failed for tour block:\n%s\n\nerror: %v", code, err)
	}
	err = p.Run(context.Background(), &out, nil, false)
	if err == nil {
		return ""
	}
	if _, blocked := vmhost.IsBlocked(err); blocked {
		t.Fatalf("the VM cannot run this tour block:\n%s\n\nerror: %v", code, err)
	}
	return doctest.Shown(out.String(), vmhost.FormatFailure(err))
}

// tourFileSystem is the in-memory file system a block runs on, seeded
// with its data files as LoadMarked seeds it, and the data files whose
// reads checkTourReads holds to the block's `read-only:` marks, by their
// paths as the block's `// FILE:` lines write them: every section that is
// not a Nomi source, a nomi.toml, a message catalog (a `.toml` the
// compiler reads, which an edit can change the output of) or a Go file
// (which the tour compiles ahead of time and shows read-only by its kind).
// A block that names modules (one with a go.mod section) is left out: it
// is written to disk.
func tourFileSystem(t *testing.T, b doctest.Block) (*vmhost.MemoryFiles, []string) {
	t.Helper()
	files, err := vmhost.BlockFiles(b.Code)
	if err != nil {
		t.Fatalf("the block's files do not split:\n%s\n\nerror: %v", b.Code, err)
	}
	names := virtualproject.FileNames(b.Code)
	for _, name := range names {
		if path.Base(name) == "go.mod" {
			return files, nil
		}
	}
	var data []string
	for _, name := range names {
		if tourDataFile(name) {
			data = append(data, name)
		}
	}
	return files, data
}

// tourDataFile reports whether a block's file named name is data the run
// may read: not a Nomi source, a manifest, a catalog or Go.
func tourDataFile(name string) bool {
	return !strings.HasSuffix(name, ".nomi") && !strings.HasSuffix(name, ".toml") &&
		!strings.HasSuffix(name, ".go") && path.Base(name) != "go.mod" && path.Base(name) != "go.sum"
}

// checkTourReads holds a block's run to the tour's rule for its data
// files: a file is editable only when an edit can change what the example
// prints. So a data file marked `read-only:` must not be read, and one not
// marked must be. A read is an open of the file's contents that succeeded
// (vmhost.MemoryFiles.Read): listing a directory, a file's metadata, a
// missing file and a path `fs.within` refuses are not reads.
func checkTourReads(t *testing.T, b doctest.Block, files *vmhost.MemoryFiles, data []string) {
	t.Helper()
	read := map[string]bool{}
	for _, p := range files.Read() {
		read[p] = true
	}
	for _, name := range data {
		was := read[path.Clean(name)]
		switch marked := b.MarkedReadOnly(name); {
		case marked && was:
			t.Errorf("%s is marked read-only, but the example reads it", name)
		case !marked && !was:
			t.Errorf("%s is editable, but the example never reads it: mark it read-only:%s, or drop it", name, name)
		}
	}
}

// readOnlyGlobErrors answers what is wrong with a block's `read-only:`
// globs, or "": each must be a path.Match pattern that matches one of the
// block's files at least, and none may match a Nomi source, since an edit
// to one changes the program.
func readOnlyGlobErrors(b doctest.Block) string {
	names := virtualproject.FileNames(b.Code)
	var errs []string
	for _, glob := range b.ReadOnlyGlobs() {
		if _, err := path.Match(glob, ""); err != nil {
			errs = append(errs, fmt.Sprintf("read-only:%s is not a glob: %v", glob, err))
			continue
		}
		matched := false
		for _, name := range names {
			if ok, _ := path.Match(glob, name); ok {
				matched = true
				if strings.HasSuffix(name, ".nomi") {
					errs = append(errs, fmt.Sprintf("read-only:%s matches %s, a Nomi source, whose edits change the program", glob, name))
				}
			}
		}
		if !matched {
			errs = append(errs, fmt.Sprintf("read-only:%s matches no file of the block", glob))
		}
	}
	return strings.Join(errs, "\n")
}

// tourCompileError is the error text the tour's runner (cmd/nomi-wasm's
// nomiRun) shows for a block that does not compile, or "" when it compiles.
func tourCompileError(t *testing.T, code string) string {
	t.Helper()
	src := tourgo.Source(code)
	hosts, err := tourgo.MarkedHosts(src)
	if err != nil {
		return err.Error()
	}
	if _, _, err := vmhost.LoadMarked(src, t.TempDir(), vmhost.WithHosts(hosts)); err != nil {
		return err.Error()
	}
	return ""
}

// tourBlockedOnVM is every tour test case the VM cannot run, by case name,
// with the reason `nomi test` reports for it. The playground prints each one
// as BLOCKED. Shrink it as retention grows; never add to it without a named
// cause. Empty since attached `//!` tests run on the VM.
var tourBlockedOnVM = map[string]string{}
