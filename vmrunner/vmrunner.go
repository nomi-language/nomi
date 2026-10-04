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
// The executable is the runner's bytes, then the image, then a fixed
// 24-byte trailer: the image's offset and length as little-endian uint64s
// and the magic "NOMIEXE\x01". A built binary runs at `nomi run` speed: it
// skips the front end and the lowering at startup and runs the same VM over
// the same IR. It is not native code.
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
	"github.com/nomi-language/nomi/internal/ir"
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
	"it is the runner `nomi build` appends one to")

// ReadImage reads the image appended to the executable at path.
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
func Main(opts ...Option) {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nomi: locating this executable: %v\n", err)
		os.Exit(1)
	}
	image, err := ReadImage(exe)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nomi: %v\n", err)
		os.Exit(1)
	}
	os.Exit(Run(image, os.Stdout, os.Stderr, os.Stdin, os.Args[1:], true, opts...))
}

// Run decodes image and runs its `main` as `nomi run` does, and answers the
// exit status `nomi run` would exit with. handleSignals installs the
// process-wide signal handler, which only a caller that owns the process may
// do.
func Run(image []byte, stdout, stderr io.Writer, stdin io.Reader, args []string, handleSignals bool, opts ...Option) int {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}
	im, err := ir.DecodeImage(image)
	if err != nil {
		fmt.Fprintf(stderr, "nomi: the appended program cannot be read: %v\n", err)
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
	if !im.HasMain {
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
	m := vm.NewProgram(entry, im.Modules, stdout).WithHosts(hosts...).WithErrorOutput(stderr)
	if in != nil {
		m = m.WithInput(in)
	}
	var boots []*ir.Symbol
	if boot := entry.Boot(); boot != nil {
		boots = append(boots, boot)
	}
	// `nomi build` refused a program the VM would block, so this is a
	// runner that does not match its image (an FFI binding it lacks).
	if found := m.Unretained([]*ir.Func{mainFn}, boots); len(found) > 0 {
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
		return 1
	}
	return 0
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
		return fmt.Sprintf("[%s] crosses into Go and the VM has no binding for it", u.Name)
	case vm.NoImplementation:
		return fmt.Sprintf("[%s] dispatched, and no implementation was retained", u.Name)
	case vm.OnceNotRetained:
		return fmt.Sprintf("[%s] once initializer not retained", u.Name)
	}
	return fmt.Sprintf("[%s] not retained", u.Name)
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
			return fmt.Errorf("this runner has no Go binding for %s; the program was built for a runner "+
				"that links its project's Go bindings", k)
		}
	}
	return nil
}

// WriteBlocked prints the grep-friendly report `nomi run` prints for a
// program the VM cannot run: one `BLOCKED <label> <reason>` line per reason,
// then a closing line.
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
