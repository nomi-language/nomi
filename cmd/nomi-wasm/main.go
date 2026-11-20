//go:build js && wasm

// Command nomi-wasm exposes Nomi to the browser. Built with GOOS=js
// GOARCH=wasm, it registers these global functions:
//
//	nomiRun(source)             -> { output, error, stage }          // run a program on the VM
//	nomiRunStdlibTest(module, body, context) -> { output, error, stage }
//	nomiTool(source, module, name) -> { output, error, stage, source, files } // `nomi tool` in a block's project
//	nomiFormat(source)          -> { output: string, error: string } // format source
//	nomiFormatTestBody(source)  -> { output: string, error: string }
//	nomiSemTokens(source)       -> string (JSON)                     // semantic tokens
//	nomiHover(source, line, col) -> string (markdown)               // LSP hover
//	nomiHoverIn(source, file, line, col) -> string (markdown)       // hover in one tab of a multi-file source
//	nomiSemTokensIn(source, ...files) -> string (JSON {file: tokens}) // semantic tokens per tab
//	nomiTourModules(source)     -> string (JSON [name])             // modules the tour links that source imports
//
// both running entirely client-side — the engine behind an interactive language
// tour (no server). The stdlib is //go:embed'd into the binary, so
// `import std::io` and friends work in the browser with no filesystem.
//
//	GOWORK=$PWD/internal/tourgo/tour.work GOOS=js GOARCH=wasm go build -o nomi.wasm ./cmd/nomi-wasm
//
// The workspace adds the tour modules under lib/ (the sqlite driver), whose
// Go side internal/tourgo links and the compiler's go.mod does not require;
// scripts/build-tour-wasm.sh builds it so.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"syscall/js"

	"github.com/nomi-language/nomi/internal/analysis"
	nomiformat "github.com/nomi-language/nomi/internal/format"
	"github.com/nomi-language/nomi/internal/highlight"
	"github.com/nomi-language/nomi/internal/hoverdoc"
	"github.com/nomi-language/nomi/internal/semtokens"
	"github.com/nomi-language/nomi/internal/tourgo"
	"github.com/nomi-language/nomi/internal/virtualproject"
	"github.com/nomi-language/nomi/std"
	"github.com/nomi-language/nomi/vmhost"
)

// projectDir is where a source that names modules is written as its files
// for the front end to read (vmhost.LoadMarked): a directory of the
// in-memory file system the tour's worker gives Go (go-memfs.js).
const projectDir = "/tour"

// A run's stage names where it failed, so the page can tell a program that
// did not compile from one that stopped while it ran: stageCompile when
// checking or lowering refused it (error is the diagnostics, and output
// holds no program output), stageRun when it stopped running (a trap, a
// failed `?` in main, an exit with a status; error is that failure, after
// the output), and stageTests when only its tests failed (error is their
// report). It is "" when nothing failed, and for a failure of the runtime
// itself (a recovered panic).
const (
	stageCompile = "compile"
	stageRun     = "run"
	stageTests   = "tests"
)

// run executes args[0] as a Nomi program and returns a JS object
// `{ output, error, stage }`. error is "" on success.
func run(this js.Value, args []js.Value) (res any) {
	var buf bytes.Buffer
	defer func() {
		if r := recover(); r != nil {
			res = result(buf.String(), fmt.Sprintf("%v", r))
		}
	}()

	if len(args) < 1 {
		return result("", "no source provided")
	}
	// A block that imports a module the tour links (`import sqlite`) gets
	// the module's files attached, as a project that names modules
	// (tourgo.Source).
	src := tourgo.Source(args[0].String())

	// Multi-file convention: `// FILE: <name>` markers split the source
	// into files (an optional `// FILE: nomi.toml` manifest, catalogs,
	// data files, and with a `go.mod` modules of its own); with no markers
	// the input is a single-file program named "main" (vmhost.LoadMarked).
	// The VM runs the block: one front-end pass and one IR lowering serve
	// both the program and its tests, as `nomi run` and `nomi test` do.
	// Standard error is shown in the output panel, interleaved with
	// standard output as a terminal shows them. The block's data files
	// (`// FILE: notes.txt`) seed an in-memory file system of its own, which
	// its std/fs calls reach, so every run starts from the files the block
	// shows.
	// Its `go` bindings are answered by the Go this binary links for the
	// tour's examples (internal/tourgo); one it does not link is an error
	// at the binding.
	hosts, err := tourgo.MarkedHosts(src)
	if err != nil {
		return failed("", err.Error(), stageCompile)
	}
	p, hasTests, err := vmhost.LoadMarked(src, projectDir, vmhost.WithHosts(hosts), vmhost.WithErrorOutput(&buf))
	if err != nil {
		return failed("", err.Error(), stageCompile)
	}
	stopped := ""
	if err := p.Run(context.Background(), &buf, nil, false); err != nil {
		stopped = formatRunError(err)
	}
	report := ""
	if hasTests {
		report = vmhost.CaseReport(p.Cases(&buf, vmhost.TestOptions{}))
		if !strings.Contains(report, "test result: FAILED") && !strings.Contains(report, "test result: BLOCKED") {
			if report != "" {
				fmt.Fprintln(&buf, report)
			}
			report = ""
		}
	}
	switch {
	case stopped != "":
		// The stop closes what the page shows: a failed tests' report
		// comes before it, with the output.
		if report != "" {
			fmt.Fprintln(&buf, report)
		}
		return failed(buf.String(), stopped, stageRun)
	case report != "":
		return failed(buf.String(), report, stageTests)
	}
	return result(buf.String(), "")
}

