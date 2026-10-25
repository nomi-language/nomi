package vmhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/buildversion"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

// MACROS RUN IN THE FRONT END, ON THE VM, IN THE TYPED-LITERAL SANDBOX.
//
// A declaration written with an imported macro's keyword is expanded
// while its file is loaded (analysis.ExpandDeclarations), before the
// checker builds types: the front end asks its MacroHost, and this is
// the one every load and the language server use. The host
//
//   - builds the macro's file as an entry with the hidden functions
//     that call its impls (analysis.MacroDrivers), once per version of
//     the module's files: the build is cached by the file, and kept while
//     every file it read still reads the same;
//   - refuses a macro whose `request` or `expand` can reach an effect,
//     with vm.Machine.Effects, the analysis that refuses a typed literal's
//     handler: output, files, the network, the clock, randomness, tasks,
//     channels or the running app's fields;
//   - runs `request`, reads the files it names under the declaring module's
//     root and no further, gathers the declarations of the keywords it
//     names, and runs `expand`, each under macroLimits: steps and a
//     bound on the memory the run allocates, which decide whether it
//     succeeds, and a hang guard far past what the steps allow;
//   - caches each expansion by everything it read: the compiler's version,
//     the macro's build (the text of every file it was built from), the
//     declaration, the configuration, the requested files' paths and texts,
//     and the declaration sites. The same key is the same expansion, since
//     the run is a pure function of what it is given.
//
// Every file the run read, the macro's own source files among them, is
// an input of the declaring file (MacroResult.Inputs), so the language
// server expands the declaration again when one changes.

var (
	// macroSteps bounds one run of `request` or `expand`: calls,
	// backward branches and iteration elements, as literalSteps does a
	// typed literal's handler. The fixture's table takes 37 steps
	// per CSV row (TestMacroExpansionCost: 370,066 steps and about
	// 0.1 s for 10,000 rows), so this is room for a table of some 540,000
	// rows, or for a parser that takes 20 steps per byte of a 1 MB file:
	// twenty times a typed literal's, which reads one literal.
	macroSteps int64 = 20_000_000
	// macroMemory bounds the memory one run allocates.
	macroMemory int64 = 1 << 30
	// macroHangGuard stops a run still going after this long. The steps
	// and the memory decide whether a run succeeds, so that an expansion
	// that checks on one machine checks on every other and a cached one is
	// what any machine would answer. The whole budget of steps takes about
	// a second on a laptop for a loop that does nothing else, and a few for
	// table's work per row (TestMacroExpansionCost), so the guard is
	// twenty times that and more: it fires only for a run whose steps are
	// slow beyond anything the budget foresees (one crossing into Go over a
	// huge value), and a run it stops is an internal failure, never cached
	// (TestMacro_HangGuard).
	macroHangGuard = 2 * time.Minute
)

// macroLimits are the limits one run is held to.
func macroLimits() vm.Limits {
	return vm.Limits{Steps: macroSteps, Deadline: time.Now().Add(macroHangGuard), Memory: macroMemory}
}

// macroHost is the analysis.MacroHost of a load (config.macros)
// or of the language server (Macros). held is set while the load it
// belongs to holds the lowering lock, around its front end: a macro
// built then is lowered under that hold.
type macroHost struct {
	held atomic.Bool
}

// Macros is the host a front end outside a load expands macros with:
// the language server's analyses.
func Macros() analysis.MacroHost { return &macroHost{} }

// Expansion is what one declaration of a file expanded to, as `nomi expand`
// shows it.
type Expansion struct {
	// Keyword is the declaration's keyword as written (`csvdata.table`)
	// and Name its name, Line its line; Macro
	// and Module name the macro that expanded it.
	Keyword, Name string
	Line          int
	Macro, Module string
	// Source is the expansion's Nomi source, and Errors what is wrong
	// with the declaration or its inputs, as the checker reports it.
	Source string
	Errors []string
}

