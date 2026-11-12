package vmhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/compilerhosts"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/stdio"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
	"github.com/nomi-language/nomi/std"
)

// Option configures how a program is loaded and run.
type Option func(*config)

type config struct {
	hosts        []HostTable
	virtualFiles map[string]string
	virtualData  map[string]string
	manifest     *analysis.Manifest
	projectRoot  string
	out          io.Writer
	errOut       io.Writer
	env          map[string]string
	input        io.Reader
	files        stdio.Backend
	stop         context.Context
	// lowering, when set, stops a lowering when it is done (CheckLowering):
	// the load answers its error.
	lowering context.Context
	// allowUnusedBindings drops unused-binding errors. See
	// WithUnusedBindingsAllowed.
	allowUnusedBindings bool
	// standalone marks an in-memory entry that is no file of the project
	// at projectRoot: compiler.run's nested program (frontend.Config's
	// StandaloneEntry).
	standalone bool
	// session is the REPL session a program is loaded for: it provides the
	// session's host functions and keeps earlier inputs' imports legal.
	session *Session
	// generate lowers the checked program, irbuild.GenerateIR when nil.
	generate func(*irbuild.Program) (*irbuild.Result, error)
	// cache, when set, keeps the program once it loads cleanly (WithCache).
	cache *Cache
	// warnings takes the program's warnings once a load has checked it
	// (WithWarnings, WithWarningsTo), or is nil to drop them.
	warnings func(Diagnostics)
}

func newConfig(opts []Option) config {
	c := config{out: os.Stdout}
	for _, o := range opts {
		o(&c)
	}
	return c
}

// WithHosts adds host tables: the Go functions a program's `host fn`
// declarations cross into. A table answers each function by the key the
// declaration registers under: its bare name in the entry file,
// `<module>.<name>` in a sibling file. A declaration no table answers is a
// load error. An FFI wrapper passes the table it generated for the project's
// `go` bindings; an embedder writes one (see hostadapt.Func). Nil tables are
// skipped.
func WithHosts(tables ...HostTable) Option {
	return func(c *config) {
		for _, t := range tables {
			if t != nil {
				c.hosts = append(c.hosts, t)
			}
		}
	}
}

// WithVirtualFiles makes in-memory sources importable by bare module name,
// ahead of disk. The tour playground stages a multi-file block this way (see
// SplitMultiFile).
func WithVirtualFiles(files map[string]string) Option {
	return func(c *config) { c.virtualFiles = files }
}

// WithVirtualData gives an in-memory program its other files, by their
// slash-separated paths from its root, which its `embed` declarations read
// instead of the disk. The tour passes a block's data files, the ones its
// in-memory file system starts with.
func WithVirtualData(files map[string]string) Option {
	return func(c *config) {
		if files == nil {
			files = map[string]string{}
		}
		c.virtualData = files
	}
}

// Manifest is a parsed nomi.toml, as SplitMultiFile returns it.
type Manifest = analysis.Manifest

// WithVirtualManifest stages a nomi.toml for an in-memory program, so the
// manifest's checks (entry_points, internal/ access, the orphan rule) apply.
func WithVirtualManifest(m *Manifest) Option {
	return func(c *config) { c.manifest = m }
}

// WithProjectRoot is the directory an in-memory program's project imports
// resolve against. A file program discovers its own.
func WithProjectRoot(root string) Option {
	return func(c *config) { c.projectRoot = root }
}

// WithOutput is where Call writes the program's output (io.print, dbg).
// Default os.Stdout. Run and the test entry points take their writer as an
// argument.
func WithOutput(w io.Writer) Option {
	return func(c *config) { c.out = w }
}

// WithErrorOutput is where the runtime's own diagnostics go — a supervised
// task's failure report. Default os.Stderr. A program's failure is the error
// Run answers, not written here.
func WithErrorOutput(w io.Writer) Option {
	return func(c *config) { c.errOut = w }
}

// WithInput is the program's standard input, which `io.read_line` reads a
// line at a time. Default none: `io.read_line` answers Err("eof"). `nomi run`
// passes os.Stdin.
func WithInput(r io.Reader) Option {
	return func(c *config) { c.input = r }
}

