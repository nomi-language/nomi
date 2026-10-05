package vmhost_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/nomi-language/nomi/internal/doctest"
	"github.com/nomi-language/nomi/internal/wasmmem"
	"github.com/nomi-language/nomi/vmhost"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The tour's browser runtime is a staged bundle, not the source tree. A stale
// bundle shows users analysis errors the current source no longer produces,
// while `TestTourDoctests` in this package stays green, because it runs the
// tour's blocks through the analyzer linked into the test binary rather than
// through the staged wasm. `public/nomi/` is gitignored, so no diff shows a
// stale bundle either.
//
// The two tests below ask the two questions a stale bundle raises:
//
//   - TestTourWasmBundleIsTheCurrentSources asks whether the staged bundle can
//     be what this source tree produces. It answers by rebuilding, not by
//     reading an mtime (mtimes move for reasons unrelated to content, and a
//     `cp` of a stale file forward defeats them entirely).
//   - TestTourWasmAnswersTheTourBlocks asks what the staged wasm actually
//     says, by running it under node the way the browser does and comparing
//     against the same committed `<!-- expect -->` blocks TestTourDoctests
//     holds the in-process VM to. This is the reading that names the
//     user-visible symptom.
//
// Whether a missing bundle skips or fails matters, because `public/nomi/` is
// gitignored and legitimately absent on a fresh clone.
//
// A skip cannot be made loud: `go test` discards a skipped test's output in
// a non-verbose run (t.Log, os.Stdout and os.Stderr alike), so a skip is
// silent whatever you write into it. Only -v shows it. A silent skip is
// therefore acceptable only where the skipped condition cannot be the
// failing one.
//
// So the absent case is split, and only one branch is silent:
//
//   - `public/nomi/` does not exist at all: nobody has ever built the tour
//     in this checkout. Skip. Nothing can be stale, and the state is
//     self-announcing the moment it matters: with no bundle the tour's
//     examples do not load, so the first page served is visibly broken and
//     the browser console names the 404.
//   - `public/nomi/` exists but a file the script stages is missing from it.
//     Fail. That is a partial or damaged bundle, which the script never
//     produces, and it is a state the browser can serve while looking mostly
//     fine.
//
// A stale wasm is a present file in a present directory, so it can never
// reach the silent branch. That is the property the check needs: the hazard
// worth avoiding, a row that skips on the same condition that would make it
// fail, requires the two to overlap, and here they are disjoint by
// construction.
//
// Requiring the whole bundle unconditionally would be worse: it would make
// `go test ./...` red on a fresh clone for an artifact that
// clone has no reason to hold, and a row that is red by default is a row
// people learn to ignore.
//
// NOMI_REQUIRE_TOUR_WASM=1 removes the silent branch for callers that know
// the bundle must exist. `make test-tour` and `make deploy-tour` both set it,
// so the publish path cannot publish a bundle the gate never read.
const requireEnv = "NOMI_REQUIRE_TOUR_WASM"

// stagedDir is the bundle the Starlight site serves statically, and the
// directory scripts/build-tour-wasm.sh stages into.
const stagedDir = "../tour/public/nomi"

const rebuildInstruction = "run ./scripts/build-tour-wasm.sh (about 4 seconds) and re-run this test"

// stagedFileMissing routes an absent staged file through the split argued
// above: silent only when the staging directory itself is absent.
func stagedFileMissing(t *testing.T, name string) bool {
	t.Helper()
	if _, err := os.Stat(filepath.Join(stagedDir, name)); err == nil {
		return false
	}
	if _, err := os.Stat(stagedDir); err == nil {
		t.Fatalf(`STAGED TOUR BUNDLE IS INCOMPLETE.

  %s exists but %s is not in it.

scripts/build-tour-wasm.sh writes every staged file in one pass, so a
directory missing one of them was assembled some other way. Fix: %s`,
			stagedDir, name, rebuildInstruction)
	}
	if os.Getenv(requireEnv) == "1" {
		t.Fatalf("%s does not exist, so there is no staged tour bundle (%s=1 makes this a failure). Fix: %s",
			stagedDir, requireEnv, rebuildInstruction)
	}
	t.Skipf("%s does not exist; the tour has never been built in this checkout. %s", stagedDir, rebuildInstruction)
	return true
}