// Expansions expands the macro declarations of the file at path as a
// check of its program would, and answers each, in the file's order.
func Expansions(path string) ([]Expansion, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	nodes, err := parser.Parse(lexer.Lex(string(data)))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	root := analysis.ProjectRoot(abs, "")
	m, _ := analysis.LoadManifest(root)
	site := analysis.SiteFor(root, abs, m, nil)
	site.Macros = &macroHost{}
	var out []Expansion
	for _, n := range analysis.ExpandDeclarations(nodes, site) {
		kd, ok := n.(*ast.KeywordDecl)
		if !ok {
			continue
		}
		name, module, ok := analysis.MacroOf(kd)
		if !ok {
			continue
		}
		x := Expansion{Keyword: kd.QualifiedKeyword(), Name: kd.Name, Line: kd.Line, Macro: name, Module: module}
		x.Source, _ = analysis.MacroSource(kd)
		for _, e := range analysis.MacroErrors(kd) {
			at := path
			if e.File != "" {
				at = e.File
			}
			x.Errors = append(x.Errors, fmt.Sprintf("%s:%d:%d: %s", at, e.Line, e.Col, e.Message))
		}
		out = append(out, x)
	}
	return out, nil
}

// nestedLowering serializes the macro builds of loads that hold the
// lowering lock.
var nestedLowering sync.Mutex

// macroBuild is one build of a macro's file.
type macroBuild struct {
	p *Program
	// files are the project files the build read, with their texts.
	files map[string]string
	// key is a hash of them, part of every expansion's cache key.
	key string
	// failure says why the file does not build as a macro.
	failure string
	hints   []string
}

var macroCache = struct {
	sync.Mutex
	builds   map[string]*macroBuild
	building map[string]bool
	// expansions are cached results by key, at most maxExpansions of
	// them; order is their keys oldest first.
	expansions map[string]*analysis.MacroResult
	order      []string
}{builds: map[string]*macroBuild{}, building: map[string]bool{}, expansions: map[string]*analysis.MacroResult{}}

const maxExpansions = 4096

// MacroCacheForTest empties the caches of macro builds and
// expansions, and answers how many expansions it held.
func MacroCacheForTest() int {
	macroCache.Lock()
	defer macroCache.Unlock()
	n := len(macroCache.expansions)
	macroCache.builds = map[string]*macroBuild{}
	macroCache.expansions = map[string]*analysis.MacroResult{}
	macroCache.order = nil
	return n
}

// expansionRuns counts the expansions a host ran rather than found in its
// cache, for tests.
var expansionRuns atomic.Int64

// ExpansionRunsForTest is how many expansions ran in this process.
func ExpansionRunsForTest() int64 { return expansionRuns.Load() }

// Expand implements analysis.MacroHost.
func (h *macroHost) Expand(call *analysis.MacroCall) *analysis.MacroResult {
	run := &macroRun{call: call, host: h, seen: map[string]bool{}}
	res := run.expand()
	res.Inputs = run.inputs
	return res
}

// macroRun is one Expand: what it read.
type macroRun struct {
	call   *analysis.MacroCall
	host   *macroHost
	inputs []analysis.ExpansionInput
	seen   map[string]bool
	// steps are the steps the last run took.
	steps int64
}

// lastExpansionSteps are the steps the last `expand` that ran took, for
// TestMacroExpansionCost.
var lastExpansionSteps atomic.Int64

// read reads path through the call's reader, recording it as an input.
func (r *macroRun) read(path string) (string, bool) {
	text, ok := r.call.Read(path)
	if !r.seen[path] {
		r.seen[path] = true
		r.inputs = append(r.inputs, analysis.ExpansionInput{Path: path, Text: text, Found: ok})
	}
	return text, ok
}

func failed(msg string, hints ...string) *analysis.MacroResult {
	return &analysis.MacroResult{Failure: msg, FailureHints: hints}
}

