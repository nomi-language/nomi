// Package vmrunner is the runtime half of `nomi build`: a runner binary reads
// the linked IR image `nomi build` appended to its own executable, decodes it
// and runs `main` on the VM exactly as `nomi run` does.
//
// It links the VM, rt, the stdlib host adapters and the IR decoder, and no
// front end: no parser, analyzer, IR builder or LSP. vmrunner's deps test
// pins that. A program that crosses into std/compiler's hosts is the one
// exception, and its runner links nomi/vmrunner/compiler, which brings the
// front end with it.
//
// A native executable is the runner's bytes, then the image, then a fixed
// 24-byte trailer: the image's offset and length as little-endian uint64s
// and the magic "NOMIEXE\x01". A WebAssembly runner carries the image in a
// custom section instead (wasm.go). A built binary runs at `nomi run` speed: it
// skips the front end and the lowering at startup and runs the same VM over
// the same IR. It is not native code.
//
// A runner nothing was appended to (nomi-vm) runs a .nomic file `nomi build --no-vm`
// wrote, handed to it by path or framed on stdin (imagefile.go, bare.go).
//
// It is a public package because an FFI project's runner is generated into a
// separate Go module, which can import only this module's public packages.
package vmrunner

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/stdmessageformat"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

// trailerMagic closes every built executable.
const trailerMagic = "NOMIEXE\x01"

// TrailerSize is the length of the trailer after the image.
const TrailerSize = 8 + 8 + len(trailerMagic)

// HostTable is a table of host adapters, as vmhost.HostTable.
type HostTable = vm.HostTable

// Trailer is the fixed tail of a built executable for an image of length n
// starting at offset.
func Trailer(offset, n int64) []byte {
	b := make([]byte, 0, TrailerSize)
	b = binary.LittleEndian.AppendUint64(b, uint64(offset))
	b = binary.LittleEndian.AppendUint64(b, uint64(n))
	return append(b, trailerMagic...)
}

// ErrNoImage is what ReadImage answers for an executable nothing was
// appended to: the bare runner.
var ErrNoImage = errors.New("no Nomi program is appended to this executable; " +
	"it is the nomi-vm `nomi build` appends one to")

// ReadImage reads the image `nomi build` added to the executable at path:
// after its end for a native executable, or for a WebAssembly module as
// ReadWasmImage reads it.
func ReadImage(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	head := make([]byte, len(wasmHeader))
	if n, _ := f.ReadAt(head, 0); IsWasm(head[:n]) {
		return ReadWasmImage(f, size)
	}
	if size < int64(TrailerSize) {
		return nil, ErrNoImage
	}
	tail := make([]byte, TrailerSize)
	if _, err := f.ReadAt(tail, size-int64(TrailerSize)); err != nil {
		return nil, err
	}
	if string(tail[16:]) != trailerMagic {
		return nil, ErrNoImage
	}
	offset := int64(binary.LittleEndian.Uint64(tail[0:8]))
	n := int64(binary.LittleEndian.Uint64(tail[8:16]))
	if offset < 0 || n < 0 || offset+n != size-int64(TrailerSize) {
		return nil, fmt.Errorf("the appended Nomi program's trailer is damaged (offset %d, length %d, file %d bytes)",
			offset, n, size)
	}
	image := make([]byte, n)
	if _, err := f.ReadAt(image, offset); err != nil {
		return nil, err
	}
	return image, nil
}

// Option configures a runner.
type Option func(*config)

type config struct {
	hosts    []HostTable
	forEntry []func(entry string) HostTable
	compiler func(root string) HostTable
}

// WithHosts adds host tables: an FFI project's generated adapters.
func WithHosts(tables ...HostTable) Option {
	return func(c *config) { c.hosts = append(c.hosts, tables...) }
}

// WithEntryHosts adds a host table built for the entry's path, which an FFI
// project's table needs to choose the key a declaration registers under.
func WithEntryHosts(table func(entry string) HostTable) Option {
	return func(c *config) { c.forEntry = append(c.forEntry, table) }
}

// WithCompiler binds std/compiler's hosts for the image's project root.
// nomi/vmrunner/compiler's Table is the one to pass.
func WithCompiler(table func(root string) HostTable) Option {
	return func(c *config) { c.compiler = table }
}

