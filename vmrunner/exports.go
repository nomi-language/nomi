package vmrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

// Instance is a program started for a host that calls its exported
// functions (`export fn`) by name, with JSON arguments and a JSON result:
// the page's worker (nomi.js's load) and the wasip1 reactor both drive one.
// Start boots the program and runs its `main` once, as the page's worker
// does; Boot only boots it, and the host runs `main` when it asks (Main), as
// the reactor's nomi_main does. Call then runs one exported function at a
// time on the same booted program, so `once` values an earlier call forced
// are shared, and effects behave as in a run (`io.print` writes to the
// output Start or Boot was given).
//
// A call that fails (a trap, an argument its parameter's FromJson refuses,
// an Int result a JSON number cannot hold exactly) answers an error whose
// text is the Nomi diagnostic, and the next call runs normally. Calls must
// not overlap.
type Instance struct {
	started *vm.Started
	exports []ir.Export
	byName  map[string]*ir.Export
	hasMain bool
}

// Export describes one exported function to a host.
type Export struct {
	Name   string
	Params []ExportParam
	// Result is the result type as Nomi writes it, or "" for a Unit result,
	// whose call answers `null`.
	Result string
	// Signature is the declaration without `export fn`:
	// `guess(game: Game, letter: String) -> Game`.
	Signature string
}

// ExportParam is one parameter of an exported function.
type ExportParam struct {
	Name string
	Type string
}

// Start decodes image, binds its hosts as Run does, boots the program with
// args as Startup.args, runs its `fn main` when it has one, and answers the
// program ready for calls. Output goes to stdout and stderr. A main that
// fails is the error, with the text `nomi run` would print, and nothing is
// started.
func Start(image []byte, stdout, stderr io.Writer, args []string, opts ...Option) (*Instance, error) {
	in, err := boot(image, stdout, stderr, nil, args, opts)
	if err != nil {
		return nil, err
	}
	if in.hasMain {
		if err := in.started.Main(); err != nil {
			in.started.Stop()
			return nil, failureError(err)
		}
	}
	return in, nil
}

// Boot is Start without running `main`: the program is booted with args as
// Startup.args and is ready for calls, and Main runs its `main` over stdin
// when the host asks. A boot that fails is the error, with the text `nomi
// run` would print.
func Boot(image []byte, stdout, stderr io.Writer, stdin io.Reader, args []string, opts ...Option) (*Instance, error) {
	return boot(image, stdout, stderr, stdin, args, opts)
}

func boot(image []byte, stdout, stderr io.Writer, stdin io.Reader, args []string, opts []Option) (*Instance, error) {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}
	im, err := ir.DecodeImage(image)
	if err != nil {
		return nil, fmt.Errorf("the compiled program cannot be read: %v", err)
	}
	entry := im.EntryModule()
	if entry == nil {
		return nil, errors.New("the compiled program has no entry module")
	}
	if !im.HasMain && len(im.Exports) == 0 {
		return nil, errNothingToRun
	}
	var hosts []HostTable
	if cfg.compiler != nil {
		hosts = append(hosts, cfg.compiler(im.Root))
	}
	hosts = append(hosts, cfg.hosts...)
	for _, t := range cfg.forEntry {
		hosts = append(hosts, t(entry.Name()))
	}
	if err := checkHostKeys(im.HostKeys, hosts); err != nil {
		return nil, err
	}
	hosts = projectHosts(hosts, cfg.compiler != nil)
	locales, err := localeData(im)
	if err != nil {
		return nil, err
	}
	m := vm.NewProgram(entry, im.Modules, stdout).WithHosts(hosts...).WithErrorOutput(stderr).WithLocaleData(locales).WithMessages(messages(im))
	if stdin != nil {
		m = m.WithInput(rt.NewInput(stdin))
	}
	var roots []*ir.Func
	var mainFn *ir.Func
	if im.HasMain {
		if mainFn = entryMain(entry); mainFn == nil {
			return nil, errors.New("the compiled program's main was not retained")
		}
		roots = vm.MainRoots(entry, mainFn)
	}
	for _, x := range im.Exports {
		roots = append(roots, x.Func, x.Encode)
		for _, p := range x.Params {
			roots = append(roots, p.Decode)
		}
	}
	roots = nonNil(roots)
	var boots []*ir.Symbol
	if boot := entry.Boot(); boot != nil {
		boots = append(boots, boot)
	}
	if found := m.Unretained(roots, boots); len(found) > 0 {
		reasons := make([]string, len(found))
		for i, u := range found {
			reasons[i] = unretainedReason(u)
		}
		return nil, fmt.Errorf("the VM cannot run this program: %s", strings.Join(reasons, "; "))
	}
	started, err := m.Start(context.Background(), args)
	if err != nil {
		return nil, failureError(err)
	}
	in := &Instance{started: started, exports: im.Exports, byName: map[string]*ir.Export{}, hasMain: mainFn != nil}
	for i := range in.exports {
		in.byName[in.exports[i].Name] = &in.exports[i]
	}
	return in, nil
}

