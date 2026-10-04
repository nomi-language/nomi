package vmhost

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/compilerhosts"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/rt"
	"github.com/nomi-language/nomi/std"
)

// Option configures how a program is loaded and run.
type Option func(*config)

type config struct {
	hosts        []HostTable
	virtualFiles map[string]string
	manifest     *analysis.Manifest
	projectRoot  string
	out          io.Writer
	errOut       io.Writer
	env          map[string]string
	input        io.Reader
	// allowUnusedBindings drops unused-binding errors. See
	// WithUnusedBindingsAllowed.
	allowUnusedBindings bool
	// session is the REPL session a program is loaded for: it provides the
	// session's host functions and keeps earlier inputs' imports legal.
	session *Session
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

// WithEnv overrides environment variables in the Startup a program's boot
// receives (`Startup.env`), on top of the process environment.
func WithEnv(env map[string]string) Option {
	return func(c *config) { c.env = env }
}

// frontendConfig is the front end's view of c: its virtual files and root,
// and which host keys c's tables answer.
func (c *config) frontendConfig() (frontend.Config, error) {
	keys, err := hostKeys(c.hosts)
	if err != nil {
		return frontend.Config{}, err
	}
	return frontend.Config{
		VirtualFiles:    c.virtualFiles,
		VirtualManifest: c.manifest,
		ProjectRoot:     c.projectRoot,
		Provided: func(key string) bool {
			return keys[key] || (c.session != nil && sessionKey(key))
		},
		HostTypesAreHandles: true,
		UnmetHint:           "answer them from a host table passed with vmhost.WithHosts.",
		AllowUnusedImports:  c.session != nil,
		AllowUnusedBindings: c.allowUnusedBindings,
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

// lowering serializes the producer's decline census, which is package state in
// internal/irbuild.
var lowering sync.Mutex

// Load runs the front end over the program at path and lowers it for the VM.
// A front-end error is returned as `nomi run` reports it.
func Load(path string, opts ...Option) (*Program, error) {
	cfg := newConfig(opts)
	fc, err := cfg.frontendConfig()
	if err != nil {
		return nil, err
	}
	return lower(cfg, func() (*irbuild.Program, error) { return irbuild.AnalyzeFile(path, fc) })
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
	return lower(cfg, func() (*irbuild.Program, error) { return irbuild.AnalyzeFileSource(path, src, fc) })
}

// LoadSource is Load for an in-memory entry named name. Its sibling files, if
// any, come from WithVirtualFiles.
func LoadSource(name, src string, opts ...Option) (*Program, error) {
	cfg := newConfig(opts)
	fc, err := cfg.frontendConfig()
	if err != nil {
		return nil, err
	}
	return lower(cfg, func() (*irbuild.Program, error) { return irbuild.AnalyzeVirtual(name, src, fc) })
}

func lower(cfg config, analyze func() (*irbuild.Program, error)) (*Program, error) {
	declines := map[string]string{}
	var details []*irbuild.Decline
	var prog *irbuild.Program
	var res *irbuild.Result
	err := func() error {
		// Deferred, so a producer panic does not leave every later load
		// waiting on the lock.
		lowering.Lock()
		defer lowering.Unlock()
		irbuild.IRDeclineObserved = func(fn, reason string) { declines[fn] = reason }
		irbuild.IRDeclineAt = func(d *irbuild.Decline) { details = append(details, d) }
		defer func() { irbuild.IRDeclineObserved, irbuild.IRDeclineAt = nil, nil }()
		var err error
		prog, err = analyze()
		if err == nil {
			res, _, err = irbuild.GenerateIR(prog)
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
		_, _, err = frontend.New(fc).CheckStdlibSource(module, string(data), mode)
		return err
	}
	fc.SourceBoundProvided = true
	// The program is lowered as `nomi run` and `nomi test` lower it, and
	// nothing runs: a body the compiler accepts and cannot lower is reported
	// at its source, as an error, rather than at run time as BLOCKED.
	p, err := lower(cfg, func() (*irbuild.Program, error) { return irbuild.AnalyzeFile(abs, fc) })
	if err != nil {
		return err
	}
	return p.Unsupported()
}

// LoadStdlib lowers the test cases of the stdlib module at path, `nomi test
// <std>/<module>.nomi`: its `//!` prompt cases, built in the module's own scope.
//
// The module is the file as it is on disk. When the file is byte-identical to
// the module embedded in this binary, the cases are built against the
// process's cached stdlib lowering (irbuild.GenerateStdlibTestIR), after the
// errors the shared analysis recorded for it are reported as a fresh check
// would report them (frontend.StdlibModuleErrors). When it is
// not, the file is checked, the module's own bodies are lowered from it, and
// the cases call those (irbuild.GenerateEditedStdlibTestIR), so an edited
// prompt and an edited body are both what the run tests. Every OTHER stdlib
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
		if err := frontend.StdlibModuleErrors(module); err != nil {
			return nil, err
		}
		return loadStdlib(module, nil, nil, irbuild.GenerateStdlibTestIR)
	}
	nodes, fa, err := frontend.New(frontend.Config{}).CheckStdlibSource(module, string(src), frontend.Mode{Tests: true})
	if err != nil {
		return nil, err
	}
	return loadStdlib(module, nodes, fa, irbuild.GenerateEditedStdlibTestIR)
}

// LoadStdlibSource is LoadStdlib for a stdlib module's source as edited: the
// tour's reference editors append one `test` to the module and run it. The
// source is checked as `nomi test` checks a stdlib file.
func LoadStdlibSource(module, src string) (*Program, error) {
	nodes, fa, err := frontend.New(frontend.Config{}).CheckStdlibSource(module, src, frontend.Mode{Tests: true})
	if err != nil {
		return nil, err
	}
	return loadStdlib(strings.TrimSuffix(strings.TrimPrefix(module, "std/"), ".nomi"), nodes, fa, irbuild.GenerateStdlibTestIR)
}

// StdlibReference runs one of the tour's stdlib reference editors: body, with
// context's import lines ahead of it, as a `test "reference"` appended to the
// stdlib module's own source, the only case selected. The case's own output
// goes to out. The tour's worker (cmd/nomi-wasm nomiRunStdlibTest) and
// cmd/nomi-docgen's reference tests both run an editor through here.
func StdlibReference(module, body, context string, out io.Writer) ([]CaseResult, error) {
	module = strings.TrimSuffix(strings.TrimPrefix(module, "std/"), ".nomi")
	src, ok := std.ReadFile(module)
	if !ok {
		return nil, fmt.Errorf("unknown stdlib module %q", module)
	}
	wrapped, line := ReferenceTestSource(string(src), body, context)
	p, err := LoadStdlibSource(module, wrapped)
	if err != nil {
		return nil, err
	}
	return p.Cases(out, TestOptions{Line: line, LineSet: true}), nil
}

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
	b.WriteString("test \"reference\" {\n")
	if context = strings.TrimSpace(context); context != "" {
		b.WriteString(indentLines(context, "  "))
		b.WriteByte('\n')
	}
	b.WriteString(indentLines(body, "  "))
	if body != "" && !strings.HasSuffix(body, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("}\n")
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
	generate func(string, []ast.Node, *analysis.FileAnalysis) (*irbuild.Program, *irbuild.Result, error)) (*Program, error) {
	lowering.Lock()
	declines := map[string]string{}
	irbuild.IRDeclineObserved = func(fn, reason string) { declines[fn] = reason }
	prog, res, err := func() (*irbuild.Program, *irbuild.Result, error) {
		// Deferred, as lower's are, so a producer panic does not leave every
		// later load waiting on the lock.
		defer lowering.Unlock()
		defer func() { irbuild.IRDeclineObserved = nil }()
		return generate(module, nodes, fa)
	}()
	if err != nil {
		return nil, err
	}
	return newProgram(prog, res, declines, newConfig(nil)), nil
}

// Warm lowers the standard library now rather than inside the first program's
// load. internal/irbuild analyzes and lowers every stdlib module once per
// process, and that is most of the first Load's time — seconds natively, far
// longer under wasm — so a long-lived host (the tour's worker) pays it at
// startup, before its first run is timed.
func Warm() {
	_, _ = LoadSource("warm", "fn main() {\n}\n")
}

// newProgram opens a lowered program. Every host function a machine calls is a generated adapter, std/compiler's table
// or a table the host passed, and the analysis already recorded the project
// root and the virtual files std/compiler's table resolves imports against.
func newProgram(prog *irbuild.Program, res *irbuild.Result, declines map[string]string, cfg config) *Program {
	p := &Program{prog: prog, res: res, declines: declines, out: cfg.out, errOut: cfg.errOut, env: cfg.env}
	if cfg.input != nil {
		p.input = rt.NewInput(cfg.input)
	}
	p.hosts = append(p.hosts, compilerhosts.Table(prog.Root, prog.VirtualFiles))
	p.hosts = append(p.hosts, cfg.hosts...)
	entryPath := prog.Entry().Path
	for _, m := range res.IR {
		if m.Name() == entryPath {
			p.entry = m
		}
	}
	return p
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