// WithStop makes every machine the program opens end its run when ctx
// does: a run blocked in a wait wakes at once, and one that is computing
// stops at its next call, loop iteration or iteration element. `nomi test
// --watch` stops a test file's cases this way when a file changes. Run also
// stops on its own ctx without it.
func WithStop(ctx context.Context) Option {
	return func(c *config) { c.stop = ctx }
}

// WithEnv overrides environment variables in the Startup a program's boot
// receives (`Startup.env`), on top of the process environment.
func WithEnv(env map[string]string) Option {
	return func(c *config) { c.env = env }
}

// WithWarnings writes what the program compiles with and reports to w,
// rendered as diagnostics, when Load or Check has checked it: each `embed`
// pattern whose base does not exist yet (Program.Warnings), and, from Check
// only, the program's advice (Program.Advice). `nomi run`, `nomi check` and
// `nomi test` pass the standard error. Without it they are written nowhere;
// a build refuses the warnings and ignores the advice.
func WithWarnings(w io.Writer) Option {
	return WithWarningsTo(func(ds Diagnostics) { ds.Render(w) })
}

// WithWarningsTo is WithWarnings for a host that reports them itself, as
// `nomi check --format json` does: f is called with them, when there are
// any.
func WithWarningsTo(f func(Diagnostics)) Option {
	return func(c *config) { c.warnings = f }
}

// frontendConfig is the front end's view of c: its virtual files and root,
// and which host keys c's tables answer.
func (c *config) frontendConfig() (frontend.Config, error) {
	keys, err := hostKeys(c.hosts)
	if err != nil {
		return frontend.Config{}, err
	}
	// A REPL session's `embed` reads files from its working directory.
	embedRoot := ""
	if c.session != nil {
		embedRoot, _ = os.Getwd()
	}
	return frontend.Config{
		VirtualFiles:    c.virtualFiles,
		VirtualData:     c.virtualData,
		EmbedRoot:       embedRoot,
		VirtualManifest: c.manifest,
		ProjectRoot:     c.projectRoot,
		Provided: func(key string) bool {
			return keys[key] || (c.session != nil && sessionKey(key))
		},
		HostTypesAreHandles: true,
		UnmetHint:           "answer them from a host table passed with vmhost.WithHosts.",
		AllowUnusedImports:  c.session != nil,
		AllowUnusedBindings: c.allowUnusedBindings,
		StandaloneEntry:     c.standalone,
	}, nil
}

// hostKeys binds each table against a plain Env to learn the keys it
// answers. Binding builds adapters and descriptors and calls nothing.
func hostKeys(tables []HostTable) (map[string]bool, error) {
	keys := map[string]bool{}
	for _, t := range tables {
		funcs, err := t(&hostadapt.Env{})
		if err != nil {
			return nil, fmt.Errorf("binding a host table: %w", err)
		}
		for k := range funcs {
			keys[k] = true
		}
	}
	return keys, nil
}

// lowering serializes lower, loadStdlib, a Session's CheckFile, whose
// session files a lowering reads, and a check of a stdlib module's source
// (checkStdlibSource). A lowering's decline hooks are its own
// (irbuild.Program.Declines), so the lowerings that do not take it, a
// relink and a relowering of an analyzed file, run beside these safely.
var lowering sync.Mutex

// checkStdlibSource checks src as the stdlib module named module
// (frontend.Checker.CheckStdlibSource) under the lowering lock. The check
// reads the shared stdlib's analyses, which a lowering on another goroutine
// writes when it checks a module's bodies (std.StdLib.Bodies), so an
// embedder that checks a std file while it loads a program needs the two
// serialized, as a lowering and a Session's CheckFile are.
func checkStdlibSource(fc frontend.Config, module, src string, mode frontend.Mode) ([]ast.Node, *analysis.FileAnalysis, error) {
	lowering.Lock()
	defer lowering.Unlock()
	return frontend.New(fc).CheckStdlibSource(module, src, mode)
}

// loweringStopped is the error of c's lowering context once it is done, or
// nil.
func (c *config) loweringStopped() error {
	if c.lowering == nil {
		return nil
	}
	return c.lowering.Err()
}