// Main runs the program appended to this executable with the process's own
// streams, arguments and signal handling, and exits with `nomi run`'s status.
//
// A runner nothing was appended to runs the image file its arguments or
// stdin hand it (RunBare). A js/wasm runner a page loaded to call the
// program's exported functions serves those calls instead (calls_js.go).
func Main(opts ...Option) {
	args := ProgramArgs()
	image, err := ownImage()
	if errors.Is(err, ErrNoImage) {
		os.Exit(RunBare(args, os.Stdout, os.Stderr, os.Stdin, true, err, opts...))
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "nomi: %v\n", err)
		os.Exit(1)
	}
	// A page's `run` hands the program its files and takes them back
	// (files_js.go); elsewhere both do nothing.
	seedPageFiles()
	if serveCalls(image, opts) {
		return
	}
	code := Run(image, os.Stdout, os.Stderr, os.Stdin, args, true, opts...)
	returnPageFiles()
	os.Exit(code)
}

// ProgramArgs answers the program's arguments: os.Args after the runner's own
// name. A WASI runner may pass no argv at all, not even a name (Node's
// node:wasi does by default), so os.Args can be empty.
func ProgramArgs() []string {
	if len(os.Args) == 0 {
		return nil
	}
	return os.Args[1:]
}

// OwnImage answers the image `nomi build` added to this runner, read as Main
// reads it: from the executable's tail, from a wasip1 module's own memory,
// or from a js/wasm module's nomi.image custom section. The wasip1 reactor (cmd/nomi-vm-reactor),
// which has no Main, reads its program with it. A runner with no program
// answers an error that is ErrNoImage.
func OwnImage() ([]byte, error) { return ownImage() }

// Run decodes image and runs its `main` as `nomi run` does, and answers the
// exit status `nomi run` would exit with. handleSignals installs the
// process-wide signal handler, which only a caller that owns the process may
// do.
func Run(image []byte, stdout, stderr io.Writer, stdin io.Reader, args []string, handleSignals bool, opts ...Option) int {
	return run(image, stdout, stderr, stdin, args, handleSignals, false, opts)
}

// RunCompiled is Run for a compiled program (.nomic) a runner was handed:
// one with exported functions and no `fn main` was built for a host that
// calls them, so it exits 1 and says where it runs rather than exiting
// quietly. A page's js/wasm module runs such a program through Run, which
// runs nothing and exits 0.
func RunCompiled(image []byte, stdout, stderr io.Writer, stdin io.Reader, args []string, handleSignals bool, opts ...Option) int {
	return run(image, stdout, stderr, stdin, args, handleSignals, true, opts)
}

func run(image []byte, stdout, stderr io.Writer, stdin io.Reader, args []string, handleSignals, compiled bool, opts []Option) int {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}
	im, err := ir.DecodeImage(image)
	if err != nil {
		fmt.Fprintf(stderr, "nomi: the compiled program cannot be read: %v\n", err)
		return 1
	}
	entry := im.EntryModule()
	var hosts []HostTable
	if cfg.compiler != nil {
		hosts = append(hosts, cfg.compiler(im.Root))
	}
	hosts = append(hosts, cfg.hosts...)
	if entry != nil {
		for _, t := range cfg.forEntry {
			hosts = append(hosts, t(entry.Name()))
		}
	}
	if err := checkHostKeys(im.HostKeys, hosts); err != nil {
		fmt.Fprintf(stderr, "nomi: %v\n", err)
		return 1
	}
	hosts = projectHosts(hosts, cfg.compiler != nil)
	if !im.HasMain {
		if compiled && len(im.Exports) > 0 {
			fmt.Fprintf(stderr, "nomi: %v. Load it into the reactor `nomi build-vm --reactor` writes\n", ErrNoMain)
			return 1
		}
		return 0
	}
	label := "main"
	if entry != nil {
		label = rt.DisplayPath(entry.Name())
	}
	mainFn := entryMain(entry)
	if mainFn == nil {
		WriteBlocked(stderr, label, []string{"[main] not retained"})
		return 1
	}
	var in *rt.Input
	if stdin != nil {
		in = rt.NewInput(stdin)
	}
	locales, err := localeData(im)
	if err != nil {
		fmt.Fprintf(stderr, "nomi: %v\n", err)
		return 1
	}
	m := vm.NewProgram(entry, im.Modules, stdout).WithHosts(hosts...).WithErrorOutput(stderr).WithLocaleData(locales).WithMessages(messages(im))
	if in != nil {
		m = m.WithInput(in)
	}
	var boots []*ir.Symbol
	if boot := entry.Boot(); boot != nil {
		boots = append(boots, boot)
	}
	// `nomi build` refused a program the VM would block, so this is a
	// runner that does not match its image (an FFI binding it lacks).
	if found := m.Unretained(vm.MainRoots(entry, mainFn), boots); len(found) > 0 {
		reasons := make([]string, len(found))
		for i, u := range found {
			reasons[i] = unretainedReason(u)
		}
		WriteBlocked(stderr, label, reasons)
		return 1
	}
	failure, limit := vm.ProgramFailure(m.Main(context.Background(), args, handleSignals))
	if limit {
		WriteBlocked(stderr, label, []string{"[vm] machine limit: " + failure.Error()})
		return 1
	}
	if failure != nil {
		WriteFailure(stderr, failure)
		return vm.ExitCode(failure)
	}
	return 0
}

