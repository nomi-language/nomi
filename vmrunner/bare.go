package vmrunner

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/nomi-language/nomi/internal/buildversion"
	"github.com/nomi-language/nomi/internal/ir"
)

// A runner nothing was appended to (nomi-vm) runs a compiled program, a
// .nomic file (imagefile.go), it is handed: by path, or framed at the start
// of stdin for a host with no filesystem, such as a browser's WASI shim.

// bareUsage is what a runner with no program prints for -h and for a
// mistake.
const bareUsage = `usage: nomi-vm <program.nomic> [args...]
       nomi-vm [-] [args...] < <program.nomic, then the program's input>
       nomi-vm --version

nomi-vm runs a compiled program (.nomic) that ` + "`nomi build --no-vm`" + ` wrote.
Given no path, or -, it reads the program from the start of stdin, and the
rest of stdin is the program's input. The arguments after the path (or -)
are the program's own.
`

// noOwnImage is a js/wasm runner that could not read its own module, so it
// cannot tell whether `nomi build` added a program to it. It runs as a bare
// runner, and says why when that finds no program either. (A wasip1 runner
// reads its program from its own memory and always can tell.)
type noOwnImage struct {
	path string
	err  error
}

func (e *noOwnImage) Error() string {
	if e.path == "" {
		return fmt.Sprintf("it found no module file to look in for a program `nomi build` added: %v", e.err)
	}
	return fmt.Sprintf("it could not read its own module %q to look for a program `nomi build` added (%v). "+
		"A loader must set globalThis.nomiImage, or the module must be readable by its own path, as with `node wasm_exec_node.js %s`",
		e.path, e.err, e.path)
}

func (e *noOwnImage) Is(target error) bool { return target == ErrNoImage }

// RunBare is a runner with no program of its own: it reads an image file
// from the path args names, or from stdin, and runs it with the arguments
// that follow, as Run does. ownErr is why the runner found no program of its
// own (ErrNoImage, or a js/wasm module's noOwnImage), which a failure
// mentions. It answers the exit status: Run's, 0 for -h and --version, 1
// for a mistake or an image this runner cannot run.
func RunBare(args []string, stdout, stderr io.Writer, stdin io.Reader, handleSignals bool, ownErr error, opts ...Option) int {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "nomi-vm: "+format+"\n", a...)
		return 1
	}
	// noProgram is a runner handed nothing it can run. A js/wasm module
	// that could not read itself may be one `nomi build` wrote, run without
	// access to its own file, so it says so.
	noProgram := func(why string) int {
		fmt.Fprintf(stderr, "nomi-vm: %s\n%s", why, bareUsage)
		var own *noOwnImage
		if errors.As(ownErr, &own) {
			fmt.Fprintf(stderr, "(It also looked for a program of its own: %v.)\n", own)
		}
		return 1
	}
	fromStdin := true
	path := ""
	if len(args) > 0 {
		switch arg := args[0]; {
		case arg == "-h" || arg == "--help":
			fmt.Fprint(stdout, bareUsage)
			return 0
		case arg == "--version":
			fmt.Fprintf(stdout, "nomi-vm %s (program format %d)\n", buildversion.Current(), ir.FormatVersion)
			return 0
		case arg == "-":
			args = args[1:]
		case strings.HasPrefix(arg, "-"):
			return fail("unknown flag %q\n%s", arg, bareUsage)
		default:
			fromStdin = false
			path = arg
			args = args[1:]
		}
	} else if isTerminal(stdin) {
		return noProgram("no program to run")
	}
	var image []byte
	var err error
	if fromStdin {
		var f ImageFile
		if f, err = ReadImageFile(stdin, "stdin"); err == nil {
			err = f.check("stdin", cfg.compiler != nil)
			image = f.Image
		}
	} else {
		image, err = LoadImageFile(path, opts...)
	}
	if err != nil {
		if fromStdin && errors.Is(err, errNotImageFile) {
			return noProgram(err.Error())
		}
		return fail("%v", err)
	}
	return Run(image, stdout, stderr, stdin, args, handleSignals, opts...)
}

// LoadImageFile reads the image file at path and answers its image, or why a
// runner with opts cannot run it (ImageFile's check).
func LoadImageFile(path string, opts ...Option) ([]byte, error) {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	f, err := ReadImageFile(file, path)
	if err != nil {
		return nil, err
	}
	if err := f.check(path, cfg.compiler != nil); err != nil {
		return nil, err
	}
	return f.Image, nil
}

// isTerminal reports whether r is a terminal, where waiting for a framed
// image would hang with no prompt.
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