// CheckAll implements analysis.MacroHost.
func (h *macroHost) CheckAll(call *analysis.MacroCheckCall) ([]analysis.MacroDiagnostic, string) {
	b, m, failure := h.built(call.File)
	if failure != "" {
		// The expansion reported why the macro does not run.
		return nil, ""
	}
	f := b.p.driver(analysis.MacroDriverName(call.Name, "check_all"))
	if f == nil {
		return nil, ""
	}
	if effects := m.Effects(f); len(effects) > 0 {
		return nil, fmt.Sprintf("its `check_all` has effects (%s)", strings.Join(effects, "; "))
	}
	h2 := sha256.New()
	fmt.Fprintf(h2, "check_all\x00%s\x00%s\x00%s\x00%s\x00%s", b.key, call.Name, keyText(call.Decls), call.Config, keyText(call.Inputs))
	key := hex.EncodeToString(h2.Sum(nil))
	if res := cachedExpansion(key); res != nil {
		return res.Diagnostics, ""
	}
	declFn := b.p.driver(analysis.MacroDriverName(call.Name, "declaration"))
	r := &macroRun{host: h, seen: map[string]bool{}}
	var decls []any
	for _, d := range call.Decls {
		v, err := r.evaluate(m, declFn, declarationArgs(d)...)
		if err != nil {
			return nil, r.runFailure("building a declaration", err)
		}
		decls = append(decls, v)
	}
	v, err := r.evaluate(m, f, nomiList(decls), call.Config, inputFields(call.Inputs))
	if err != nil {
		return nil, r.runFailure("`check_all`", err)
	}
	var diags []analysis.MacroDiagnostic
	for _, d := range listItems(v) {
		diags = append(diags, decodeDiagnostic(d))
	}
	cacheExpansion(key, &analysis.MacroResult{Diagnostics: diags})
	return diags, ""
}

// Navigate implements analysis.MacroHost.
func (h *macroHost) Navigate(nav *analysis.MacroNavigation) *analysis.MacroAnswer {
	b, m, failure := h.built(nav.File)
	if failure != "" {
		return nil
	}
	f := b.p.driver(analysis.MacroDriverName(nav.Name, nav.Op))
	declFn := b.p.driver(analysis.MacroDriverName(nav.Name, "declaration"))
	if f == nil || declFn == nil || len(m.Effects(f)) > 0 {
		return nil
	}
	r := &macroRun{host: h, seen: map[string]bool{}}
	decl, err := r.evaluate(m, declFn, declarationArgs(nav.Decl)...)
	if err != nil {
		return nil
	}
	var v any
	if nav.Op == "actions" {
		d := nav.Diagnostic
		v, err = r.evaluate(m, f, decl, nav.Config, inputFields(nav.Inputs), Fields{"span": spanFields(d.Span), "message": d.Message, "hints": d.Hints})
	} else {
		v, err = r.evaluate(m, f, decl, nav.Config, inputFields(nav.Inputs), nav.Line, nav.Col)
	}
	if err != nil {
		return nil
	}
	a := &analysis.MacroAnswer{}
	switch nav.Op {
	case "hover":
		if s, ok := maybe(v); ok {
			a.Hover, _ = s.(string)
		}
	case "definition":
		if s, ok := maybe(v); ok {
			span := decodeSpan(s)
			a.Definition = &span
		}
	case "complete":
		for _, c := range listItems(v) {
			a.Completions = append(a.Completions, analysis.MacroCompletion{Label: stringField(c, "label"), Detail: stringField(c, "detail"), Insert: stringField(c, "insert")})
		}
	case "actions":
		for _, act := range listItems(v) {
			action := analysis.MacroAction{Title: stringField(act, "title")}
			for _, e := range listItems(field(act, "edits")) {
				action.Edits = append(action.Edits, analysis.MacroEdit{Span: decodeSpan(field(e, "span")), Text: stringField(e, "text")})
			}
			if tool, ok := maybe(field(act, "tool")); ok {
				action.Tool = &analysis.MacroTool{Name: stringField(tool, "name"), Args: stringList(field(tool, "args"))}
			}
			a.Actions = append(a.Actions, action)
		}
	}
	return a
}

// built is the cached build of the macro file at file, with a machine
// over it, or why there is none. A build is made by an expansion first;
// ProgramCheck and Navigation run on it.
func (h *macroHost) built(file string) (*macroBuild, *vm.Machine, string) {
	macroCache.Lock()
	b := macroCache.builds[file]
	macroCache.Unlock()
	if b == nil {
		return nil, nil, "it was not built"
	}
	if b.failure != "" {
		return nil, nil, b.failure
	}
	return b, b.p.machine(io.Discard), ""
}

func cachedExpansion(key string) *analysis.MacroResult {
	macroCache.Lock()
	defer macroCache.Unlock()
	return macroCache.expansions[key]
}