// tool runs `nomi tool <args[1]> <args[2]>` in the project of args[0], a
// block that imports a module the tour links (vmhost.ToolMarked), and
// returns `{ output, error, source, files }`: what the tool printed, its
// failure, the block's source with each of its files the tool changed put
// in, and those files by path (a ```nomi-run tool:<module>/<name> block,
// whose run follows with the source). A file the tool wrote among the
// attached modules' is left out.
func tool(this js.Value, args []js.Value) (res any) {
	var buf bytes.Buffer
	defer func() {
		if r := recover(); r != nil {
			res = result(buf.String(), fmt.Sprintf("%v", r))
		}
	}()
	if len(args) < 3 {
		return result("", "source, module and tool name required")
	}
	src := args[0].String()
	attached := tourgo.Source(src)
	hosts, err := tourgo.ToolHosts(attached, args[2].String())
	if err != nil {
		return failed("", err.Error(), stageCompile)
	}
	written, err := vmhost.ToolMarked(attached, projectDir, args[1].String(), args[2].String(), &buf, vmhost.WithHosts(hosts), vmhost.WithErrorOutput(&buf))
	if err != nil {
		if vmhost.LoadFailed(err) {
			return failed("", err.Error(), stageCompile)
		}
		return failed(buf.String(), formatRunError(err), stageRun)
	}
	files := map[string]any{}
	for name, text := range written {
		files[name] = text
	}
	return map[string]any{
		"output": buf.String(),
		"error":  "",
		"source": vmhost.ReplaceSections(src, written),
		"files":  files,
	}
}

// formatSrc formats args[0] and returns `{ output, error }`.
func formatSrc(this js.Value, args []js.Value) (res any) {
	defer func() {
		if r := recover(); r != nil {
			res = result("", fmt.Sprintf("%v", r))
		}
	}()
	if len(args) < 1 {
		return result("", "no source provided")
	}
	out, err := nomiformat.Format(args[0].String())
	if err != nil {
		return result("", err.Error())
	}
	return result(out, "")
}

// formatTestBody formats args[0], a reference-page editor's body, as `nomi
// fmt` formats it in the `//!` prompt the page rendered it from.
func formatTestBody(this js.Value, args []js.Value) (res any) {
	defer func() {
		if r := recover(); r != nil {
			res = result("", fmt.Sprintf("%v", r))
		}
	}()
	if len(args) < 1 {
		return result("", "no source provided")
	}
	out, err := nomiformat.FormatAttachedTestBody(args[0].String())
	if err != nil {
		return result("", err.Error())
	}
	return result(out, "")
}

// runStdlibTestBody executes an editable stdlib reference snippet on the VM,
// as the body of one `test "reference"` appended to the embedded stdlib module
// that produced it (vmhost.StdlibReference): the case is built in the module's
// own scope against the cached stdlib lowering, as `nomi test <std file>`
// builds the module's `//!` prompts.
func runStdlibTestBody(this js.Value, args []js.Value) (res any) {
	var buf bytes.Buffer
	defer func() {
		if r := recover(); r != nil {
			res = result(buf.String(), fmt.Sprintf("%v", r))
		}
	}()
	if len(args) < 2 {
		return result("", "module and test body required")
	}
	context := ""
	if len(args) >= 3 {
		context = args[2].String()
	}
	cases, err := vmhost.StdlibReference(args[0].String(), args[1].String(), context, &buf)
	if err != nil {
		return failed("", err.Error(), stageCompile)
	}
	report := vmhost.CaseReport(cases)
	if strings.Contains(report, "test result: FAILED") || strings.Contains(report, "test result: BLOCKED") {
		return failed(buf.String(), report, stageTests)
	}
	if report != "" {
		fmt.Fprintln(&buf, report)
	}
	return result(buf.String(), "")
}