// Load runs the front end over the program at path and lowers it for the VM.
// A front-end error is returned as `nomi run` reports it.
func Load(path string, opts ...Option) (*Program, error) {
	cfg := newConfig(opts)
	fc, err := cfg.frontendConfig()
	if err != nil {
		return nil, err
	}
	p, err := checked(lower(cfg, func() (*irbuild.Program, error) { return irbuild.AnalyzeFile(path, fc) }))
	if err != nil {
		return nil, err
	}
	p.keep(false)
	return p, nil
}

// LoadFileSource is Load for the file at path whose text is src rather than
// what is on disk, as an editor holds it: the file's root, module name and
// sibling files are its own.
func LoadFileSource(path, src string, opts ...Option) (*Program, error) {
	cfg := newConfig(opts)
	fc, err := cfg.frontendConfig()
	if err != nil {
		return nil, err
	}
	p, err := lower(cfg, func() (*irbuild.Program, error) { return irbuild.AnalyzeFileSource(path, src, fc) })
	if err != nil {
		return nil, err
	}
	p.sources = map[string]string{p.prog.Entry().Path: src}
	return checked(p, nil)
}

// LoadSource is Load for an in-memory entry named name. Its sibling files, if
// any, come from WithVirtualFiles.
func LoadSource(name, src string, opts ...Option) (*Program, error) {
	cfg := newConfig(opts)
	fc, err := cfg.frontendConfig()
	if err != nil {
		return nil, err
	}
	p, err := lower(cfg, func() (*irbuild.Program, error) { return irbuild.AnalyzeVirtual(name, src, fc) })
	if err != nil {
		return nil, err
	}
	p.sources = map[string]string{p.prog.Entry().Path: src}
	// A virtual sibling's text, so a diagnostic in it quotes its line rather
	// than reading a same-named file from the working directory.
	for _, mod := range p.prog.Modules {
		if text, virtual := cfg.virtualFiles[mod.Name]; virtual {
			p.sources[mod.Path] = text
		}
	}
	return checked(p, nil)
}

// InternalError is a panic the compiler raised: while loading a program, or
// while the VM compiled one of its functions to bytecode on first call. It is
// a bug in Nomi, not in the program. Panic is the recovered value (for the
// VM's compiler, the function and the value) and Stack the goroutine's stack
// where it was recovered.
type InternalError struct {
	Panic any
	Stack []byte
}

func (e *InternalError) Error() string { return vm.InternalErrorText(e.Panic) }

// programFailure is vm.ProgramFailure with a panic in the VM's bytecode
// compiler answered as an InternalError, as a panic while loading is.
func programFailure(err error) (failure error, limit bool) {
	failure, limit = vm.ProgramFailure(err)
	return internalError(failure), limit
}

// internalError is err, or the InternalError for the compile panic it carries.
func internalError(err error) error {
	if cp, internal := vm.AsCompilePanic(err); internal {
		return &InternalError{Panic: cp.What(), Stack: cp.Stack}
	}
	return err
}

func lower(cfg config, analyze func() (*irbuild.Program, error)) (*Program, error) {
	// Deferred, so a producer panic does not leave every later load waiting
	// on the lock.
	lowering.Lock()
	defer lowering.Unlock()
	return lowerLocked(cfg, analyze)
}

// lowerLocked is lower for a caller that holds the lowering lock.
func lowerLocked(cfg config, analyze func() (*irbuild.Program, error)) (*Program, error) {
	declines := map[string]string{}
	var details []*irbuild.Decline
	var prog *irbuild.Program
	var res *irbuild.Result
	err := func() (err error) {
		// A panic in the front end or the IR builder (an `ir.Lint`
		// violation, say) is a compiler bug, never the program's fault. It
		// reaches the caller as an InternalError, so `nomi run` reports it
		// and a long-lived host such as the language server keeps running.
		defer func() {
			if r := recover(); r != nil {
				err = &InternalError{Panic: r, Stack: debug.Stack()}
			}
		}()
		if err := cfg.loweringStopped(); err != nil {
			return err
		}
		// The std bundle the lowering restores is read and decoded while
		// the front end builds the stdlib's analysis.
		irbuild.PrefetchStdBundle()
		prog, err = analyze()
		if err != nil {
			return err
		}
		prog.Declines = irbuild.DeclineHooks{
			Observed: func(fn, reason string) { declines[fn] = reason },
			At:       func(d *irbuild.Decline) { details = append(details, d) },
		}
		if err := cfg.loweringStopped(); err != nil {
			return err
		}
		if cfg.lowering != nil {
			prog.Stop = func() bool { return cfg.lowering.Err() != nil }
		}
		if cfg.generate != nil {
			res, err = cfg.generate(prog)
		} else {
			res, _, err = irbuild.GenerateIR(prog)
		}
		if errors.Is(err, irbuild.ErrStopped) {
			err = cfg.lowering.Err()
		}
		return err
	}()
	if err != nil {
		return nil, err
	}
	p := newProgram(prog, res, declines, cfg)
	p.declineDetails = details
	return p, nil
}