// TestTourWasmBundleIsTheCurrentSources fails when a staged file cannot be
// what this tree produces.
//
// Every file the build script stages has a source of truth in this repository,
// and each is compared against it rather than against its own timestamp:
//
//	nomi.wasm.gz          a fresh GOOS=js GOARCH=wasm build of ./cmd/nomi-wasm,
//	                      compared after decompressing the staged file
//	wasm_exec.js          $(go env GOROOT)/lib/wasm/wasm_exec.js
//	tree-sitter-nomi.wasm ../tree-sitter-nomi/tree-sitter-nomi.wasm
//	highlights.scm        ../tree-sitter-nomi/queries/highlights.scm
//	highlight.mjs         ../tour/src/lib/highlight.mjs
//	run-worker.js         ../tour/src/lib/run-worker.js
//	tour-client.mjs       ../tour/src/lib/tour-client.mjs
//
// The wasm is staged gzipped because Cloudflare Pages refuses files over
// 25 MiB (scripts/build-tour-wasm.sh). The row decompresses it and compares
// the module's bytes, so a gzip of a stale wasm fails exactly as a stale wasm
// did. An uncompressed nomi.wasm left beside it fails too: nothing serves it,
// and a stale one would sit there unread by every row.
//
// Rebuilding is what makes the nomi.wasm row exact rather than a heuristic:
// Go's output for a given source tree, toolchain and module directory is
// deterministic, verified here by building twice into different paths and
// requiring byte equality before the staged file is judged at all. So an
// equal hash means the staged binary is what this source builds, and the
// check needs no guess about which strings a current analyzer must emit.
//
// The four npm-vendored files (web-tree-sitter.js/.wasm, marked.esm.js,
// purify.es.mjs) are deliberately not checked: their source of truth is a
// version pinned in the script and fetched from the network, so reading it
// here would make the test need npm. They also carry a different risk —
// a pinned third-party file does not drift when Nomi's source changes.
func TestTourWasmBundleIsTheCurrentSources(t *testing.T) {
	if stagedFileMissing(t, "nomi.wasm.gz") {
		return
	}
	if _, err := os.Stat(filepath.Join(stagedDir, "nomi.wasm")); err == nil {
		t.Errorf("%s/nomi.wasm is staged beside nomi.wasm.gz. The site serves only the .gz, and Cloudflare Pages refuses the raw module (over 25 MiB). Fix: %s",
			stagedDir, rebuildInstruction)
	}

	t.Run("nomi.wasm.gz", func(t *testing.T) {
		fresh := buildTourWasmTwice(t)
		staged := gunzipStaged(t, "nomi.wasm.gz")
		compareStaged(t, staged, fresh,
			"a fresh GOOS=js GOARCH=wasm build of ./cmd/nomi-wasm")
	})
	for _, c := range []struct{ staged, source, what string }{
		{"wasm_exec.js", gorootWasmExec(t), "the Go toolchain's wasm glue"},
		{"tree-sitter-nomi.wasm", "../tree-sitter-nomi/tree-sitter-nomi.wasm", "the committed grammar wasm"},
		{"highlights.scm", "../tree-sitter-nomi/queries/highlights.scm", "the committed highlight queries"},
		{"highlight.mjs", "../tour/src/lib/highlight.mjs", "the committed client highlighter"},
		{"run-worker.js", "../tour/src/lib/run-worker.js", "the committed run worker"},
		{"tour-client.mjs", "../tour/src/lib/tour-client.mjs", "the committed tour client"},
	} {
		c := c
		t.Run(c.staged, func(t *testing.T) {
			// Not a skip: the directory is here (nomi.wasm was found
			// above), so a missing row is an incomplete bundle.
			if stagedFileMissing(t, c.staged) {
				return
			}
			if _, err := os.Stat(c.source); err != nil {
				t.Fatalf("source of truth %s is missing: %v", c.source, err)
			}
			compareStaged(t, filepath.Join(stagedDir, c.staged), c.source, c.what)
		})
	}
}