func cacheExpansion(key string, res *analysis.MacroResult) {
	macroCache.Lock()
	defer macroCache.Unlock()
	if _, ok := macroCache.expansions[key]; ok {
		return
	}
	macroCache.expansions[key] = res
	macroCache.order = append(macroCache.order, key)
	if len(macroCache.order) > maxExpansions {
		delete(macroCache.expansions, macroCache.order[0])
		macroCache.order = macroCache.order[1:]
	}
}

// nomiList is items, values the VM holds, as a List.
func nomiList(items []any) *rt.List[any] {
	var l *rt.List[any]
	for i := len(items) - 1; i >= 0; i-- {
		l = rt.Cons[any](items[i], l)
	}
	return l
}

func (r *macroRun) expand() *analysis.MacroResult {
	b := r.build()
	if b.failure != "" {
		// The module's own errors are the program's too, and say why.
		res := failed(b.failure, b.hints...)
		res.Unbuilt = true
		return res
	}
	res := r.run(b)
	// The module built, so a failure from here on is the compiler's
	// refusal or stop of the macro's own code, which says all there is.
	res.Refused = res.Failure != ""
	return res
}

// run runs the built macro's request and expand for the call.
func (r *macroRun) run(b *macroBuild) *analysis.MacroResult {
	name := r.call.Name
	fns := map[string]*ir.Func{}
	for _, part := range []string{"declaration", "request", "expand"} {
		f := b.p.driver(analysis.MacroDriverName(name, part))
		if f == nil {
			if b.p.declares(analysis.MacroDriverName(name, part)) {
				return failed(fmt.Sprintf("the compiler cannot lower its `%s`: %s", part, b.p.decline(analysis.MacroDriverName(name, part))))
			}
			return failed(fmt.Sprintf("%s declares no macro `%s`", r.relative(r.call.File), name))
		}
		fns[part] = f
	}
	m := b.p.machine(io.Discard)
	for _, part := range []string{"request", "expand"} {
		if found := m.Unretained([]*ir.Func{fns[part]}, nil); len(found) > 0 {
			return failed(fmt.Sprintf("its `%s` reaches code the compiler cannot lower: %s", part, strings.Join(b.p.reasons(found), "; ")))
		}
		if effects := m.Effects(fns[part]); len(effects) > 0 {
			return failed(fmt.Sprintf("its `%s` has effects, which a macro may not have while a program is checked (%s)", part, strings.Join(effects, "; ")),
				"a macro is a pure function of the declaration and the files it requests; work that needs files, the network or a database belongs in a tool, which `nomi tool` runs")
		}
	}
	decl, err := r.evaluate(m, fns["declaration"], declarationArgs(r.call.Decl)...)
	if err != nil {
		return failed(r.runFailure("building the declaration", err))
	}
	reqV, err := r.evaluate(m, fns["request"], decl, r.call.Config)
	if err != nil {
		return failed(r.runFailure("`request`", err))
	}
	files, keywords := stringList(field(reqV, "files")), stringList(field(reqV, "sites"))
	inputs, diags := r.readRequested(files)
	sites := r.call.Sites(keywords, r.read)
	key := r.key(b, files, inputs, keywords, sites)
	res := cachedExpansion(key)
	if res == nil {
		expansionRuns.Add(1)
		expV, err := r.evaluate(m, fns["expand"], decl, r.call.Config, inputFields(inputs), siteFields(sites))
		lastExpansionSteps.Store(r.steps)
		if err != nil {
			return failed(r.runFailure("`expand`", err))
		}
		res = decodeExpansion(expV)
		res.Diagnostics = append(diags, res.Diagnostics...)
		res.Requested = inputs
		cacheExpansion(key, res)
	}
	out := *res
	return &out
}

// driver is the entry's function name, a macro driver, or nil.
func (p *Program) driver(name string) *ir.Func {
	if p.entry == nil {
		return nil
	}
	for _, f := range p.entry.Funcs() {
		if f.Name() == name {
			return f
		}
	}
	return nil
}