func formatRunError(err error) string { return vmhost.FormatFailure(err) }

// semToken is the JSON shape the browser highlighter consumes for the semantic
// overlay layer. Line and Col are 1-based; Col and Len count UTF-16 code
// units, as the browser indexes the source string.
type semToken struct {
	Line int    `json:"line"`
	Col  int    `json:"col"`
	Len  int    `json:"len"`
	Type string `json:"type"`
}

// semTokensSrc returns the analyzer's semantic tokens for args[0] as a JSON
// array, for the browser to overlay on tree-sitter's syntactic highlighting.
func semTokensSrc(this js.Value, args []js.Value) (res any) {
	defer func() {
		if recover() != nil {
			res = "[]"
		}
	}()
	if len(args) < 1 {
		return "[]"
	}
	src := args[0].String()
	var toks []semtokens.Token
	if tourgo.Source(src) != src {
		// A block that imports a module the tour links is analyzed as the
		// project nomiRun loads, so the module's names resolve.
		toks = programOf(src).SemanticTokensIn(attachedEntry)
	} else {
		toks = highlight.SemanticTokens(src, std.Shared())
	}
	out := make([]semToken, 0, len(toks))
	for _, t := range toks {
		out = append(out, semToken{Line: t.Line, Col: t.Col, Len: t.Length, Type: t.Type})
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "[]"
	}
	return string(data)
}

// hoverSrc returns the hover markdown for the symbol at (line, col) — both
// 1-based, col in UTF-16 code units as the browser counts — in args[0], or
// "" if there's nothing to show. It runs the same
// renderer the LSP runs (internal/hoverdoc) over the same analysis the LSP
// builds for a single pathless buffer (highlight.Analyze, which builds the
// project). It does not call lsp.HoverAt — the LSP framework is deliberately
// out of this binary — and the agreement with Zed holds only for a snippet:
// there are no sibling files here and no filesystem to read them from. A
// call's hover ends with what it reaches outside the machine, as the LSP's
// does (reachedOf, hoverdoc.AppendReach).
func hoverSrc(this js.Value, args []js.Value) (res any) {
	defer func() {
		if recover() != nil {
			res = ""
		}
	}()
	if len(args) < 3 {
		return ""
	}
	src := args[0].String()
	if tourgo.Source(src) != src {
		return programOf(src).HoverIn(attachedEntry, args[1].Int(), args[2].Int(), reachedOf(src))
	}
	fa, nodes := highlight.AnalyzeNodes(src, std.Shared())
	if fa == nil {
		return ""
	}
	line := args[1].Int()
	inEntry := func(path string) bool {
		return virtualproject.NormalizeModuleName(path) == virtualproject.NormalizeModuleName(attachedEntry)
	}
	return hoverdoc.AtReaching(fa, nodes, src, analysis.Pos{Line: line, Col: highlight.ByteCol(src, line, args[2].Int())}, reachedOf(src), inEntry)
}

// lastReach is what the program of the last source a hover asked about
// reaches outside the machine (reachedOf). Every hover after a run asks
// about the same source, so the source's program is loaded once for them
// all until the visitor edits a tab.
var lastReach struct {
	source string
	done   bool
	calls  []vmhost.ReachedCall
}

// reachedOf is each outside call the program of src, a block, reaches,
// with the line a hover on the call ends with (vmhost.ReachedMarked,
// hoverdoc.AppendReach), as the language server shows it; nil when the
// block does not load.
func reachedOf(src string) []vmhost.ReachedCall {
	if lastReach.done && lastReach.source == src {
		return lastReach.calls
	}
	lastReach.source, lastReach.done, lastReach.calls = src, true, nil
	hosts, err := tourgo.MarkedHosts(src)
	if err != nil {
		return nil
	}
	lastReach.calls, _ = vmhost.ReachedMarked(src, projectDir, vmhost.WithHosts(hosts))
	return lastReach.calls
}

// lastProgram is the analysis of the last multi-file source nomiHoverIn or
// nomiSemTokensIn was asked about. A run asks for the source's tokens and
// every hover after it asks about the same source, so one analysis serves
// them all until the visitor edits a tab.
var lastProgram struct {
	source string
	prog   *highlight.Program
}