// gunzipStaged decompresses a gzipped staged file into a temporary file named
// after it (nomi.wasm.gz -> nomi.wasm) and returns that file's path.
func gunzipStaged(t *testing.T, name string) string {
	t.Helper()
	f, err := os.Open(filepath.Join(stagedDir, name))
	if err != nil {
		t.Fatalf("open staged %s: %v", name, err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("staged %s is not gzip: %v. Fix: %s", name, err, rebuildInstruction)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(zr); err != nil {
		t.Fatalf("decompress staged %s: %v. Fix: %s", name, err, rebuildInstruction)
	}
	out := filepath.Join(t.TempDir(), strings.TrimSuffix(name, ".gz"))
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write %s: %v", out, err)
	}
	return out
}

// compareStaged reports whether two files have identical contents, naming the
// remedy rather than only the difference.
func compareStaged(t *testing.T, staged, source, what string) {
	t.Helper()
	stagedSum, stagedSize := hashFile(t, staged)
	sourceSum, sourceSize := hashFile(t, source)
	if stagedSum == sourceSum {
		t.Logf("%s matches %s (%s, %d bytes)", filepath.Base(staged), what, stagedSum[:16], stagedSize)
		return
	}
	t.Errorf(`STAGED TOUR FILE IS NOT THIS SOURCE TREE'S.

  staged  %s
          sha256 %s  %d bytes  mtime %s
  source  %s (%s)
          sha256 %s  %d bytes  mtime %s

The browser serves the staged file; every test in this package that runs Nomi
runs the source tree instead. They have diverged, so the tour can show an
answer no current test can reproduce.

Fix: %s`,
		staged, stagedSum, stagedSize, modTime(t, staged),
		source, what, sourceSum, sourceSize, modTime(t, source),
		rebuildInstruction)
}

// buildTourWasmTwice builds ./cmd/nomi-wasm for js/wasm twice, into different
// output paths, and fails unless the two are byte-identical.
//
// The second build is the gate's own control. Comparing a staged binary
// against a rebuild is only meaningful if the rebuild is reproducible; if it
// were not, an unequal hash would be uninformative and this test would be a
// flake generator rather than a gate. Cached, the pair costs well under a
// second.
func buildTourWasmTwice(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	first := filepath.Join(dir, "first.wasm")
	second := filepath.Join(dir, "second.wasm")
	buildTourWasm(t, first)
	buildTourWasm(t, second)
	firstSum, _ := hashFile(t, first)
	secondSum, _ := hashFile(t, second)
	if firstSum != secondSum {
		t.Fatalf(`two builds of ./cmd/nomi-wasm from the same tree differ (%s vs %s).

This test compares the staged wasm against a rebuild, which assumes the
rebuild is reproducible. It is not here, so the comparison below would be
meaningless and the gate is reporting that instead of a false verdict.`,
			firstSum[:16], secondSum[:16])
	}
	return first
}

func buildTourWasm(t *testing.T, out string) {
	t.Helper()
	// -C first, and an absolute -o, because -C changes the working
	// directory for everything after it.
	//
	// The flag set must match scripts/build-tour-wasm.sh's wasm build.
	//
	// -buildvcs=false is load-bearing on both sides. Without it Go stamps
	// vcs.revision and vcs.modified into the binary, so the bytes move on
	// every commit and on every clean<->dirty transition even when no
	// source the build reads has changed, and this test, which compares
	// the staged file against a rebuild, would go red after every commit.
	//
	// -trimpath is on both sides as well. Without it the bundle embeds the
	// absolute path of every linked Go source file, the build machine's
	// home directory included, and the site publishes them. With it the
	// build is also path-independent: one commit built in two
	// differently-named directories gives identical bytes. Passing either
	// flag on one side only leaves this test permanently red on a freshly
	// built bundle.
	abs, err := filepath.Abs(out)
	if err != nil {
		t.Fatalf("abs(%s): %v", out, err)
	}
	cmd := exec.Command("go", "build", "-C", "..", "-trimpath", "-buildvcs=false", "-o", abs, "./cmd/nomi-wasm")
	cmd.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("GOOS=js GOARCH=wasm go build -trimpath -buildvcs=false ./cmd/nomi-wasm: %v\n%s", err, output)
	}
	// The script caps the module's memory after building it (internal/wasmmem),
	// so the rebuild must too or it can never equal the staged file.
	if err := wasmmem.CapFile(abs); err != nil {
		t.Fatalf("cap the rebuilt wasm's memory: %v", err)
	}
}

