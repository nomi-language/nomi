package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/std"
	"github.com/nomi-language/nomi/vmhost"
)

// referenceFunction is one public function or `once` a std module's
// reference page documents, with what the effects walk its "Has effects"
// line reads (Program.StdEffects) answers for it.
type referenceFunction struct {
	// module is the std module, as a list names it (`std::fs`).
	module string
	// label is the function's heading on the page (`fs.read_text`,
	// `Seed.from_os`).
	label string
	sym   *analysis.Symbol
	// answered is whether the walk answers for the function, and effect
	// the name a list must hold for a call to it, "" for none.
	answered bool
	effect   string
}

// referenceFunctions is every public function and `once` the stdlib
// reference documents, module by module, each with the walk's answer.
func referenceFunctions(lib *std.StdLib) []referenceFunction {
	var out []referenceFunction
	for _, name := range referenceCandidates(lib) {
		entries := sortedAPIEntries(lib.Files[name], lib.Nodes[name])
		if len(entries) == 0 {
			continue
		}
		program := stdProgramOf(name)
		program.load()
		for _, e := range entries {
			if !effectDecl(e.Sym.Kind) {
				continue
			}
			at := vmhost.DeclAt{Line: e.Sym.Pos.Line, Col: e.Sym.Pos.Col}
			out = append(out, referenceFunction{
				module:   "std::" + stdImportPath(name),
				label:    entryLabel(name, e),
				sym:      e.Sym,
				answered: program.answered[at],
				effect:   program.effects[at],
			})
		}
	}
	return out
}

// effectTableHeader opens the table in docs/tooling.md's "Effects lists"
// that names the functions with effects.
const effectTableHeader = "| Module | Functions with effects |"

// effectTable is the table as docs/tooling.md holds it: for each name a
// list holds, the public functions a call to which has that effect, as the
// reference marks them.
func effectTable(fns []referenceFunction) string {
	withEffect := map[string][]string{}
	for _, f := range fns {
		if f.answered && f.effect != "" {
			label := f.label
			if f.effect != f.module {
				label = f.module + " " + label
			}
			withEffect[f.effect] = append(withEffect[f.effect], label)
		}
	}
	return renderFunctionTable(effectTableHeader, withEffect)
}

// renderFunctionTable is rows, each a std name and its functions, as a
// Markdown table under header, rows and functions sorted.
func renderFunctionTable(header string, rows map[string][]string) string {
	names := make([]string, 0, len(rows))
	for n := range rows {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString(header + "\n|---|---|\n")
	for _, n := range names {
		fns := slices.Clone(rows[n])
		sort.Strings(fns)
		fns = slices.Compact(fns)
		for i, f := range fns {
			fns[i] = "`" + f + "`"
		}
		fmt.Fprintf(&b, "| `%s` | %s |\n", n, strings.Join(fns, ", "))
	}
	return b.String()
}

// tableAt is the Markdown table in text that header opens, through its
// last row, with the newline that ends it, or "" when text holds none.
func tableAt(text, header string) string {
	start := strings.Index(text, header)
	if start < 0 {
		return ""
	}
	end := strings.Index(text[start:], "\n\n")
	if end < 0 {
		end = len(text) - start
	}
	return text[start:start+end] + "\n"
}

// tableFunctions is each function a table tableAt read names, as its
// module and label joined by a space (`std::fs fs.read_text`).
func tableFunctions(table string) []string {
	lines := strings.Split(table, "\n")
	if len(lines) < 3 {
		return nil
	}
	var out []string
	for _, line := range lines[2:] {
		cells := strings.Split(line, "|")
		if len(cells) < 3 {
			continue
		}
		module := strings.Trim(strings.TrimSpace(cells[1]), "`")
		for _, f := range strings.Split(cells[2], ",") {
			out = append(out, module+" "+strings.Trim(strings.TrimSpace(f), "`"))
		}
	}
	return out
}

// checkTable fails t unless docs/tooling.md's table that want's header
// opens is want, printing want to paste.
func checkTable(t *testing.T, text, want string) {
	t.Helper()
	header := want[:strings.Index(want, "\n")]
	got := tableAt(text, header)
	if got == "" {
		t.Errorf("docs/tooling.md holds no table headed %q; add it under \"What counts as an effect\":\n\n%s", header, want)
		return
	}
	if got != want {
		t.Errorf("docs/tooling.md's table headed %q is not the one the reference's walk gives; replace it with:\n\n%s", header, want)
	}
}

// toolingText is docs/tooling.md.
func toolingText(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "tooling.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The tooling reference's table of functions with effects is the public
// std functions the reference marks "Has effects", by the name a list
// holds, from the walk the marks come from. So the table and the
// reference cannot disagree, and a function that starts or stops having
// an effect shows in both.
func TestEffectTableMatchesTheReference(t *testing.T) {
	effects := effectTable(referenceFunctions(std.Load()))
	checkTable(t, toolingText(t), effects)
	t.Logf("%d modules, %d functions with effects", strings.Count(effects, "\n")-2, len(tableFunctions(effects)))
}

// Every public std function the reference documents is answered by the
// walk the reference's "Has effects" lines come from, so a new function
// the walk cannot see, a generic one whose abstract body declines above
// all, fails here rather than reading as pure.
func TestReferenceEffectsAnswerEveryFunction(t *testing.T) {
	fns := referenceFunctions(std.Load())
	for _, f := range fns {
		if f.answered {
			continue
		}
		key := f.module + " " + f.label
		if analysis.IsSynthesizedLine(f.sym.Pos.Line) {
			t.Errorf("%s, a derived body, has no effects answer: a test of the module that uses the derived body at some type lowers it, or its abstract body does once the builder lowers what it declines at", key)
		} else {
			t.Errorf("%s at line %d has no effects answer: let its abstract body lower (irbuild.StdAbstractBodies, or the program's own for a module lowered with each program), or give the function a `//!` example that calls it, so its module's tests lower the body the walk reads", key, f.sym.Pos.Line)
		}
	}
	t.Logf("%d public std functions, each answered", len(fns))
}