// programOf answers the whole-program analysis of a multi-file source,
// built as nomiRun builds the program: every tab, nomi.toml, the catalogs
// and the modules the tour links that it imports included.
func programOf(src string) *highlight.Program {
	if lastProgram.prog == nil || lastProgram.source != src {
		lastProgram.source = src
		lastProgram.prog = highlight.AnalyzeProgram(tourgo.Source(src), projectDir, std.Shared())
	}
	return lastProgram.prog
}

// attachedEntry is a single-file block's file once tourgo.Source has
// attached the modules it imports.
const attachedEntry = "main.nomi"

// tourModules answers the names of the modules the tour links that the
// source args[0] imports, as a JSON array: the worker loads what a
// module's Go side needs in the page (SQLite's WebAssembly build for
// sqlite) before it runs the source.
func tourModules(this js.Value, args []js.Value) (res any) {
	defer func() {
		if recover() != nil {
			res = "[]"
		}
	}()
	if len(args) < 1 {
		return "[]"
	}
	data, err := json.Marshal(tourgo.Imported(args[0].String()))
	if err != nil {
		return "[]"
	}
	return string(data)
}

// hoverInSrc is hoverSrc for one tab of a multi-file source (args[0], the
// tabs stitched with `// FILE:` markers): the hover markdown at (args[2],
// args[3]) in the file args[1] names, positions counted in that file's own
// text as hoverSrc counts them. The file is analyzed as part of the whole
// program, so a name it imports from another tab resolves.
func hoverInSrc(this js.Value, args []js.Value) (res any) {
	defer func() {
		if recover() != nil {
			res = ""
		}
	}()
	if len(args) < 4 {
		return ""
	}
	return programOf(args[0].String()).HoverIn(args[1].String(), args[2].Int(), args[3].Int(), reachedOf(args[0].String()))
}

// semTokensInSrc answers the semantic tokens of every Nomi tab of a
// multi-file source (args[0]) as a JSON object from the tab's file name
// (args[1..], `cart.nomi`) to its tokens, positions in the tab's own text.
func semTokensInSrc(this js.Value, args []js.Value) (res any) {
	defer func() {
		if recover() != nil {
			res = "{}"
		}
	}()
	if len(args) < 1 {
		return "{}"
	}
	prog := programOf(args[0].String())
	out := map[string][]semToken{}
	for _, name := range args[1:] {
		toks := prog.SemanticTokensIn(name.String())
		list := make([]semToken, 0, len(toks))
		for _, t := range toks {
			list = append(list, semToken{Line: t.Line, Col: t.Col, Len: t.Length, Type: t.Type})
		}
		out[name.String()] = list
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// warmOnce lowers the whole standard library, which every run's lowering
// reads (vmhost.Warm). The page shows a block's recorded output and asks only
// for its semantic tokens and hovers until the reader edits it, and those
// need the analysis alone, so the lowering waits for the first run: the
// worker calls nomiWarm before it posts that run's start, inside the load
// allowance (LOAD_BUDGET_MS) and outside the run's own budget.
var warmOnce sync.Once

func warm(this js.Value, args []js.Value) any {
	warmOnce.Do(vmhost.Warm)
	return nil
}

func result(output, errStr string) any {
	return map[string]any{"output": output, "error": errStr}
}

// failed is a run's answer when it failed at stage (stageCompile, stageRun
// or stageTests).
func failed(output, errStr, stage string) any {
	return map[string]any{"output": output, "error": errStr, "stage": stage}
}

func main() {
	// The analysis vmhost's front end and lowering read too, so the page
	// analyzes the stdlib once.
	std.Shared()
	js.Global().Set("nomiRun", js.FuncOf(run))
	js.Global().Set("nomiRunStdlibTest", js.FuncOf(runStdlibTestBody))
	js.Global().Set("nomiTool", js.FuncOf(tool))
	js.Global().Set("nomiFormat", js.FuncOf(formatSrc))
	js.Global().Set("nomiFormatTestBody", js.FuncOf(formatTestBody))
	js.Global().Set("nomiSemTokens", js.FuncOf(semTokensSrc))
	js.Global().Set("nomiHover", js.FuncOf(hoverSrc))
	js.Global().Set("nomiHoverIn", js.FuncOf(hoverInSrc))
	js.Global().Set("nomiSemTokensIn", js.FuncOf(semTokensInSrc))
	js.Global().Set("nomiTourModules", js.FuncOf(tourModules))
	js.Global().Set("nomiWarm", js.FuncOf(warm))
	// Keep the Go runtime alive so the exported functions stay callable.
	select {}
}