func goEnv(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.Command("go", "env", name).Output()
	if err != nil {
		t.Fatalf("go env %s: %v", name, err)
	}
	return strings.TrimSpace(string(out))
}

// gorootWasmExec resolves the glue the build script copies, with the same
// two candidates in the same order — `lib/wasm` on current toolchains,
// `misc/wasm` on older ones. Reading only the first would make this row pass
// vacuously on a toolchain that keeps the file in the second place.
func gorootWasmExec(t *testing.T) string {
	t.Helper()
	goroot := goEnv(t, "GOROOT")
	candidates := []string{
		filepath.Join(goroot, "lib", "wasm", "wasm_exec.js"),
		filepath.Join(goroot, "misc", "wasm", "wasm_exec.js"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	t.Fatalf("no wasm_exec.js under GOROOT %s (tried %v)", goroot, candidates)
	return ""
}

func hashFile(t *testing.T, path string) (string, int64) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), int64(len(data))
}

func modTime(t *testing.T, path string) string {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		return "unknown"
	}
	return info.ModTime().Format(time.RFC3339)
}

// TestTourWasmAnswersTheTourBlocks runs the staged wasm the way the browser
// does — under a JS host, through nomiRun — over every runnable tour block,
// and holds it to the same committed `<!-- expect -->` output that
// TestTourDoctests holds the in-process VM to.
//
// This is the only thing in the repository that executes the staged artifact.
// Its value beyond the byte comparison above is that byte-equality cannot see
// a bundle whose files are each current but do not work together: nomi.wasm
// and wasm_exec.js are produced by the same Go toolchain and a mismatched pair
// loads in neither the browser nor here. It also states the failure in the
// terms the user reported it in — this block, this expected output, this
// actual output — rather than as two hashes.
//
// Blocks containing tests are compared on their ERROR channel only. nomiRun
// concatenates the test report onto program output, and reproducing that
// composition here would duplicate cmd/nomi-wasm's formatting rather than
// check it. Both counts are logged so the split is visible rather than
// implied.
func TestTourWasmAnswersTheTourBlocks(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; loads a 25MB wasm under node; -short")
	}
	if stagedFileMissing(t, "nomi.wasm.gz") || stagedFileMissing(t, "wasm_exec.js") {
		return
	}
	node, err := exec.LookPath("node")
	if err != nil {
		// The one silent condition this test cannot narrow away. It is
		// tolerable because TestTourWasmBundleIsTheCurrentSources needs
		// no JS host and covers the same staleness by rebuilding: a
		// machine without node still gets the verdict, just not the
		// user-facing wording.
		if os.Getenv(requireEnv) == "1" {
			t.Fatalf("node is not on PATH, so the staged wasm cannot be run the way the browser runs it (%s=1 makes this a failure)", requireEnv)
		}
		t.Skip("node is not on PATH; the staged wasm cannot be executed the way the browser executes it")
	}

	blocks := tourRunBlocks(t)
	if len(blocks) == 0 {
		t.Fatal("no runnable ```nomi-run blocks found under ../tour/src/content/docs")
	}

	type probe struct {
		Name   string `json:"name"`
		Source string `json:"source"`
	}
	probes := make([]probe, 0, len(blocks))
	for _, b := range blocks {
		probes = append(probes, probe{Name: b.name, Source: b.block.Code})
	}
	answers := runUnderNode(t, node, probes)
	if len(answers) != len(probes) {
		t.Fatalf("staged wasm answered %d of %d blocks", len(answers), len(probes))
	}

	withTests, compared := 0, 0
	for i, b := range blocks {
		got := answers[i]
		if got.Name != b.name {
			t.Fatalf("answer %d is for %q, expected %q", i, got.Name, b.name)
		}
		t.Run(b.name, func(t *testing.T) {
			if got.Error != "" && onlyListedBlocked(got.Error) {
				// The playground reports these cases BLOCKED on the VM, the
				// set TestTourDoctests pins as tourBlockedOnVM.
				return
			}
			if got.Error != "" {
				t.Errorf(`STAGED TOUR WASM REPORTS AN ERROR ON A DOCTESTED BLOCK.

  block   %s
  error   %s

The VM linked into this test binary runs the same block clean (see
TestTourDoctests), so the browser and the source tree disagree. Fix: %s`,
					b.name, got.Error, rebuildInstruction)
				return
			}
			if b.hasTests {
				return
			}
			if b.block.Expected == nil {
				if got.Output != "" {
					t.Errorf("block %s has no <!-- expect --> block but the staged wasm printed:\n%s", b.name, got.Output)
				}
				return
			}
			if err := doctest.CheckOutput(b.block.Expected, got.Output); err != nil {
				t.Errorf(`STAGED TOUR WASM DISAGREES WITH THE DOCS.

  block %s

%s

The expected lines are the chapter's own hidden <!-- expect --> block, which
TestTourDoctests holds the in-process VM to. Fix: %s`,
					b.name, err, rebuildInstruction)
			}
		})
		if b.hasTests {
			withTests++
		} else {
			compared++
		}
	}
	t.Logf("staged wasm ran %d tour blocks: %d output-compared against their <!-- expect --> block, %d test-carrying (error channel only)",
		len(blocks), compared, withTests)
}

