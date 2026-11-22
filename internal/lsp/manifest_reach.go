package lsp

import (
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/vmhost"
)

// THE EFFECTS A PROJECT'S PROGRAMS HAVE, IN ITS NOMI.TOML.
//
// A name in nomi.toml's effects lists is answered from the effects the
// programs `nomi check` checks in the project have, worked out as `nomi
// effects` works them out (frontend.CheckedFiles, vmhost.ReachedAll):
// from the Nomi files as they are on disk, with the editor's text of
// nomi.toml. Hover on a std name shows the calls that need it, as `nomi
// effects --why` prints them, and references answer those calls.
// A std name that nothing a list holds has, the name `nomi
// effects --check` reports unused (vmhost.HeldChanges), is a hint at
// the name in each open nomi.toml, with a fix that takes it out
// (unusedHints). The answer is worked out once per project and kept until
// a nomi.toml or a Nomi file changes (projectReaches.changed), which
// publishes the hints again (reachChanged). A project with a program that
// does not check has no answer, since its effects are unknown, and no
// hints.

// reachChanged drops what the projects' programs reach (projectReaches)
// and publishes each open nomi.toml's hints again: a nomi.toml or a Nomi
// file changed.
func (s *Server) reachChanged() {
	s.reaches.changed()
	u := &s.unused
	u.mu.Lock()
	defer u.mu.Unlock()
	for uri := range u.open {
		u.open[uri]++
		go s.publishUnused(uri, u.open[uri])
	}
}

// unusedNames holds the open nomi.toml files, each with the count of its
// hints' refreshes, and the fixes of the hints published in each.
type unusedNames struct {
	mu    sync.Mutex
	open  map[string]int
	fixes map[string][]parser.ParseError
}

// opened notes that the nomi.toml at uri is open in an editor; its hints
// follow on the next change (reachChanged), which an open's recheck is.
func (u *unusedNames) opened(uri string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.open == nil {
		u.open, u.fixes = map[string]int{}, map[string][]parser.ParseError{}
	}
	if _, ok := u.open[uri]; !ok {
		u.open[uri] = 0
	}
}

// closeUnused forgets the closed nomi.toml at uri and clears its hints.
func (s *Server) closeUnused(uri string) {
	u := &s.unused
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.open, uri)
	delete(u.fixes, uri)
	s.publishInputDiagnostics(unusedSource(uri), nil)
}

// fixesIn are the fixes of the hints published in the nomi.toml at uri.
func (u *unusedNames) fixesIn(uri string) []parser.ParseError {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.fixes[uri]
}

// manifestFixes are the fixes the code actions in the nomi.toml at uri
// offer: those of its hints (unusedHints), and those of the errors the
// open documents' lowerings found in it (manifestDiagnostics).
func (s *Server) manifestFixes(uri string) []parser.ParseError {
	return append(s.unused.fixesIn(uri), s.lowering.manifestFixesIn(uri)...)
}

// unusedSource is the source key the hints in the nomi.toml at uri are
// kept under (inputDiagnostics.bySource).
func unusedSource(uri string) string { return "unused " + uri }

// publishUnused publishes the hints in the open nomi.toml at uri
// (unusedHints), unless a later refresh, seq being this one's, or a close
// came first.
func (s *Server) publishUnused(uri string, seq int) {
	defer recoverPanic("publishing the unused names in "+uri, nil)
	r := s.reaches.of(filepath.Dir(uriToPath(uri)))
	text, _ := s.contentForURI(protocol.DocumentUri(uri))
	diags, fixes := unusedHints(r, uriToPath(uri), text)
	u := &s.unused
	u.mu.Lock()
	defer u.mu.Unlock()
	if at, open := u.open[uri]; !open || at != seq {
		return
	}
	u.fixes[uri] = fixes
	s.publishInputDiagnostics(unusedSource(uri), map[string][]protocol.Diagnostic{uri: diags})
}

