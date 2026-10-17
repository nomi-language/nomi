package vmhost

// COMPILING A DEBUGGER EXPRESSION: a watch, a console input, a hover, a
// breakpoint's condition or a logpoint's message, in the file of a stopped
// frame.
//
// The expression becomes the body of a function appended to a copy of the
// frame's file, whose parameters are the frame's variables the expression
// names, at the types the variable table records:
//
//	fn nomi_debug_eval(total: Int, p: Point) -> <the expression's type> {
//	    _ = total
//	    _ = p
//	    (<the expression>)
//	}
//
// Appended, so the expression sees the file's imports and top-level
// declarations as the frame's own code does, and every line of the file
// keeps its number. A variable's type, or the expression's, may be declared
// in a file the frame's file does not import (a value a callee returned):
// such a type is imported by name from the one file of the program that
// declares it `pub`, by an import written at the start of the file's first
// line and ended with `;`, so no line moves. The expression's type comes
// from a first check of the same function with the expression bound to a
// local, since a function must spell its result type. The copy is lowered as its own program (the file and what it
// imports) and run on a view of the stopped machine (vm.Scope.Run).

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/std"
)

// DebugParam is a variable a debugger expression may name: its name and its
// type as an annotation spells it (vm.ScopeVar's).
type DebugParam struct {
	Name, Type string
}

// DebugExpr is an expression compiled for a frame of a file.
type DebugExpr struct {
	// Type is the expression's type.
	Type string
	// Params are the names of the variables the compiled function takes, in
	// order: Run's arguments.
	Params []string
	entry  *ir.Module
	rest   []*ir.Module
	fn     *ir.Func
	// startLine is the synthesized function's first line.
	startLine int
}

const (
	debugEvalName  = "nomi_debug_eval"
	debugValueName = "nomi_debug_value"
)

// ExprError is an expression the front end rejects: each message, worded
// for the console with no position inside the synthesized function.
type ExprError struct {
	Messages []string
}

func (e *ExprError) Error() string { return strings.Join(e.Messages, "\n") }

// CompileExpr compiles expr for a frame of file whose visible variables are
// vars. A variable expr does not name is not a parameter.
func (p *Program) CompileExpr(file string, vars []DebugParam, expr string) (*DebugExpr, error) {
	if strings.TrimSpace(expr) == "" {
		return nil, errors.New("there is no expression to evaluate")
	}
	// In a standard library frame, the expression is a file of its own
	// beside the program's entry: it sees the frame's variables, the
	// prelude and what it can import, and not the std file's private
	// declarations (std files are checked as std, without the prelude).
	stdModule := ""
	var src string
	var err error
	if std.IsSourcePath(file) {
		entry := p.prog.Entry().Path
		if std.IsSourcePath(entry) {
			return nil, errors.New("expressions are evaluated in a standard library frame only when the program is not itself the standard library's")
		}
		stdModule = strings.TrimSuffix(filepath.Base(file), ".nomi")
		file = filepath.Join(filepath.Dir(entry), debugEvalName+".nomi")
	} else if src, err = p.sourceOf(file); err != nil {
		return nil, err
	}
	params := namedIn(expr, vars)
	base := strings.TrimRight(src, "\n") + "\n\n"
	startLine := strings.Count(base, "\n") + 1
	cfg := p.cfg
	cfg.input = nil
	fc, err := cfg.frontendConfig()
	if err != nil {
		return nil, err
	}
	imported := map[string]bool{}
	// withImports retries a check that failed on types the file cannot
	// name, once those are imported; it answers whether it added any.
	withImports := func(err error) bool {
		added := false
		for _, name := range unknownTypes(err) {
			if imported[name] {
				continue
			}
			if path := p.declaringFile(file, stdModule, name); path != "" {
				imported[name] = true
				base = importAtStart(base, path+"."+name)
				added = true
			}
		}
		return added
	}
	var checked *irbuild.Program
	for {
		probe := base + debugFunc(params, "", debugValueName+" = ("+expr+")\n    _ = "+debugValueName)
		checked, err = irbuild.AnalyzeFileSource(file, probe, fc)
		if err == nil {
			break
		}
		if !withImports(err) {
			return nil, exprError(err, file, startLine)
		}
	}
	typ, err := debugValueType(checked)
	if err != nil {
		return nil, err
	}
	var lowered *Program
	for {
		full := base + debugFunc(params, typ, "("+expr+")")
		lowered, err = lower(cfg, func() (*irbuild.Program, error) { return irbuild.AnalyzeFileSource(file, full, fc) })
		if err == nil {
			break
		}
		if !withImports(err) {
			return nil, exprError(err, file, startLine)
		}
	}
	var fn *ir.Func
	if lowered.entry != nil {
		for _, f := range lowered.entry.Funcs() {
			if f.Name() == debugEvalName {
				fn = f
			}
		}
	}
	if fn == nil {
		return nil, errors.New("the debugger cannot run this expression yet: " + lowered.decline(debugEvalName))
	}
	out := &DebugExpr{Type: typ, entry: lowered.entry, rest: lowered.res.IRModules(), fn: fn, startLine: startLine}
	for _, v := range params {
		out.Params = append(out.Params, v.Name)
	}
	return out, nil
}