// TestTourWasmReportsAFailedMain runs a block whose `main` returns `Err`
// through the staged wasm, as the playground runs one: the program's output
// stays on the output channel and the failure is the error channel's
// `error: ` line, which the playground shows the way it shows a fault.
func TestTourWasmReportsAFailedMain(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; loads a 25MB wasm under node; -short")
	}
	if stagedFileMissing(t, "nomi.wasm.gz") || stagedFileMissing(t, "wasm_exec.js") {
		return
	}
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv(requireEnv) == "1" {
			t.Fatalf("node is not on PATH (%s=1 makes this a failure)", requireEnv)
		}
		t.Skip("node is not on PATH")
	}
	type probe struct {
		Name   string `json:"name"`
		Source string `json:"source"`
	}
	src := "import std/io\n\nfn main(): Result<Unit, String> {\n    io.print(\"before\")\n    Err(\"boom\")\n}\n"
	answers := runUnderNode(t, node, []probe{{Name: "failed main", Source: src}})
	if len(answers) != 1 {
		t.Fatalf("staged wasm answered %d of 1 blocks", len(answers))
	}
	if got := answers[0]; got.Output != "before\n" || got.Error != "error: boom" {
		t.Errorf("staged wasm answered output %q, error %q; want output %q, error %q",
			got.Output, got.Error, "before\n", "error: boom")
	}
}

// onlyListedBlocked reports whether a block's error text is a test report
// whose only non-passing cases are BLOCKED cases tourBlockedOnVM lists.
func onlyListedBlocked(errText string) bool {
	blocked := false
	for _, line := range strings.Split(strings.TrimSpace(errText), "\n") {
		switch {
		case strings.HasPrefix(line, "ok "):
		case strings.HasPrefix(line, "test result: BLOCKED. "):
		case blocked && strings.HasPrefix(line, "  "):
			// A blocker's hint, under its BLOCKED line.
		case strings.HasPrefix(line, "BLOCKED "):
			listed := false
			for name := range tourBlockedOnVM {
				if strings.HasPrefix(line, "BLOCKED "+name+" ") {
					listed = true
				}
			}
			if !listed {
				return false
			}
			blocked = true
		default:
			return false
		}
	}
	return blocked
}

type tourBlock struct {
	name     string
	block    doctest.Block
	hasTests bool
}