// unusedHints are the hints in text, the nomi.toml at path, r being what
// its project's programs reach: one at each std name a list holds that
// `nomi effects --check` reports unused (vmhost.HeldChanges), saying
// why (unusedName), and the fix of each, which writes the tables without
// it; none for no r or a text that does not parse.
func unusedHints(r *vmhost.Reach, path, text string) ([]protocol.Diagnostic, []parser.ParseError) {
	if r == nil {
		return nil, nil
	}
	data := []byte(text)
	m, err := analysis.ParseManifestIn(filepath.Dir(path), data)
	if err != nil || m.Effects == nil {
		return nil, nil
	}
	lists, tight := m.Effects, r.Tightest()
	lines := newLineIndex(text)
	severity := protocol.DiagnosticSeverityHint
	source := "nomi"
	var diags []protocol.Diagnostic
	var fixes []parser.ParseError
	for _, c := range vmhost.HeldChanges(analysis.EffectChanges(lists, tight), lists) {
		if c.Change != "remove" || c.Name == "" || !isStdList(c.List) {
			continue
		}
		key := append([]string{"effects"}, strings.Split(c.List, ".")...)
		if slices.Contains(analysis.EntryLists, c.List) {
			key = analysis.EntryKeyPath(c.List, c.Module)
		}
		line := analysis.ManifestEntryLine(data, c.Name, key...)
		if line == 0 {
			continue
		}
		lineText, _ := lines.line(uint32(line - 1))
		start, end := 0, len(lineText)
		if i := strings.Index(lineText, fmt.Sprintf("%q", c.Name)); i >= 0 {
			start, end = i, i+len(c.Name)+2
		}
		message, title, to := unusedName(lists, tight, c)
		diags = append(diags, protocol.Diagnostic{
			Range: lines.utf16Range(posRange(line, start+1, line, end+1)), Severity: &severity, Source: &source,
			Message: message, Tags: []protocol.DiagnosticTag{protocol.DiagnosticTagUnnecessary},
		})
		if fix, err := analysis.EffectsFix(path, data, title, to); err == nil {
			fixes = append(fixes, parser.ParseError{Line: line, Col: start + 1, Message: message, Fix: fix})
		}
	}
	return diags, fixes
}

// unusedName is what a hint says of c, a name lists holds that the
// tightest tables tight do not, the title of its fix and the tables the
// fix writes: lists without the name. A name std holds that only the tests
// reach moves to std_in_tests, and a name std_in_tests holds that std
// allows already is said to be.
func unusedName(lists, tight *vmhost.Effects, c analysis.EffectChange) (message, title string, to *vmhost.Effects) {
	to = lists.Clone()
	without := func(names []string) []string {
		return slices.DeleteFunc(slices.Clone(names), func(n string) bool { return n == c.Name })
	}
	message = nothingReaches(c.List, c.Module, c.Name)
	_, key, _ := strings.Cut(c.List, ".")
	title = fmt.Sprintf("Remove %s from %s", c.Name, key)
	// std and std_in_tests of the table c is in, as lists has and tight
	// would have them.
	var std, inTests *[]string
	var tightStd, tightInTests []string
	switch c.List {
	case analysis.ListSelfStd, analysis.ListSelfStdInTests:
		std, inTests, tightStd, tightInTests = &to.Self.Std, &to.Self.StdInTests, tight.Self.Std, tight.Self.StdInTests
	case analysis.ListDepsStd, analysis.ListDepsStdInTests:
		std, inTests, tightStd, tightInTests = &to.Deps.Std, &to.Deps.StdInTests, tight.Deps.Std, tight.Deps.StdInTests
	default:
		to.SetEntry(c.List, c.Module, without(to.Entries(c.List)[c.Module]))
		return message, fmt.Sprintf("Remove %s from the entry for %s", c.Name, c.Module), to
	}
	switch {
	case strings.HasSuffix(c.List, "_in_tests"):
		*inTests = without(*inTests)
		if slices.Contains(tightStd, c.Name) {
			message = fmt.Sprintf("std already allows %s to the tests", c.Name)
		}
	case slices.Contains(tightInTests, c.Name):
		*std = without(*std)
		if !slices.Contains(*inTests, c.Name) {
			*inTests = append(*inTests, c.Name)
			title = fmt.Sprintf("Move %s to std_in_tests", c.Name)
		}
		message = fmt.Sprintf("only the tests have the effect %s, which std_in_tests is for", c.Name)
	default:
		*std = without(*std)
	}
	return message, title, to
}

