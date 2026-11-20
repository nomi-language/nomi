package main

import (
	"fmt"
	"html"
	"slices"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/vmhost"
)

// stdProgram is one std module's lowered tests (vmhost.LoadEmbeddedStdlib),
// loaded on first use and once per process: the program a page's prompts
// are classified by (promptClassifier) and its functions' outside calls
// are found from (outsideCall).
type stdProgram struct {
	module  string
	once    sync.Once
	prog    *vmhost.Program
	outside map[vmhost.DeclAt]string
}

var (
	stdProgramsMu sync.Mutex
	stdPrograms   = map[string]*stdProgram{}
)

// stdProgramOf is module's stdProgram, the same one for every caller.
func stdProgramOf(module string) *stdProgram {
	stdProgramsMu.Lock()
	defer stdProgramsMu.Unlock()
	p := stdPrograms[module]
	if p == nil {
		p = &stdProgram{module: module}
		stdPrograms[module] = p
	}
	return p
}

func (p *stdProgram) load() {
	p.once.Do(func() {
		prog, err := vmhost.LoadEmbeddedStdlib(p.module)
		if err != nil {
			panic(fmt.Sprintf("nomi-docgen: loading std/%s: %v", p.module, err))
		}
		p.prog = prog
		p.outside = prog.StdOutsideCalls()
	})
}

// program is the module's lowered tests.
func (p *stdProgram) program() *vmhost.Program {
	p.load()
	return p.prog
}

// outsideCall is the name a call to the function s, which the module
// declares, is an outside call by, as a capability list holds it
// (`std::fs`), or "" when a call to it is none (Program.StdOutsideCalls):
// the one decision a page's line under the signature and the module's
// overview read.
func (p *stdProgram) outsideCall(s *analysis.Symbol) string {
	if s == nil || s.Kind != analysis.SymbolFunction {
		return ""
	}
	p.load()
	return p.outside[vmhost.DeclAt{Line: s.Pos.Line, Col: s.Pos.Col}]
}

// outsideNames is the names the module's entries' calls are outside calls
// by, in order of first appearance.
func (p *stdProgram) outsideNames(entries []apiEntry) []string {
	var names []string
	for _, e := range entries {
		if name := p.outsideCall(e.Sym); name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

// capabilityListsLink is the tooling page's section on the lists a project
// holds its outside calls to.
const capabilityListsLink = "/tooling/projects/#capability-lists"

// outsideText is the line under the signature of a function a call to
// which is an outside call by name, as Markdown.
func outsideText(name string) string {
	return fmt.Sprintf("Reaches outside the program: `%s`.", name)
}

// outsideOverview is the sentence a module's overview carries when calls to
// some of its functions are outside calls by names, as Markdown.
func outsideOverview(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = "`" + n + "`"
	}
	return fmt.Sprintf(
		"Functions marked \"Reaches outside the program\" act on or read from something outside it, and a project with [capability lists](%s) must allow %s to call one.",
		capabilityListsLink, strings.Join(quoted, " and "),
	)
}

// withOutsideLine is body, an entry's rendering (hoverdoc.Render), with line
// put right under its signature: after the opening ```nomi fence's block.
func withOutsideLine(body, line string) string {
	if line == "" {
		return body
	}
	if strings.HasPrefix(body, "```") {
		if end := strings.Index(body[3:], "\n```"); end >= 0 {
			cut := 3 + end + len("\n```")
			return body[:cut] + "\n\n" + line + body[cut:]
		}
	}
	return line + "\n\n" + body
}

// outsideHTML is outsideText as the reference page writes it.
func outsideHTML(name string) string {
	return fmt.Sprintf("<p class=\"nomi-ref-reach\">Reaches outside the program: <code>%s</code>.</p>", html.EscapeString(name))
}