// relative is path from the declaring module's root, or as it is.
func (r *macroRun) relative(p string) string {
	if rel, err := filepath.Rel(r.call.Root, p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return p
}

// runFailure says why a run of what failed.
func (r *macroRun) runFailure(what string, err error) string {
	switch {
	case errors.Is(err, vm.ErrMemoryLimit):
		return fmt.Sprintf("%s allocated more than its memory budget of %d MiB", what, macroMemory>>20)
	case errors.Is(err, vm.ErrDeadline):
		return fmt.Sprintf("%s was still running after %s, so the compiler stopped it. This is an internal failure, not the macro's answer: "+
			"its budget of %s steps stops a run that does too much work, so one of its steps took far longer than any should. "+
			"Nothing was cached, and the next check runs the macro again", what, macroHangGuard, groupDigits(macroSteps))
	case errors.Is(err, vm.ErrLimit):
		return fmt.Sprintf("%s did not finish within its budget of %s steps (calls, backward branches and iteration elements)", what, groupDigits(macroSteps))
	}
	failure, _ := programFailure(err)
	return fmt.Sprintf("%s failed: %s", what, failure.Error())
}

// evaluate runs f with args, each converted to the value its parameter's
// type holds, under the macro limits.
func (r *macroRun) evaluate(m *vm.Machine, f *ir.Func, args ...any) (any, error) {
	params := f.Params()
	converted := make([]any, len(args))
	for i, prm := range params {
		v, err := toNomi(f.TempType(prm.Temp), args[i])
		if err != nil {
			return nil, err
		}
		converted[i] = v
	}
	lim := macroLimits()
	lim.Spent = &r.steps
	return m.Evaluate(context.Background(), f, converted, lim)
}

// build is the build of the call's macro file: the cached one while
// every file it read reads the same, else a new one.
func (r *macroRun) build() *macroBuild {
	file := r.call.File
	macroCache.Lock()
	b := macroCache.builds[file]
	if macroCache.building[file] {
		macroCache.Unlock()
		return &macroBuild{failure: "it is being built already: its module uses a macro that uses it, a cycle"}
	}
	macroCache.Unlock()
	if b != nil && r.current(b) {
		return b
	}
	macroCache.Lock()
	macroCache.building[file] = true
	macroCache.Unlock()
	defer func() {
		macroCache.Lock()
		delete(macroCache.building, file)
		macroCache.Unlock()
	}()
	b = r.compile()
	if b.failure == "" {
		// A build that failed is not kept: it read nothing to tell when
		// it would build.
		macroCache.Lock()
		macroCache.builds[file] = b
		macroCache.Unlock()
	}
	return b
}

// current reports whether every file b read still reads the same, reading
// each as an input of the call's declaration.
func (r *macroRun) current(b *macroBuild) bool {
	paths := make([]string, 0, len(b.files))
	for p := range b.files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	same := true
	for _, p := range paths {
		if text, ok := r.read(p); !ok || text != b.files[p] {
			same = false
		}
	}
	return same
}

// compile builds the macro's file as an entry, with its drivers.
func (r *macroRun) compile() *macroBuild {
	nested := &macroHost{}
	cfg := newConfig([]Option{WithOutput(io.Discard)})
	cfg.macros = nested
	fc, err := cfg.frontendConfig()
	if err != nil {
		return &macroBuild{failure: err.Error()}
	}
	fc.MacroDrivers = true
	analyze := func() (*irbuild.Program, error) { return irbuild.AnalyzeFile(r.call.File, fc) }
	var p *Program
	if r.host.held.Load() {
		nested.held.Store(true)
		p, err = lowerNested(cfg, analyze)
	} else {
		p, err = lower(cfg, analyze)
	}
	if err == nil {
		err = p.checkLiterals()
	}
	if err != nil {
		return &macroBuild{
			failure: "its module does not check: " + firstLine(err.Error()),
		}
	}
	b := &macroBuild{p: p, files: map[string]string{}}
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00", buildversion.Current())
	var paths []string
	for _, mod := range p.prog.Modules {
		if strings.HasPrefix(mod.Name, "std/") || !filepath.IsAbs(mod.Path) {
			continue
		}
		paths = append(paths, mod.Path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		text, _ := r.read(path)
		b.files[path] = text
		fmt.Fprintf(h, "%s\x00%s\x00", path, text)
	}
	b.key = hex.EncodeToString(h.Sum(nil))
	return b
}

// groupDigits is n written with a comma between each group of three
// digits: 20,000,000.
func groupDigits(n int64) string {
	s := fmt.Sprint(n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// firstLine is the first line of s, saying how many more follow.
func firstLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	more := 0
	for _, l := range lines[1:] {
		if !strings.HasPrefix(strings.TrimSpace(l), "|") && strings.Contains(l, ": ") && !strings.Contains(l, ": help: ") && !strings.Contains(l, ": note: ") {
			more++
		}
	}
	if more > 0 {
		return fmt.Sprintf("%s (and %d more)", lines[0], more)
	}
	return lines[0]
}

// lowerNested is lower for a build inside a load that holds the lowering
// lock: the load is in its front end, so the decline census is idle, and it
// is set aside for this build and given back.
func lowerNested(cfg config, analyze func() (*irbuild.Program, error)) (*Program, error) {
	nestedLowering.Lock()
	defer nestedLowering.Unlock()
	observed, at := irbuild.IRDeclineObserved, irbuild.IRDeclineAt
	defer func() { irbuild.IRDeclineObserved, irbuild.IRDeclineAt = observed, at }()
	return lowerLocked(cfg, analyze)
}

// readRequested reads the files a request names: each path, or each file
// a glob matches, under the declaring module's root. A path that leaves the
// root is refused with a diagnostic at the declaration.
func (r *macroRun) readRequested(patterns []string) ([]analysis.ExpansionInput, []analysis.MacroDiagnostic) {
	root := r.call.DeclRoot
	var out []analysis.ExpansionInput
	var diags []analysis.MacroDiagnostic
	seen := map[string]bool{}
	add := func(rel string) {
		if seen[rel] {
			return
		}
		seen[rel] = true
		if text, ok := r.read(filepath.Join(root, filepath.FromSlash(rel))); ok {
			out = append(out, analysis.ExpansionInput{Path: rel, Text: text, Found: true})
		}
	}
	for _, pat := range patterns {
		clean := path.Clean(filepath.ToSlash(pat))
		if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") || !within(root, clean) {
			diags = append(diags, analysis.MacroDiagnostic{Span: r.call.Decl.Span,
				Message: fmt.Sprintf("the macro `%s` asks to read %q, which is not under the module root; a macro reads only its declaring module's files", r.call.Name, pat)})
			continue
		}
		if !strings.ContainsAny(clean, "*?[") {
			add(clean)
			continue
		}
		var matches []string
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			if ok, _ := path.Match(clean, rel); ok {
				matches = append(matches, rel)
			}
			return nil
		})
		sort.Strings(matches)
		for _, m := range matches {
			add(m)
		}
	}
	return out, diags
}