func nonNil(fs []*ir.Func) []*ir.Func {
	out := fs[:0]
	for _, f := range fs {
		if f != nil {
			out = append(out, f)
		}
	}
	return out
}

// Exports lists the program's exported functions in declaration order.
func (in *Instance) Exports() []Export {
	out := make([]Export, len(in.exports))
	for i, x := range in.exports {
		e := Export{Name: x.Name, Result: x.Result, Signature: x.Signature()}
		for _, p := range x.Params {
			e.Params = append(e.Params, ExportParam{Name: p.Name, Type: p.Type})
		}
		out[i] = e
	}
	return out
}

// Call runs the exported function name with the arguments argsJSON holds,
// a JSON array with one element per parameter, and answers the result as
// JSON text (`null` for a Unit result).
func (in *Instance) Call(name string, argsJSON []byte) ([]byte, error) {
	x := in.byName[name]
	if x == nil {
		return nil, in.unknown(name)
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(argsJSON, &raw); err != nil || raw == nil && !bytes.Equal(bytes.TrimSpace(argsJSON), []byte("[]")) {
		return nil, fmt.Errorf("the arguments to `%s` must be a JSON array", x.Signature())
	}
	if len(raw) != len(x.Params) {
		return nil, fmt.Errorf("`%s` takes %s, and was called with %d", x.Signature(), countOf(len(x.Params), "argument"), len(raw))
	}
	args := make([]any, len(raw))
	for i, p := range x.Params {
		decoded, err := in.started.Call(p.Decode, []any{string(raw[i])})
		if err != nil {
			return nil, failureError(err)
		}
		v, why, ok := resultOf(decoded)
		if !ok {
			return nil, fmt.Errorf("argument `%s` of `%s`: %s", p.Name, x.Signature(), why)
		}
		args[i] = v
	}
	result, err := in.started.Call(x.Func, args)
	if err != nil {
		return nil, failureError(err)
	}
	if x.Encode == nil {
		return []byte("null"), nil
	}
	encoded, err := in.started.Call(x.Encode, []any{result})
	if err != nil {
		return nil, failureError(err)
	}
	text, isString := encoded.(string)
	if !isString {
		return nil, fmt.Errorf("the result of `%s` was not encoded as JSON text", x.Name)
	}
	if n, found := firstInexactInt(text); found {
		return nil, fmt.Errorf("the result of `%s` holds the Int %s, outside ±2^53: a JSON number cannot hold it exactly", x.Name, n)
	}
	return []byte(text), nil
}

// ExitCode is the status a host exits with for Main's error: 0 for nil,
// the code of a main that returned `Err(#Exit{...})`, else 1.
func ExitCode(err error) int { return vm.ExitCode(err) }

// ErrNoMain is Main on a program with no `fn main`.
var ErrNoMain = errors.New("this program has no `fn main`; it is called through its exported functions")

// Main runs the program's `fn main` on the booted program and answers its
// failure with the text `nomi run` prints, without its final newline. A
// program with no `fn main` answers ErrNoMain. The program stays booted:
// calls may follow, and so may another Main.
func (in *Instance) Main() error {
	if !in.hasMain {
		return ErrNoMain
	}
	if err := in.started.Main(); err != nil {
		return failureError(err)
	}
	return nil
}

// CallResult is Call for a host that reads one JSON answer: `{"ok":
// <result>}` when the call succeeds, `{"error": "<diagnostic>"}` when it
// fails. The wasip1 reactor's nomi_call answers it.
func (in *Instance) CallResult(name string, argsJSON []byte) []byte {
	result, err := in.Call(name, argsJSON)
	if err != nil {
		return ErrorResult(err)
	}
	out := make([]byte, 0, len(result)+8)
	out = append(out, `{"ok":`...)
	out = append(out, result...)
	return append(out, '}')
}

// ErrorResult is the `{"error": "<text>"}` answer for err.
func ErrorResult(err error) []byte {
	text := plainJSON(err.Error())
	out := make([]byte, 0, len(text)+11)
	out = append(out, `{"error":`...)
	out = append(out, text...)
	return append(out, '}')
}

// plainJSON is v as JSON with `<`, `>` and `&` left as written, so a
// signature's `->` reads as `->` rather than `->`.
func plainJSON(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}

// Close stops the program as the end of a run does.
func (in *Instance) Close() error {
	return in.started.Stop()
}

func (in *Instance) unknown(name string) error {
	if len(in.exports) == 0 {
		return fmt.Errorf("no exported function `%s`: this program exports no functions", name)
	}
	names := make([]string, len(in.exports))
	for i, x := range in.exports {
		names[i] = x.Name
	}
	sort.Strings(names)
	return fmt.Errorf("no exported function `%s`; the exported functions are %s", name, strings.Join(names, ", "))
}

// resultOf reads a Result record: its Ok payload, or its Err payload's text.
func resultOf(v any) (ok any, why string, isOk bool) {
	r, isRecord := v.(*rt.Record)
	if !isRecord || r == nil || r.NumFields() != 1 {
		return nil, fmt.Sprintf("the decoder answered %T, not a Result", v), false
	}
	if r.Variant().Name == "Ok" {
		return r.Field(0), "", true
	}
	text, _ := r.Field(0).(string)
	return nil, text, false
}

// failureError is a call's failure as the error a host sees: the text
// `nomi run` prints for it, without its final newline.
func failureError(err error) error {
	failure, limit := vm.ProgramFailure(err)
	if limit {
		return fmt.Errorf("the VM cannot run this program: machine limit: %v", failure)
	}
	var exit *vm.Exit
	if errors.As(failure, &exit) {
		// Its text is the message, and ExitCode reads its status.
		return exit
	}
	var b strings.Builder
	WriteFailure(&b, failure)
	return errors.New(strings.TrimRight(b.String(), "\n"))
}

// maxExactInt is 2^53: an Int of greater magnitude is not a number JSON
// readers (a JavaScript number, a float64) hold exactly.
var maxExactInt = new(big.Int).Lsh(big.NewInt(1), 53)

// firstInexactInt finds the first integer in the JSON text whose magnitude
// is over 2^53. Only an Int encodes as a bare integer: a Float renders with
// a fraction or an exponent past six digits.
func firstInexactInt(text string) (string, bool) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", false
		}
		n, isNumber := tok.(json.Number)
		if !isNumber || strings.ContainsAny(string(n), ".eE") {
			continue
		}
		v, ok := new(big.Int).SetString(string(n), 10)
		if ok && v.CmpAbs(maxExactInt) > 0 {
			return string(n), true
		}
	}
}

func countOf(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