// projectReaches holds what each project's programs reach, by the
// project's root.
type projectReaches struct {
	mu    sync.Mutex
	roots map[string]*rootReach
	// gen counts the changes; a reach worked out before the latest is
	// worked out again.
	gen int
	// computed counts the reaches worked out (tests).
	computed atomic.Int64
}

// rootReach is one project's reach: reach, of the generation gen (-1 for
// none yet), nil when a program does not check. compute is held while it
// is worked out, so two requests work it out once.
type rootReach struct {
	compute sync.Mutex
	gen     int
	reach   *vmhost.Reach
}

// changed drops every project's reach: a nomi.toml or a Nomi file changed.
func (p *projectReaches) changed() {
	p.mu.Lock()
	p.gen++
	p.mu.Unlock()
}

// of is what the programs of the project at root reach, worked out when
// what was kept is older than the latest change; nil when a program does
// not check.
func (p *projectReaches) of(root string) *vmhost.Reach {
	p.mu.Lock()
	if p.roots == nil {
		p.roots = map[string]*rootReach{}
	}
	rr := p.roots[root]
	if rr == nil {
		rr = &rootReach{gen: -1}
		p.roots[root] = rr
	}
	p.mu.Unlock()
	rr.compute.Lock()
	defer rr.compute.Unlock()
	p.mu.Lock()
	gen := p.gen
	if rr.gen == gen {
		r := rr.reach
		p.mu.Unlock()
		return r
	}
	p.mu.Unlock()
	r := projectReach(root)
	p.computed.Add(1)
	p.mu.Lock()
	if p.gen == gen {
		rr.gen, rr.reach = gen, r
	}
	p.mu.Unlock()
	return r
}

// projectReach is what the programs `nomi check root` checks reach, as
// `nomi effects root` works it out; nil when one does not check, or
// the compiler panics.
func projectReach(root string) (r *vmhost.Reach) {
	defer func() {
		if recover() != nil {
			r = nil
		}
	}()
	files, _, _, err := frontend.CheckedFiles(root)
	if err != nil {
		return nil
	}
	return vmhost.ReachedAll(files, nil, vmhost.WithWarnings(io.Discard))
}

// manifestHover is the hover at pos in the nomi.toml at uri: on a std name
// in an effects list, the calls that need it there (nameHover); on a key
// in a table of entries, what that code reaches (moduleHover); nil
// anywhere else.
func (s *Server) manifestHover(uri string, pos protocol.Position) *protocol.Hover {
	text, _ := s.contentForURI(protocol.DocumentUri(uri))
	at := manifestPlaceAt(text, pos)
	name, start, end, ok := at.quoted()
	if !ok {
		return nil
	}
	root := filepath.Dir(uriToPath(uri))
	var md string
	switch list := at.list(); {
	case isStdList(list) && strings.HasPrefix(name, "std::"):
		md = nameHover(s.reaches.of(root), list, at.key, name)
	case at.entryList() != "":
		md = moduleHover(s.reaches.of(root), at.entryList(), name)
	default:
		return nil
	}
	return &protocol.Hover{
		Contents: protocol.MarkupContent{Kind: protocol.MarkupKindMarkdown, Value: md},
		Range:    ptrRange(at.quotedRange(start, end)),
	}
}

