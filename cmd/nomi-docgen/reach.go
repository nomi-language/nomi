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
// are classified by (promptClassifier) and its functions' effects are
// found from (effectName), with the functions the walk answers for
// (Program.StdEffects).
type stdProgram struct {
	module   string
	once     sync.Once
	prog     *vmhost.Program
	effects  map[vmhost.DeclAt]string
	answered map[vmhost.DeclAt]bool
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
		p.effects, p.answered = prog.StdEffects()
	})
}

// program is the module's lowered tests.
func (p *stdProgram) program() *vmhost.Program {
	p.load()
	return p.prog
}

// effectDecl is whether the walk Program.StdEffects runs answers for a
// declaration of kind k: a function, by its body or its crossing, and a
// `once`, by its initializer.
func effectDecl(k analysis.SymbolKind) bool {
	return k == analysis.SymbolFunction || k == analysis.SymbolOnce
}

// effectName is the name of the effect a call to the function s, or a read
// of the `once` s, which the module declares, has, as an effects list holds
// it (`std::fs`), or "" when it has none (Program.StdEffects): the one
// decision a page's line under the signature and the module's overview
// read.
func (p *stdProgram) effectName(s *analysis.Symbol) string {
	if s == nil || !effectDecl(s.Kind) {
		return ""
	}
	p.load()
	return p.effects[vmhost.DeclAt{Line: s.Pos.Line, Col: s.Pos.Col}]
}

// effectNames is the names of the effects the module's entries have, in
// order of first appearance.
func (p *stdProgram) effectNames(entries []apiEntry) []string {
	var names []string
	for _, e := range entries {
		if name := p.effectName(e.Sym); name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

// effectListsLink is the tooling page's section on the lists a project
// holds its effects to.
const effectListsLink = "/tooling/projects/#effects-lists"

// effectText is the line under the signature of a function a call to
// which, or a `once` a read of which, has the effect name, as Markdown.
func effectText(name string) string {
	return fmt.Sprintf("Has effects: `%s`.", name)
}

// effectOverview is the sentence a module's overview carries when some of
// its entries, functions or `once` values, have the effects names, as
// Markdown.
func effectOverview(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = "`" + n + "`"
	}
	return fmt.Sprintf(
		"Entries marked \"Has effects\" have effects, and a project with [effects lists](%s) must allow %s to use one.",
		effectListsLink, strings.Join(quoted, " and "),
	)
}

// withEffectLine is body, an entry's rendering (hoverdoc.Render), with line
// put right under its signature: after the opening ```nomi fence's block.
func withEffectLine(body, line string) string {
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

// effectHTML is effectText as the reference page writes it.
func effectHTML(name string) string {
	return fmt.Sprintf("<p class=\"nomi-ref-reach\">Has effects: <code>%s</code>.</p>", html.EscapeString(name))
}