// localeData is the CLDR data image carries (ir.Image.Locales), decoded:
// the only locale data a runner has, since it links none of its own. An
// image that carries none answers empty data, so a program that never
// reached locale data does not read some by accident.
func localeData(im ir.Image) (*cldrdata.Data, error) {
	if len(im.Locales) == 0 {
		return cldrdata.Empty(), nil
	}
	d, err := cldrdata.Decode(im.Locales)
	if err != nil {
		return nil, fmt.Errorf("the compiled program's locale data cannot be read: %v", err)
	}
	return d, nil
}

// messages is the program's messages: the tables its image carries, which
// are those of the messages and nouns it reaches, and in a page the locale
// files it loads.
func messages(im ir.Image) *stdmessageformat.Runtime {
	return stdmessageformat.NewRuntime(func() ([]string, []string) {
		return vm.MessageTables(im.Modules, vm.ModuleFuncs(im.Modules), func(name string) bool { return name == vm.MessageModule })
	}, localeFetcher())
}

func entryMain(entry *ir.Module) *ir.Func {
	if entry == nil {
		return nil
	}
	for _, f := range entry.Funcs() {
		if f.Name() == "main" && len(f.Params()) == 0 {
			return f
		}
	}
	return nil
}

func unretainedReason(u vm.Unretained) string {
	switch u.Kind {
	case vm.NoBinding:
		return fmt.Sprintf("[%s] crosses into Go and the VM has no binding for it", hostadapt.DisplayName(u.Name))
	case vm.NoImplementation:
		return fmt.Sprintf("[%s] dispatched, and no implementation was retained", u.Name)
	case vm.OnceNotRetained:
		return fmt.Sprintf("[%s] once initializer not retained", u.Name)
	}
	return fmt.Sprintf("[%s] not retained", u.Name)
}

// projectHosts is hosts as a machine binds them: std/compiler's table, first
// when withCompiler, as it is, and every other table, a project's or an
// embedder's, under its keys' project names (hostadapt.ProjectTable), so no
// key of theirs answers a std crossing.
func projectHosts(hosts []HostTable, withCompiler bool) []HostTable {
	out := make([]HostTable, 0, len(hosts))
	for i, t := range hosts {
		if withCompiler && i == 0 {
			out = append(out, t)
			continue
		}
		out = append(out, hostadapt.ProjectTable(t))
	}
	return out
}

// checkHostKeys requires every Go-bound host key the image names to be
// answered by one of the runner's tables, so a runner built without the
// project's bindings says so before the program runs.
func checkHostKeys(keys []string, tables []HostTable) error {
	if len(keys) == 0 {
		return nil
	}
	have := map[string]bool{}
	for _, t := range tables {
		funcs, err := t(&hostadapt.Env{})
		if err != nil {
			return fmt.Errorf("binding a host table: %w", err)
		}
		for k := range funcs {
			have[k] = true
		}
	}
	for _, k := range keys {
		if !have[k] {
			return fmt.Errorf("this nomi-vm has no Go binding for %s; the program was built for a nomi-vm "+
				"that links its project's Go bindings", k)
		}
	}
	return nil
}

// WriteBlocked prints the report for a program the VM cannot run for a
// reason that has no source diagnostic (a Go binding a runner lacks, a
// machine limit): one `BLOCKED <label> <reason>` line per reason, then a
// closing line. A body the compiler could not lower is reported by
// vmhost.Blocked.Write as `nomi check` reports it, before anything is built.
func WriteBlocked(w io.Writer, label string, reasons []string) {
	for _, reason := range reasons {
		fmt.Fprintf(w, "BLOCKED %s %s\n", label, reason)
	}
	fmt.Fprintln(w, "the VM cannot run this program")
}

// WriteFailure renders a program's failure as `nomi run` prints it:
// diagnostics with their source lines, an assertion failure with its
// operands and stages, anything else as its text.
func WriteFailure(w io.Writer, err error) {
	var exit *vm.Exit
	if errors.As(err, &exit) {
		// `Err(#Exit{...})` from main: its message alone, and nothing when
		// it is empty.
		if exit.Message != "" {
			fmt.Fprintln(w, exit.Message)
		}
		return
	}
	var rd rt.Renderer
	if errors.As(err, &rd) {
		rd.Render(w)
		return
	}
	var assertionErr *rt.AssertionFailure
	if errors.As(err, &assertionErr) {
		rt.WriteAssertionFailure(w, assertionErr)
		return
	}
	fmt.Fprintln(w, err)
}
