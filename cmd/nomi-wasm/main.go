//go:build js && wasm

// Command nomi-wasm exposes Nomi to the browser. Built with GOOS=js
// GOARCH=wasm, it registers these global functions:
//
//	nomiRun(source)             -> { output: string, error: string } // run a program on the VM
//	nomiRunStdlibTest(module, body, context) -> { output: string, error: string }
//	nomiExpand(source)          -> { output: string, error: string } // `nomi expand` of a block's first tab
//	nomiFormat(source)          -> { output: string, error: string } // format source
//	nomiFormatTestBody(source)  -> { output: string, error: string }
//	nomiSemTokens(source)       -> string (JSON)                     // semantic tokens
//	nomiHover(source, line, col) -> string (markdown)               // LSP hover
//	nomiHoverIn(source, file, line, col) -> string (markdown)       // hover in one tab of a multi-file source
//	nomiSemTokensIn(source, ...files) -> string (JSON {file: tokens}) // semantic tokens per tab
//
// both running entirely client-side — the engine behind an interactive language
// tour (no server). The stdlib is //go:embed'd into the binary, so
// `import std/io` and friends work in the browser with no filesystem.
//
//	GOOS=js GOARCH=wasm go build -o nomi.wasm ./cmd/nomi-wasm
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"syscall/js"

	"github.com/nomi-language/nomi/internal/analysis"
	nomiformat "github.com/nomi-language/nomi/internal/format"
	"github.com/nomi-language/nomi/internal/highlight"
	"github.com/nomi-language/nomi/internal/hoverdoc"
	"github.com/nomi-language/nomi/internal/tourgo"
	"github.com/nomi-language/nomi/std"
	"github.com/nomi-language/nomi/vmhost"
)

// stdLib backs nomiHighlight's semantic layer; loaded once at startup.
var stdLib *std.StdLib

// projectDir is where a source that names modules is written as its files
// for the front end to read (vmhost.LoadMarked): a directory of the
// in-memory file system the tour's worker gives Go (go-memfs.js).
const projectDir = "/tour"

// run executes args[0] as a Nomi program and returns a JS object
// `{ output: string, error: string }`. error is "" on success.
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
	src := args[0].String()

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
		return result("", err.Error())
	}
	p, hasTests, err := vmhost.LoadMarked(src, projectDir, vmhost.WithHosts(hosts), vmhost.WithErrorOutput(&buf))
	if err != nil {
		return result(buf.String(), err.Error())
	}
	errText := ""
	if err := p.Run(context.Background(), &buf, nil, false); err != nil {
		errText = formatRunError(err)
	}
	if hasTests {
		report := formatCaseResults(p.Cases(&buf, vmhost.TestOptions{}))
		if strings.Contains(report, "test result: FAILED") || strings.Contains(report, "test result: BLOCKED") {
			errText = appendError(errText, report)
		} else if report != "" {
			fmt.Fprintln(&buf, report)
		}
	}

	return result(buf.String(), errText)
}

// expand answers what `nomi expand` prints for the first tab of args[0], a
// source that names modules (vmhost.ExpandMarked): a ```nomi-run expand
// block's output.
func expand(this js.Value, args []js.Value) (res any) {
	defer func() {
		if r := recover(); r != nil {
			res = result("", fmt.Sprintf("%v", r))
		}
	}()
	if len(args) < 1 {
		return result("", "no source provided")
	}
	text, err := vmhost.ExpandMarked(args[0].String(), projectDir)
	if err != nil {
		return result("", err.Error())
	}
	return result(text, "")
}

