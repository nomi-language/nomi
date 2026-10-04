// Package vmhost is Nomi's embedding API: a Go program loads a Nomi program,
// runs it, calls its functions and runs its tests on the VM.
//
//	p, err := vmhost.Load("policy.nomi", vmhost.WithHosts(myHosts))
//	quote, err := p.Call(ctx, "price", vmhost.Fields{"weight": 12.5, "distance": 800})
//
// Loading runs the shared front end (internal/frontend), lowers the program
// to IR (internal/irbuild's GenerateIR) and opens the VM (internal/vm) over it.
// `nomi run`, `nomi test`, `nomi check`, the REPL, the tour and an FFI
// project's wrapper all run through this package.
//
// A program that reaches a function the VM cannot run fails with a *Blocked naming each such function and why it
// was not retained, before the program's first effect.
//
// Go functions a program calls are host tables (WithHosts) of hostadapt.Func
// values over the VM's own Values: an FFI wrapper generates one from the
// project's `go` bindings, and an embedder writes one for the `host fn`s its
// program declares.
//
// It is a public package because a generated FFI wrapper is a separate Go
// module, and a separate module can import only the public packages of this
// one.
package vmhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
	"github.com/nomi-language/nomi/vmrunner"
)

// Program is one lowered program, ready to run, call or test on the VM.
type Program struct {
	prog     *irbuild.Program
	res      *irbuild.Result
	entry    *ir.Module
	declines map[string]string
	// declineDetails are the same declines with the file and position each
	// was taken at, which Unsupported reports them by.
	declineDetails []*irbuild.Decline
	// hosts are the host tables every machine binds beside its own stdlib
	// adapters: std/compiler's, then the tables WithHosts gave.
	hosts []HostTable
	// out is Call's output writer and env the Startup.env overrides.
	out io.Writer
	env map[string]string
	// errOut is the runtime's diagnostic output, or nil for os.Stderr.
	errOut io.Writer
	// input is what `io.read_line` reads, shared by every machine this
	// program opens, or nil for no input.
	input *rt.Input
}

// HostTable is a table of host adapters: bound against a machine's Env, it
// answers each adapter by the key the program's `host fn` crosses under. A
// generated adapter file's Bind is one; an FFI wrapper passes the table it
// generated for the project's Go bindings; an embedder writes one.
type HostTable = vm.HostTable

// Blocked is the error a program answers when it reaches something the VM
// cannot run. Each reason is one line of the form `[<name>] <why>`.
type Blocked struct {
	Reasons []string
}

func (b *Blocked) Error() string {
	return "the VM cannot run this program:\n  " + strings.Join(b.Reasons, "\n  ")
}

// Write prints the grep-friendly report `nomi run` prints: one
// `BLOCKED <label> <reason>` line per reason, then a closing line.
// It is vmrunner's, so a built binary reports what `nomi run` reports.
func (b *Blocked) Write(w io.Writer, label string) {
	vmrunner.WriteBlocked(w, label, b.Reasons)
}

// Disassemble is the VM bytecode of the entry's function named name.
func (p *Program) Disassemble(name string) (string, error) {
	return p.machine(io.Discard).Disassemble(name)
}

// HasMain reports whether the entry declares `fn main`.
func (p *Program) HasMain() bool { return p.res.HasMain }

// RequireMain is nil when the entry declares `fn main`, and otherwise the
// error `nomi run` and `nomi build` give for it: there is nothing to run or
// build, where verb is "run" or "build". An entry that declares tests is
// pointed at `nomi test`.
func (p *Program) RequireMain(verb string) error {
	if p.HasMain() {
		return nil
	}
	path := DisplayPath(p.prog.Entry().Path)
	msg := fmt.Sprintf("%s declares no `fn main`, so there is no program to %s", path, verb)
	if p.prog.HasTests {
		msg += fmt.Sprintf("; to run its tests, use `nomi test %s`", path)
	}
	return errors.New(msg)
}

func (p *Program) machine(out io.Writer) *vm.Machine {
	m := vm.NewProgram(p.entry, p.res.IRModules(), out).WithHosts(p.hosts...)
	if p.errOut != nil {
		m = m.WithErrorOutput(p.errOut)
	}
	if p.input != nil {
		m = m.WithInput(p.input)
	}
	return m
}