// Run evaluates the expression in scope, whose variables supply its
// parameters, under lim. Unless allowEffects, one that can reach an effect
// is refused with *vm.EffectsError before it runs.
func (e *DebugExpr) Run(scope *vm.Scope, lim EvalLimits, allowEffects bool) (Value, error) {
	args := make([]any, len(e.Params))
	for i, name := range e.Params {
		v, ok := scope.Var(name)
		if !ok {
			return nil, fmt.Errorf("%s is not visible here", name)
		}
		args[i] = v.Value
	}
	v, err := scope.Run(e.entry, e.rest, e.fn, args, lim, allowEffects)
	if errors.Is(err, vm.ErrLimit) {
		return nil, ErrEvalLimit
	}
	if err != nil {
		if failure, _ := programFailure(err); failure != nil {
			err = failure
		}
		return nil, &EvalFailure{Text: e.failureText(err), Err: err}
	}
	return v, nil
}

// EvalFailure is an expression that failed as it ran: a trap, or a failure
// of code it called.
type EvalFailure struct {
	// Text is the failure's text, without the line a trap names when that
	// line is inside the synthesized function rather than the program.
	Text string
	Err  error
}

func (e *EvalFailure) Error() string { return e.Text }
func (e *EvalFailure) Unwrap() error { return e.Err }

var trapLine = regexp.MustCompile(`^line (\d+): `)

func (e *DebugExpr) failureText(err error) string {
	text := strings.TrimRight(err.Error(), "\n")
	if m := trapLine.FindStringSubmatch(text); m != nil {
		if line, _ := strconv.Atoi(m[1]); line >= e.startLine {
			text = text[len(m[0]):]
		}
	}
	return text
}

var unknownTypePatterns = []*regexp.Regexp{
	regexp.MustCompile(`unknown type "([A-Z][A-Za-z0-9_]*)"`),
	regexp.MustCompile(`undefined type or variant '([A-Z][A-Za-z0-9_]*)'`),
}

// unknownTypes are the type names a failed check could not resolve.
func unknownTypes(err error) []string {
	var out []string
	text := err.Error()
	for _, re := range unknownTypePatterns {
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			out = append(out, m[1])
		}
	}
	return out
}

// declaringFile is the import path of the one file of the program, other
// than file, that declares a `pub` type named name, or "" when none or
// several do. With none, a std module (a std frame's own) that declares it
// answers.
func (p *Program) declaringFile(file, stdModule, name string) string {
	if path := p.userDeclaringFile(file, name); path != "" || stdModule == "" {
		return path
	}
	if src, ok := std.ReadFile(stdModule); ok {
		decl := regexp.MustCompile(`(?m)^pub\s+(opaque\s+)?(struct|enum|type|interface)\s+` + regexp.QuoteMeta(name) + `\b`)
		if decl.Match(src) {
			return "std/" + stdModule
		}
	}
	return ""
}

func (p *Program) userDeclaringFile(file, name string) string {
	found := ""
	for _, mod := range p.prog.Modules {
		if mod.Path == file || std.IsSourcePath(mod.Path) {
			continue
		}
		for _, n := range mod.Nodes {
			declared := false
			switch d := n.(type) {
			case *ast.StructDef:
				declared = d.Public && d.Name == name
			case *ast.EnumDef:
				declared = d.Public && d.Name == name
			case *ast.TypeDef:
				declared = d.Public && d.Name == name
			case *ast.InterfaceDef:
				declared = d.Public && d.Name == name
			}
			if declared {
				if found != "" && found != mod.Name {
					return ""
				}
				found = mod.Name
			}
		}
	}
	return found
}