// manifestReferences are the calls the std name at pos in the nomi.toml at
// uri is needed by in its list (neededBy), each at its callee.
func (s *Server) manifestReferences(uri string, pos protocol.Position) []protocol.Location {
	text, _ := s.contentForURI(protocol.DocumentUri(uri))
	at := manifestPlaceAt(text, pos)
	name, _, _, ok := at.quoted()
	list := at.list()
	if !ok || !isStdList(list) {
		return nil
	}
	r := s.reaches.of(filepath.Dir(uriToPath(uri)))
	if r == nil {
		return nil
	}
	var out []protocol.Location
	for _, c := range neededBy(r, list, at.key, name) {
		target := protocol.DocumentUri(pathToURI(c.Path))
		rng := posRange(c.Line, c.Col, c.EndLine, c.EndCol)
		// The calls are in the files as the programs read them, from disk.
		if text, ok := diskContent(target); ok {
			rng = newLineIndex(text).utf16Range(rng)
		}
		out = append(out, protocol.Location{URI: target, Range: rng})
	}
	return out
}

// isStdList reports whether list, as a change names it, holds std names.
func isStdList(list string) bool {
	switch list {
	case analysis.ListSelfStd, analysis.ListSelfStdInTests, analysis.ListDepsStd, analysis.ListDepsStdInTests:
		return true
	}
	return slices.Contains(analysis.EntryLists, list)
}

// maxHoverCalls is how many calls a hover on a name lists before it counts
// the rest.
const maxHoverCalls = 10

// nameHover is what a hover on the std name in list (module's entry for a
// table of entries) says, r being the effects the project's programs have:
// the calls that need it there, as `nomi effects --why` prints them, at
// most maxHoverCalls; that nothing there has it; or, for no r, that which
// code has it is not known.
func nameHover(r *vmhost.Reach, list, module, name string) string {
	if r == nil {
		return fmt.Sprintf("Which code has the effect `%s` is known once every program of this project checks.", name)
	}
	calls := neededBy(r, list, module, name)
	if len(calls) == 0 {
		msg := nothingReaches(list, module, name)
		return strings.ToUpper(msg[:1]) + msg[1:] + "."
	}
	var b strings.Builder
	r.WriteWhy(&b, name, calls, maxHoverCalls)
	return "```text\n" + b.String() + "```"
}

// moduleHover is what a hover on module, a key of list's entries (a
// dependency's path, or a module of the project), says, r being the
// effects the project's programs have: the effects the module's own files have, as
// `nomi add` prints them (vmhost.WriteModuleReach), or that no program
// imports it yet; for no r, that its effects are not known.
func moduleHover(r *vmhost.Reach, list, module string) string {
	if r == nil {
		return fmt.Sprintf("The effects of `%s` are known once every program of this project checks.", module)
	}
	var b strings.Builder
	b.WriteString(module + "\n")
	modules := r.EntryReaches(list)
	if i := slices.IndexFunc(modules, func(u vmhost.ModuleReach) bool { return u.Path == module }); i >= 0 {
		vmhost.WriteModuleReach(&b, "  ", modules[i])
	} else {
		b.WriteString("  not imported yet\n")
	}
	return "```text\n" + b.String() + "```"
}

// neededBy is each call r reaches that has the effect name and that list
// holds (Reach.Why): a call in the project's own files for [effects.self]'s
// lists, in a dependency's for [effects.deps]', and in module's for
// its entry under [effects.self.modules] or
// [effects.deps.modules].
func neededBy(r *vmhost.Reach, list, module, name string) []vmhost.WhyCall {
	calls, err := r.Why(name)
	if err != nil {
		return nil
	}
	var out []vmhost.WhyCall
	for _, c := range calls {
		self := c.Module == r.Project
		switch list {
		case analysis.ListSelfStd, analysis.ListSelfStdInTests:
			if !self {
				continue
			}
		case analysis.ListSelfModules:
			if !self || c.Import != module {
				continue
			}
		case analysis.ListDepsModules:
			if c.Module != module {
				continue
			}
		default:
			if self {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// nothingReaches says that no call list holds has the effect name
// (neededBy), naming the code list holds.
func nothingReaches(list, module, name string) string {
	switch list {
	case analysis.ListSelfStd, analysis.ListSelfStdInTests:
		return fmt.Sprintf("nothing in this module's own code has the effect %s", name)
	case analysis.ListSelfModules, analysis.ListDepsModules:
		return fmt.Sprintf("nothing in %s has the effect %s", module, name)
	}
	return fmt.Sprintf("no dependency has the effect %s", name)
}