// Run boots the program and calls `main`, writing its output to out. A
// program with no `fn main` runs nothing.
//
// The error is a *Blocked when the program reaches something the VM cannot
// run, and otherwise the program's own failure, which
// WriteFailure renders as `nomi run` does.
func (p *Program) Run(ctx context.Context, out io.Writer, args []string, handleSignals bool) error {
	return p.run(ctx, out, args, handleSignals, p.env)
}

// run is Run with environment overrides for the Startup a boot receives,
// which `compiler.run_file` supplies.
func (p *Program) run(ctx context.Context, out io.Writer, args []string, handleSignals bool, hostEnv map[string]string) error {
	if !p.HasMain() {
		return nil
	}
	mainFn := p.entryFunc("main")
	if mainFn == nil {
		return &Blocked{Reasons: []string{p.notRetained("main")}}
	}
	m := p.machine(out).WithHostEnv(hostEnv)
	var boots []*ir.Symbol
	if boot := p.entry.Boot(); boot != nil {
		boots = append(boots, boot)
	}
	if reasons := p.reasons(m.Unretained([]*ir.Func{mainFn}, boots)); len(reasons) > 0 {
		return &Blocked{Reasons: reasons}
	}
	failure, limit := vm.ProgramFailure(m.Main(ctx, args, handleSignals))
	if limit {
		return &Blocked{Reasons: []string{machineLimit(failure)}}
	}
	return failure
}

// Test runs the entry's test cases that the VM can run and reports every
// selected case in source order: a case that ran through rep.Result, and a
// case the VM cannot run through rep.Blocked with its reasons. label names a
// case in the report. Every runnable case runs before anything is reported,
// which puts a case's own output above the report.
//
// file is the absolute path a JSON report locates each case by, with the
// case's first and last lines.
func (p *Program) Test(out io.Writer, rep *TestReport, file string, opts TestOptions, label func(string) string) {
	for _, c := range p.Cases(out, opts) {
		loc := TestLocation{File: file, Line: c.Line, EndLine: c.EndLine}
		if c.Blocked != nil {
			rep.BlockedAt(loc, label(c.Name), c.Blocked)
			continue
		}
		rep.ResultAt(loc, label(c.Name), c.Err)
	}
}

// CaseResult is one case's outcome. Blocked is non-nil for a case the VM
// could not run, and then Err is meaningless.
type CaseResult struct {
	Name    string
	Line    int
	EndLine int
	Err     error
	Blocked []string
}

// Cases runs the entry's runnable test cases, writing their own output to
// out, and answers every selected case in source order. When the file declares
// duplicate case names it answers those as failures and runs nothing.
func (p *Program) Cases(out io.Writer, opts TestOptions) []CaseResult {
	plan, duplicates := frontend.SelectTests(p.prog.Entry().Nodes, opts)
	if len(duplicates) > 0 {
		results := make([]CaseResult, len(duplicates))
		for i, d := range duplicates {
			results[i] = CaseResult{Name: d.Name, Err: d.Err, Line: d.Line, EndLine: d.EndLine}
		}
		return results
	}
	retained := map[string]ir.TestCase{}
	if p.entry != nil {
		for _, c := range p.entry.Tests() {
			retained[c.Name()] = c
		}
	}
	var m *vm.Machine
	if p.entry != nil {
		m = p.machine(out)
	}
	type outcome struct {
		blocked []string
		err     error
		run     int // index into the runnable slice, or -1
	}
	outcomes := make([]outcome, len(plan))
	var runnable []ir.TestCase
	for i, tc := range plan {
		outcomes[i].run = -1
		if tc.Clock == frontend.InvalidClock {
			outcomes[i].err = frontend.ErrInvalidClock()
			continue
		}
		c, ok := retained[tc.FullName()]
		if !ok || p.entry == nil {
			outcomes[i].blocked = []string{p.testNotRetained(tc.FullName())}
			continue
		}
		var boots []*ir.Symbol
		if b := c.Group().Boot; b != nil {
			boots = append(boots, b, c.Group().Startup)
		}
		if reasons := p.reasons(m.Unretained([]*ir.Func{c.Fn()}, boots)); len(reasons) > 0 {
			outcomes[i].blocked = reasons
			continue
		}
		outcomes[i].run = len(runnable)
		runnable = append(runnable, c)
	}
	var failures []error
	var limits []string
	if len(runnable) > 0 {
		failures, limits = m.RunCases(runnable)
	}
	results := make([]CaseResult, len(plan))
	for i, tc := range plan {
		o := outcomes[i]
		results[i].Name, results[i].Line, results[i].EndLine = tc.FullName(), tc.Line, tc.LastLine()
		switch {
		case o.blocked != nil:
			results[i].Blocked = o.blocked
		case o.run >= 0 && limits[o.run] != "":
			results[i].Blocked = []string{"[vm] machine limit: " + limits[o.run]}
		case o.run >= 0:
			results[i].Err = failures[o.run]
		default:
			results[i].Err = o.err
		}
	}
	return results
}