// importAtStart is src with `import what;` at the start of its first line
// (after a `#!` line), so every line keeps its number.
func importAtStart(src, what string) string {
	at := 0
	if strings.HasPrefix(src, "#!") {
		if i := strings.IndexByte(src, '\n'); i >= 0 {
			at = i + 1
		}
	}
	return src[:at] + "import " + what + "; " + src[at:]
}

// debugFunc is the synthesized function: params read once each, so none is
// an unread parameter, then body.
func debugFunc(params []DebugParam, result, body string) string {
	var b strings.Builder
	b.WriteString("fn " + debugEvalName + "(")
	for i, v := range params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(v.Name + ": " + v.Type)
	}
	b.WriteString(")")
	if result != "" {
		b.WriteString(" -> " + result)
	}
	b.WriteString(" {\n")
	for _, v := range params {
		b.WriteString("    _ = " + v.Name + "\n")
	}
	b.WriteString("    " + body + "\n}\n")
	return b.String()
}

// namedIn are the variables whose names expr mentions as a word, the
// innermost of each name (the last in vars).
func namedIn(expr string, vars []DebugParam) []DebugParam {
	var out []DebugParam
	index := map[string]int{}
	for _, v := range vars {
		if !regexp.MustCompile(`(^|[^A-Za-z0-9_?])` + regexp.QuoteMeta(v.Name) + `($|[^A-Za-z0-9_?])`).MatchString(expr) {
			continue
		}
		if i, seen := index[v.Name]; seen {
			out[i] = v
			continue
		}
		index[v.Name] = len(out)
		out = append(out, v)
	}
	return out
}

// debugValueType is the checked type of the probe's value binding.
func debugValueType(prog *irbuild.Program) (string, error) {
	fa := prog.Entry().FA
	if fa != nil {
		for _, sym := range fa.Definitions {
			if sym == nil || sym.Name != debugValueName || sym.Kind != analysis.SymbolBinding || sym.Type == nil {
				continue
			}
			text := sym.Type.String()
			if strings.Contains(text, "?") {
				return "", fmt.Errorf("the expression's type (%s) is not determined; give it a type, as a binding's annotation would", text)
			}
			return text, nil
		}
	}
	return "", errors.New("the expression has no type")
}

// exprError words a front-end error for the console: a diagnostic inside
// the synthesized function by its message alone, one elsewhere in file (the
// file changed since the program started) with its position.
func exprError(err error, file string, startLine int) error {
	var diags frontend.Diagnostics
	if !errors.As(err, &diags) {
		return err
	}
	out := &ExprError{}
	seen := map[string]bool{}
	for _, d := range diags {
		msg := d.Message
		if d.Path != file || d.Line < startLine {
			msg = fmt.Sprintf("%s:%d:%d: %s", DisplayPath(d.Path), d.Line, d.Col, d.Message)
		}
		msg = strings.ReplaceAll(msg, debugValueName, "the expression")
		if seen[msg] {
			continue
		}
		seen[msg] = true
		out.Messages = append(out.Messages, msg)
	}
	if len(out.Messages) == 0 {
		return err
	}
	return out
}

// KeepSources records the text of every file of the program's own now, so
// a debugger expression compiled later compiles against the code that runs
// even when the file is edited meanwhile.
func (p *Program) KeepSources() {
	if p.sources == nil {
		p.sources = map[string]string{}
	}
	for _, mod := range p.prog.Modules {
		if _, kept := p.sources[mod.Path]; kept || std.IsSourcePath(mod.Path) {
			continue
		}
		if data, err := os.ReadFile(mod.Path); err == nil {
			p.sources[mod.Path] = string(data)
		}
	}
}

// sourceOf is the text file had when the program was loaded: its in-memory
// text, or what the disk holds.
func (p *Program) sourceOf(file string) (string, error) {
	if text, ok := p.sources[file]; ok {
		return text, nil
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
