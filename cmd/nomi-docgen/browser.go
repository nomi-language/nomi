package main

import (
	"fmt"
	"html"
	"io"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/osreach"
	"github.com/nomi-language/nomi/vmhost"
)

// The target the tour's worker runs a reference editor on: the js/wasm
// build of the VM.
const browserGOOS, browserGOARCH = "js", "wasm"

// promptClassifier answers, for each `//!` prompt of one stdlib module,
// the capability classes it reaches that the browser lacks
// (vmhost.TargetLacks for js/wasm): a prompt that listens on a socket,
// dials one, looks a name up or starts a program. The walk is the one
// `nomi build --target js/wasm` refuses a program by, run from the
// prompt's own test case in the module's lowered tests, so it counts the
// std calls the prompt reaches on its behalf (`std::mail::testing.with_smtp_server`
// reaches `net.listen`). An HTTP request is not among them: the browser
// build sends it through fetch.
type promptClassifier struct {
	module string
	prog   *vmhost.Program
	lacks  []string
}

func newPromptClassifier(module string) *promptClassifier {
	return &promptClassifier{module: module, lacks: vmhost.TargetLacks(browserGOOS, browserGOARCH)}
}

// browserLacks is the classes, sorted, that test reaches and the browser
// lacks, or nil when the browser can run it. body and context are the
// editor's, as the page writes them. The module is loaded on the first
// call, so a page with no prompts loads nothing.
//
// The walk counts what the prompt could reach: a call through an
// interface reaches every impl, and a crossing a fake answers on the frame
// (std::process::testing.run's) still counts. So a prompt the walk flags is run
// once, natively, as its editor runs (vmhost.StdlibReference), and keeps
// only the classes whose calls went on to the operating system
// (osreach). A run that fails, or that cannot listen here, keeps every
// class the walk found.
func (c *promptClassifier) browserLacks(test ast.AttachedTest, body, context string) []string {
	if c.prog == nil {
		prog, err := vmhost.LoadEmbeddedStdlib(c.module)
		if err != nil {
			panic(fmt.Sprintf("nomi-docgen: loading std/%s to classify its prompts: %v", c.module, err))
		}
		c.prog = prog
	}
	var walked []string
	for _, u := range c.prog.TestCapabilityUses(vmhost.TestOptions{Line: test.Line, LineSet: true}) {
		if slices.Contains(c.lacks, u.Class) && !slices.Contains(walked, u.Class) {
			walked = append(walked, u.Class)
		}
	}
	if len(walked) == 0 {
		return nil
	}
	slices.Sort(walked)
	before := make([]int64, len(walked))
	for i, class := range walked {
		before[i] = osreach.Count(class)
	}
	cases, err := vmhost.StdlibReference(c.module, body, context, io.Discard)
	if err != nil || len(cases) != 1 || cases[0].Err != nil || cases[0].Blocked != nil {
		return walked
	}
	var reached []string
	for i, class := range walked {
		if osreach.Count(class) != before[i] {
			reached = append(reached, class)
		}
	}
	return reached
}

// browserNote is the line under a prompt the page does not run, saying
// why and what does run it.
func browserNote(lacking []string) string {
	what := make([]string, len(lacking))
	for i, class := range lacking {
		what[i] = vmhost.ClassDoing(class)
	}
	return fmt.Sprintf(
		"<p class=\"nomi-ref-note\">Not run here: this example %s, which a browser cannot do. <code>nomi test</code> runs it.</p>\n\n",
		html.EscapeString(strings.Join(what, " and ")),
	)
}