func (p *Program) entryFunc(name string) *ir.Func {
	if p.entry == nil {
		return nil
	}
	for _, f := range p.entry.Funcs() {
		if f.Name() == name && len(f.Params()) == 0 {
			return f
		}
	}
	return nil
}

func (p *Program) reasons(found []vm.Unretained) []string {
	out := make([]string, 0, len(found))
	for _, u := range found {
		switch u.Kind {
		case vm.NotRetained:
			out = append(out, p.notRetained(u.Name))
		case vm.OnceNotRetained:
			out = append(out, fmt.Sprintf("[%s] once initializer not retained: %s", u.Name, p.decline(u.Name)))
		case vm.NoBinding:
			out = append(out, fmt.Sprintf("[%s] crosses into Go and the VM has no binding for it", u.Name))
		case vm.NoImplementation:
			out = append(out, fmt.Sprintf("[%s] dispatched, and no implementation was retained", u.Name))
		}
	}
	return out
}

func (p *Program) notRetained(name string) string {
	return fmt.Sprintf("[%s] not retained: %s", name, p.decline(name))
}

func (p *Program) testNotRetained(name string) string {
	reason, ok := p.declines["test body: "+name]
	if !ok {
		reason = "no decline reason recorded"
	}
	return "[test body] not retained: " + reason
}

// decline is the first reason the producer's last attempt at name declined
// for. The census names an attempt by its declaration's own name, so a
// qualified IR name falls back to its last segment.
func (p *Program) decline(name string) string {
	if reason, ok := p.declines[name]; ok {
		return reason
	}
	short := name
	if i := strings.LastIndex(name, "."); i >= 0 {
		short = name[i+1:]
		if reason, ok := p.declines[short]; ok {
			return reason
		}
	}
	return p.undeclined(short)
}

// declares reports whether the entry file declares a top-level function or
// `host fn` named name.
func (p *Program) declares(name string) bool {
	for _, n := range p.prog.Entry().Nodes {
		switch d := n.(type) {
		case *ast.FuncDef:
			if d.Name == name && !d.ImplFunction {
				return true
			}
		case *ast.ExternFunc:
			if d.Name == name {
				return true
			}
		}
	}
	return false
}

// undeclined describes a declaration the builder made no recorded attempt at,
// from the declaration itself.
func (p *Program) undeclined(name string) string {
	for _, m := range p.prog.Modules {
		for _, n := range m.Nodes {
			switch d := n.(type) {
			case *ast.ExternFunc:
				if d.Name != name {
					continue
				}
				if d.ForeignName != "" {
					return "a `go`-bound declaration (go " + d.ForeignAlias + "." + d.ForeignName + ") has no VM body"
				}
				return "a `host fn` with no VM body"
			case *ast.FuncDef:
				if d.Name == name {
					return "no decline reason recorded"
				}
			}
		}
	}
	return "no decline reason recorded (not declared in the program's own files; " +
		"stdlib bodies are lowered once per process, outside the decline census)"
}

func machineLimit(err error) string {
	return "[vm] machine limit: " + err.Error()
}

// IsBlocked reports whether err is a *Blocked.
func IsBlocked(err error) (*Blocked, bool) {
	var b *Blocked
	if errors.As(err, &b) {
		return b, true
	}
	return nil, false
}