// checked is p once its backtick typed literals pass their compile-time
// check (checkLiterals), which every load makes once the program's sources
// are known, so a diagnostic quotes the text that was loaded.
func checked(p *Program, err error) (*Program, error) {
	if err != nil {
		return nil, err
	}
	if err := p.checkLiterals(); err != nil {
		return nil, err
	}
	p.writeWarnings()
	return p, nil
}

// Check runs the front end over the program at path, as `nomi check` does,
// and lowers and runs nothing. A stdlib source file is checked as its module.
//
// A file that declares tests is checked with them, as `nomi run` and
// `nomi test` analyze it (irbuild.AnalyzeFile) and as std.Load analyzes the
// stdlib. Its `//!` tests are part of the file, so an import only they use is
// used, and a type error in one is an error in the file.
func Check(path string, opts ...Option) error {
	cfg := newConfig(opts)
	fc, err := cfg.frontendConfig()
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	hasTests, _ := frontend.FileDeclaresTests(abs)
	mode := frontend.Mode{Tests: hasTests}
	if module, root, ok := frontend.StdlibFile(abs); ok {
		data, err := os.ReadFile(abs)
		if err != nil {
			return err
		}
		fc.ProjectRoot = root
		if _, _, err = checkStdlibSource(fc, module, string(data), mode); err != nil || !hasTests {
			return err
		}
		// The module's `//!` cases are lowered as `nomi test` lowers them.
		p, err := LoadStdlib(abs)
		if err != nil {
			return err
		}
		return p.Unsupported()
	}
	fc.SourceBoundProvided = true
	// The program is lowered as `nomi run` and `nomi test` lower it, and
	// nothing runs: a body the compiler accepts and cannot lower is reported
	// at its source, as the error `nomi run` would stop with.
	p, err := checked(lower(cfg, func() (*irbuild.Program, error) { return irbuild.AnalyzeFile(abs, fc) }))
	if err != nil {
		return err
	}
	if err := p.Unsupported(); err != nil {
		return err
	}
	if f := cfg.warnings; f != nil {
		if ds := p.Advice(); len(ds) > 0 {
			f(ds)
		}
	}
	p.keep(true)
	return nil
}