// within reports whether rel, a clean relative path, stays under root
// once symbolic links are resolved.
func within(root, rel string) bool {
	full := filepath.Join(root, filepath.FromSlash(rel))
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		// A file that does not exist reads as missing; its directory decides.
		dir, derr := filepath.EvalSymlinks(filepath.Dir(full))
		if derr != nil {
			return true
		}
		real = filepath.Join(dir, filepath.Base(full))
	}
	back, err := filepath.Rel(realRoot, real)
	return err == nil && back != ".." && !strings.HasPrefix(back, "../")
}

// key is the cache key of an expansion: everything it reads.
func (r *macroRun) key(b *macroBuild, files []string, inputs []analysis.ExpansionInput, keywords []string, sites []analysis.MacroSite) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s\x00", b.key, r.call.File, r.call.Name, keyText(r.call.Decl), r.call.Config)
	fmt.Fprintf(h, "%q\x00%q\x00", files, keywords)
	for _, in := range inputs {
		fmt.Fprintf(h, "%s\x00%s\x00", in.Path, in.Text)
	}
	fmt.Fprintf(h, "%s", keyText(sites))
	return hex.EncodeToString(h.Sum(nil))
}

// keyText is v as cache key text: its JSON, which follows pointers, where
// fmt's %#v would print their addresses.
func keyText(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic("vmhost: a macro cache key does not encode: " + err.Error())
	}
	return string(data)
}