// tourRunBlocks enumerates the same population TestTourDoctests does, with the
// same extractor and the same `ignore` exclusion, so a block cannot be in one
// instrument and absent from the other.
func tourRunBlocks(t *testing.T) []tourBlock {
	t.Helper()
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
	var out []tourBlock
	for _, chapter := range chapters {
		data, err := os.ReadFile(chapter)
		if err != nil {
			t.Fatalf("read %s: %v", chapter, err)
		}
		for _, b := range doctest.ExtractBlocks(string(data), "nomi-run") {
			if b.HasInfo("ignore") {
				continue
			}
			entrySrc, _, _, _, splitErr := vmhost.SplitMultiFile(b.Code)
			if splitErr != nil {
				t.Fatalf("SplitMultiFile failed for %s:L%d: %v", chapter, b.Line, splitErr)
			}
			hasTests, err := vmhost.SourceContainsTests(entrySrc)
			if err != nil {
				t.Fatalf("SourceContainsTests failed for %s:L%d: %v", chapter, b.Line, err)
			}
			out = append(out, tourBlock{
				name:     fmt.Sprintf("%s:L%d", filepath.Base(chapter), b.Line),
				block:    b,
				hasTests: hasTests,
			})
		}
	}
	return out
}

type nodeAnswer struct {
	Name   string `json:"name"`
	Output string `json:"output"`
	Error  string `json:"error"`
}

// runUnderNode loads the staged bundle in one node process and calls its
// nomiRun for each probe, returning the answers in order.
//
// One process for the whole population: instantiating a 25MB wasm dominates
// the cost, and the browser reuses one instance across runs too (see
// run-worker.js), so this also matches how the tour actually drives it.
func runUnderNode(t *testing.T, node string, probes any) []nodeAnswer {
	t.Helper()
	dir := t.TempDir()
	driver := filepath.Join(dir, "driver.cjs")
	if err := os.WriteFile(driver, []byte(nodeDriver), 0o644); err != nil {
		t.Fatalf("write driver: %v", err)
	}
	in := filepath.Join(dir, "probes.json")
	encoded, err := json.Marshal(probes)
	if err != nil {
		t.Fatalf("marshal probes: %v", err)
	}
	if err := os.WriteFile(in, encoded, 0o644); err != nil {
		t.Fatalf("write probes: %v", err)
	}
	staged, err := filepath.Abs(stagedDir)
	if err != nil {
		t.Fatalf("abs(%s): %v", stagedDir, err)
	}

	// A tour block is doctested, so it terminates; a stale one might not,
	// and a hung node process must fail loudly rather than take the
	// package's timeout down with it.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, driver, staged, in)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("node driver over %s failed: %v\nstderr:\n%s", staged, err, stderr.String())
	}
	var answers []nodeAnswer
	if err := json.Unmarshal(stdout.Bytes(), &answers); err != nil {
		t.Fatalf("decode driver output: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if s := strings.TrimSpace(stderr.String()); s != "" {
		t.Logf("node driver stderr:\n%s", s)
	}
	return answers
}

// nodeDriver is the JS host. It is the same three steps run-worker.js performs
// — load wasm_exec.js, decompress and instantiate nomi.wasm.gz, go.run() to
// register the globals
// — kept here rather than committed as a file so it cannot drift from the test
// that uses it, and so the test needs nothing staged but the bundle itself.
const nodeDriver = `
const fs = require("fs");
const path = require("path");
const zlib = require("zlib");
const { webcrypto } = require("node:crypto");
// wasm_exec.js aborts at load without globalThis.crypto, which browsers
// provide natively and older node builds do not. internal/wasmsmoke's
// harness documents the same hazard.
if (!globalThis.crypto) globalThis.crypto = webcrypto;
const dir = process.argv[2];
require(path.join(dir, "wasm_exec.js")); // sets globalThis.Go
const go = new Go();
(async () => {
  const wasm = zlib.gunzipSync(fs.readFileSync(path.join(dir, "nomi.wasm.gz")));
  const { instance } = await WebAssembly.instantiate(wasm, go.importObject);
  go.run(instance); // registers nomiRun etc., then parks on select{}
  if (typeof globalThis.nomiRun !== "function") {
    throw new Error("nomi.wasm did not register nomiRun");
  }
  const probes = JSON.parse(fs.readFileSync(process.argv[3], "utf8"));
  const answers = probes.map((p) => {
    try {
      const r = globalThis.nomiRun(p.source);
      return { name: p.name, output: r.output, error: r.error };
    } catch (err) {
      return { name: p.name, output: "", error: "host threw: " + String(err) };
    }
  });
  process.stdout.write(JSON.stringify(answers));
  process.exit(0);
})().catch((err) => {
  process.stderr.write(String(err && err.stack ? err.stack : err));
  process.exit(1);
});
`