// LoadStdlib lowers the test cases of the stdlib module at path, `nomi test
// <std>/<module>.nomi`: its `//!` prompt cases, built in the module's own scope.
//
// The module is the file as it is on disk. When the file is byte-identical to
// the module embedded in this binary, the cases are built against the
// process's cached stdlib lowering (irbuild.GenerateStdlibTestIR), after the
// errors the shared analysis recorded, in this module or any other, are
// reported as `nomi check` reports them (frontend.StdlibErrors). When it is
// not, the file is checked, the module's own bodies are lowered from it, and
// the cases call those (irbuild.GenerateEditedStdlibTestIR), so an edited
// prompt and an edited body are both what the run tests. Every other stdlib
// module is still the embedded one, including where it calls into this
// module: an edit to two stdlib files, or to one another module calls, needs a
// rebuilt nomi to be tested together.
func LoadStdlib(path string) (*Program, error) {
	module := irbuild.StdlibTestModule(path)
	if module == "" {
		return nil, fmt.Errorf("%s is not a stdlib module", path)
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if embedded, ok := std.ReadFile(module); ok && string(embedded) == string(src) {
		if err := frontend.StdlibErrors(); err != nil {
			return nil, err
		}
		return loadStdlib(module, nil, nil, irbuild.GenerateStdlibTestIR)
	}
	nodes, fa, err := checkStdlibSource(frontend.Config{}, module, string(src), frontend.Mode{Tests: true})
	if err != nil {
		return nil, err
	}
	return loadStdlib(module, nodes, fa, irbuild.GenerateEditedStdlibTestIR)
}

// LoadStdlibSource is LoadStdlib for a stdlib module's source as edited: the
// tour's reference editors append one `test` to the module and run it. The
// source is checked as `nomi test` checks a stdlib file.
func LoadStdlibSource(module, src string) (*Program, error) {
	nodes, fa, err := checkStdlibSource(frontend.Config{}, module, src, frontend.Mode{Tests: true})
	if err != nil {
		return nil, err
	}
	return loadStdlib(strings.TrimSuffix(strings.TrimPrefix(module, "std/"), ".nomi"), nodes, fa, irbuild.GenerateStdlibTestIR)
}

// LoadEmbeddedStdlib is LoadStdlib for the embedded stdlib module named
// module (`net`, `std/net`), from the process's cached stdlib lowering:
// the module's tests, its `//!` prompts among them, as `nomi test std`
// runs them.
func LoadEmbeddedStdlib(module string) (*Program, error) {
	module = strings.TrimSuffix(strings.TrimPrefix(module, "std/"), ".nomi")
	if _, ok := std.ReadFile(module); !ok {
		return nil, fmt.Errorf("unknown stdlib module %q", module)
	}
	if err := frontend.StdlibErrors(); err != nil {
		return nil, err
	}
	return loadStdlib(module, nil, nil, irbuild.GenerateStdlibTestIR)
}

// StdlibReference runs one of the tour's stdlib reference editors: body, with
// context's import lines ahead of it, as a `test "reference"` appended to the
// stdlib module's own source, the only case selected. The case's own output
// goes to out. The tour's worker (cmd/nomi-wasm nomiRunStdlibTest) and
// cmd/nomi-docgen's reference tests both run an editor through here.
//
// The module's declarations are the embedded ones, which the process has
// checked and lowered already, so only the test is checked, in the module's
// scope of the shared analysis (frontend.Checker.CheckStdlibTest), and
// lowered against the module's lowering (irbuild.GenerateStdlibReferenceIR).
// An edit that is not one test alone is checked and lowered with the whole
// module, as `nomi test` checks an edited stdlib file.
func StdlibReference(module, body, context string, out io.Writer) ([]CaseResult, error) {
	module = strings.TrimSuffix(strings.TrimPrefix(module, "std/"), ".nomi")
	src, ok := std.ReadFile(module)
	if !ok {
		return nil, fmt.Errorf("unknown stdlib module %q", module)
	}
	wrapped, line := ReferenceTestSource(string(src), body, context)
	// The check adds the test's scope to the shared analysis of the module
	// until restore, so one editor runs at a time.
	referenceMu.Lock()
	defer referenceMu.Unlock()
	test, fa, restore, ok, err := frontend.New(frontend.Config{}).CheckStdlibTest(module, wrapped, line)
	if err != nil {
		return nil, err
	}
	var p *Program
	if ok {
		defer restore()
		p, err = loadStdlib(module, nil, nil, func(_ string, _ []ast.Node, _ *analysis.FileAnalysis, hooks irbuild.DeclineHooks) (*irbuild.Program, *irbuild.Result, error) {
			return irbuild.GenerateStdlibReferenceIR(module, test, fa, hooks)
		})
	} else {
		p, err = LoadStdlibSource(module, wrapped)
	}
	if err != nil {
		return nil, err
	}
	cases := p.Cases(out, TestOptions{Line: line, LineSet: true})
	for _, c := range cases {
		io.WriteString(out, c.Output)
	}
	return cases, nil
}

// referenceMu holds one StdlibReference at a time.
var referenceMu sync.Mutex

// ReferenceTestSource appends body, after context's lines, to src as
// `test "reference"`, and answers the result and the test's line.
func ReferenceTestSource(src, body, context string) (string, int) {
	prefix := ""
	if src != "" {
		prefix = strings.TrimRight(src, "\n") + "\n\n"
	}
	line := strings.Count(prefix, "\n") + 1
	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString("test \"reference\"\n")
	if context = strings.TrimSpace(context); context != "" {
		b.WriteString(indentLines(context, "  "))
		b.WriteByte('\n')
	}
	b.WriteString(indentLines(body, "  "))
	if body != "" && !strings.HasSuffix(body, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("end\n")
	return b.String(), line
}

func indentLines(s, prefix string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func loadStdlib(module string, nodes []ast.Node, fa *analysis.FileAnalysis,
	generate func(string, []ast.Node, *analysis.FileAnalysis, irbuild.DeclineHooks) (*irbuild.Program, *irbuild.Result, error)) (*Program, error) {
	lowering.Lock()
	declines := map[string]string{}
	var details []*irbuild.Decline
	hooks := irbuild.DeclineHooks{
		Observed: func(fn, reason string) { declines[fn] = reason },
		At:       func(d *irbuild.Decline) { details = append(details, d) },
	}
	prog, res, err := func() (*irbuild.Program, *irbuild.Result, error) {
		// Deferred, as lower's are, so a producer panic does not leave every
		// later load waiting on the lock.
		defer lowering.Unlock()
		return generate(module, nodes, fa, hooks)
	}()
	if err != nil {
		return nil, err
	}
	p := newProgram(prog, res, declines, newConfig(nil))
	p.declineDetails = details
	p.stdModule = module
	if err := p.checkLiterals(); err != nil {
		return nil, err
	}
	p.writeWarnings()
	return p, nil
}

// Warm lowers the standard library now rather than inside the first program's
// load. internal/irbuild analyzes and lowers every stdlib module once per
// process, and that is most of the first Load's time — seconds natively, far
// longer under wasm — so a long-lived host (the tour's worker) pays it at
// startup, before its first run is timed.
func Warm() {
	_, _ = LoadSource("warm", "fn main() end\n")
}

// newProgram opens a lowered program. Every host function a machine calls is a generated adapter, std/compiler's table
// or a table the host passed, and the analysis already recorded the project
// root and the virtual files std/compiler's table resolves imports against.
func newProgram(prog *irbuild.Program, res *irbuild.Result, declines map[string]string, cfg config) *Program {
	p := &Program{prog: prog, res: res, declines: declines, out: cfg.out, errOut: cfg.errOut, env: cfg.env, cfg: cfg}
	if cfg.input != nil {
		p.input = rt.NewInput(cfg.input)
	}
	p.hosts = append(p.hosts, compilerhosts.Table(prog.Root, prog.VirtualFiles))
	// A project's and an embedder's tables answer under their keys'
	// project names, so none of their keys answers a std crossing
	// (hostadapt.ProjectTable).
	for _, t := range cfg.hosts {
		p.hosts = append(p.hosts, hostadapt.ProjectTable(t))
	}
	m := programManifest(prog.Root, cfg.manifest)
	p.locales, p.webEmbedded = m.CarriedLocales(), m.WebLocales()
	if p.locales != nil {
		p.source = m.Source()
	}
	entryPath := prog.Entry().Path
	for _, m := range res.IR {
		if m.Name() == entryPath {
			p.entry = m
		}
	}
	return p
}

// programManifest is the program's manifest, whose `[i18n]` says the
// locales it carries: the staged one, else the one at root, nil when there
// is none (every locale). The front end already reported a manifest it
// cannot read, so one is read here as none.
func programManifest(root string, staged *analysis.Manifest) *analysis.Manifest {
	if staged != nil {
		return staged
	}
	if root == "" {
		return nil
	}
	if m, err := analysis.LoadManifest(root); err == nil {
		return m
	}
	return nil
}

// SplitMultiFile splits a tour-style source holding `// FILE: <name>` markers
// into its entry, the entry's module name, the other files (for
// WithVirtualFiles) and a staged nomi.toml (for WithVirtualManifest), if any.
func SplitMultiFile(src string) (entrySrc, entryName string, virtualFiles map[string]string, manifest *Manifest, err error) {
	return splitMultiFile(src)
}

// SourceContainsTests reports, by parsing alone, whether src declares any
// test.
func SourceContainsTests(src string) (bool, error) { return frontend.SourceContainsTests(src) }

// FileDeclaresTests reports, by parsing alone, whether the file at path
// declares any test case.
func FileDeclaresTests(path string) (bool, error) { return frontend.FileDeclaresTests(path) }