// formatCaseResults renders the VM's case results as `nomi test` does: a case the
// VM could not run prints one `BLOCKED <case> <blocker>` line per blocker (the
// diagnostic `nomi check` gives, its hints indented under it) and
// counts toward the summary's blocked number. What a case printed is
// indented under its line.
func formatCaseResults(cases []vmhost.CaseResult) string {
	if len(cases) == 0 {
		return ""
	}
	passed, failed, blocked := 0, 0, 0
	var report strings.Builder
	for _, c := range cases {
		name := c.Name
		if name == "" {
			name = "main"
		}
		switch {
		case c.Blocked != nil:
			blocked++
			for _, blocker := range c.Blocked {
				first, more, _ := strings.Cut(blocker, "\n")
				fmt.Fprintf(&report, "BLOCKED %s %s\n", name, first)
				if more != "" {
					report.WriteString(indent(more, "  "))
					report.WriteByte('\n')
				}
			}
			writeCaseOutput(&report, c.Output)
		case c.Err != nil:
			failed++
			fmt.Fprintf(&report, "FAIL %s\n", name)
			writeCaseOutput(&report, c.Output)
			report.WriteString(indent(formatRunError(c.Err), "  "))
			report.WriteByte('\n')
		default:
			passed++
			fmt.Fprintf(&report, "ok %s\n", name)
			writeCaseOutput(&report, c.Output)
		}
	}
	verdict := "ok"
	switch {
	case failed > 0:
		verdict = "FAILED"
	case blocked > 0:
		verdict = "BLOCKED"
	}
	fmt.Fprintf(&report, "test result: %s. %d passed, %d failed", verdict, passed, failed)
	if blocked > 0 {
		fmt.Fprintf(&report, ", %d blocked", blocked)
	}
	return report.String()
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
		return result(buf.String(), err.Error())
	}
	report := formatCaseResults(cases)
	if strings.Contains(report, "test result: FAILED") || strings.Contains(report, "test result: BLOCKED") {
		return result(buf.String(), report)
	}
	if report != "" {
		fmt.Fprintln(&buf, report)
	}
	return result(buf.String(), "")
}

func formatRunError(err error) string { return vmhost.FormatFailure(err) }

func appendError(existing, next string) string {
	if existing == "" {
		return next
	}
	if next == "" {
		return existing
	}
	return existing + "\n" + next
}

// writeCaseOutput writes what a case printed under its line, indented two
// spaces with blank lines left blank, as `nomi test` does.
func writeCaseOutput(report *strings.Builder, output string) {
	if output == "" {
		return
	}
	for _, line := range strings.Split(strings.TrimSuffix(output, "\n"), "\n") {
		if line != "" {
			report.WriteString("  " + line)
		}
		report.WriteByte('\n')
	}
}

func indent(s, prefix string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

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
	toks := highlight.SemanticTokens(args[0].String(), stdLib)
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
// there are no sibling files here and no filesystem to read them from.
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
	fa, nodes := highlight.AnalyzeNodes(src, stdLib)
	if fa == nil {
		return ""
	}
	line := args[1].Int()
	return hoverdoc.At(fa, nodes, analysis.Pos{Line: line, Col: highlight.ByteCol(src, line, args[2].Int())})
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
// built as nomiRun builds the program: every tab, nomi.toml and the
// catalogs included.
func programOf(src string) *highlight.Program {
	if lastProgram.prog == nil || lastProgram.source != src {
		lastProgram.source = src
		lastProgram.prog = highlight.AnalyzeProgram(src, projectDir, stdLib, vmhost.Macros())
	}
	return lastProgram.prog
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
	return programOf(args[0].String()).HoverIn(args[1].String(), args[2].Int(), args[3].Int())
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

func result(output, errStr string) any {
	return map[string]any{"output": output, "error": errStr}
}

func main() {
	// The analysis vmhost's front end and lowering read too, so the page
	// analyzes the stdlib once.
	stdLib = std.Shared()
	js.Global().Set("nomiRun", js.FuncOf(run))
	js.Global().Set("nomiRunStdlibTest", js.FuncOf(runStdlibTestBody))
	js.Global().Set("nomiExpand", js.FuncOf(expand))
	js.Global().Set("nomiFormat", js.FuncOf(formatSrc))
	js.Global().Set("nomiFormatTestBody", js.FuncOf(formatTestBody))
	js.Global().Set("nomiSemTokens", js.FuncOf(semTokensSrc))
	js.Global().Set("nomiHover", js.FuncOf(hoverSrc))
	js.Global().Set("nomiHoverIn", js.FuncOf(hoverInSrc))
	js.Global().Set("nomiSemTokensIn", js.FuncOf(semTokensInSrc))
	// The first VM run would otherwise lower the whole standard library inside
	// its own timed budget. Doing it here makes it part of the worker's cold
	// load, which the tour client budgets separately (LOAD_BUDGET_MS).
	vmhost.Warm()
	// Keep the Go runtime alive so the exported functions stay callable.
	select {}
}