// declarationArgs are the driver's `declaration` arguments for d.
func declarationArgs(d analysis.MacroDeclaration) []any {
	params := make([]Fields, 0, len(d.Params))
	for _, p := range d.Params {
		params = append(params, Fields{"name": p.Name, "type_text": typeTextFields(p.Type), "span": spanFields(p.Span)})
	}
	hasResult, result := d.Result != nil, typeTextFields(analysis.MacroTypeText{})
	if hasResult {
		result = typeTextFields(*d.Result)
	}
	hasBody := d.Body != nil
	body := analysis.MacroBody{Span: d.Span}
	if hasBody {
		body = *d.Body
	}
	return []any{d.Keyword, d.Name, d.Public, params, hasResult, result,
		hasBody, body.Text, spanFields(body.Span), body.StartLine, body.StartCol, body.IndentCol, body.Tag != "", body.Tag, spanFields(body.TagSpan),
		d.Doc, d.File, spanFields(d.Span), spanFields(d.NameSpan)}
}

func spanFields(s analysis.MacroSpan) Fields {
	return Fields{"file": s.File, "line": s.Line, "col": s.Col, "end_line": s.EndLine, "end_col": s.EndCol}
}

func typeTextFields(t analysis.MacroTypeText) Fields {
	return Fields{"text": t.Text, "span": spanFields(t.Span)}
}

// inputFields are inputs as a List<Input> argument.
func inputFields(inputs []analysis.ExpansionInput) []Fields {
	out := make([]Fields, 0, len(inputs))
	for _, in := range inputs {
		out = append(out, Fields{"path": in.Path, "text": in.Text})
	}
	return out
}

// siteFields are sites as a List<Site> argument.
func siteFields(sites []analysis.MacroSite) []Fields {
	out := make([]Fields, 0, len(sites))
	for _, s := range sites {
		out = append(out, Fields{"keyword": s.Keyword, "name": s.Name, "file": s.File, "span": spanFields(s.Span)})
	}
	return out
}

// field is the field name of a record, or nil.
func field(v any, name string) any {
	rec, ok := v.(*rt.Record)
	if !ok || rec == nil {
		return nil
	}
	f, _ := rec.FieldNamed(name)
	return f
}

// listItems are the items of a List value.
func listItems(v any) []any {
	l, ok := v.(*rt.List[any])
	if !ok {
		return nil
	}
	var out []any
	for ; l != nil; l = l.Tail {
		out = append(out, l.Head)
	}
	return out
}

func stringList(v any) []string {
	var out []string
	for _, item := range listItems(v) {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func intField(v any, name string) int {
	n, _ := field(v, name).(int64)
	return int(n)
}

func stringField(v any, name string) string {
	s, _ := field(v, name).(string)
	return s
}

// maybe is the payload of a Maybe value, and whether it is Some.
func maybe(v any) (any, bool) {
	rec, ok := v.(*rt.Record)
	if !ok || rec == nil || rec.Variant().Name != "Some" || rec.NumFields() != 1 {
		return nil, false
	}
	return rec.Field(0), true
}

func decodeSpan(v any) analysis.MacroSpan {
	return analysis.MacroSpan{
		File: stringField(v, "file"), Line: intField(v, "line"), Col: intField(v, "col"),
		EndLine: intField(v, "end_line"), EndCol: intField(v, "end_col"),
	}
}

func decodeDiagnostic(v any) analysis.MacroDiagnostic {
	return analysis.MacroDiagnostic{Span: decodeSpan(field(v, "span")), Message: stringField(v, "message"), Hints: stringList(field(v, "hints"))}
}

// decodeExpansion is the Expansion value v.
func decodeExpansion(v any) *analysis.MacroResult {
	res := &analysis.MacroResult{Source: stringField(v, "source")}
	for _, m := range listItems(field(v, "spans")) {
		sm := analysis.MacroSpanMap{Start: intField(m, "start"), End: intField(m, "end"), Span: decodeSpan(field(m, "span"))}
		if hint, ok := maybe(field(m, "hint")); ok {
			sm.Hint, _ = hint.(string)
		}
		res.Spans = append(res.Spans, sm)
	}
	for _, d := range listItems(field(v, "diagnostics")) {
		res.Diagnostics = append(res.Diagnostics, decodeDiagnostic(d))
	}
	return res
}
