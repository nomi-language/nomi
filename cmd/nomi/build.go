package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nomi-language/nomi/internal/ffirun"
	"github.com/nomi-language/nomi/vmhost"
	"github.com/nomi-language/nomi/vmrunner"
)

// `nomi build <entry.nomi> -o <out>`: a single executable, the way `deno
// compile` and `bun build --compile` make one. It lowers the program as `nomi
// run` does, encodes its linked IR (ir.EncodeImage), and writes a cached
// runner binary with the image and a trailer appended (nomi/vmrunner). The
// binary runs at `nomi run` speed: it skips the front end and the lowering at
// startup and runs the same VM over the same IR. It is not native code.
//
// A program the front end rejects, or one the VM would block, is refused with
// the text `nomi run` prints, and nothing is written.

const buildUsage = "usage: nomi build <file.nomi> [-o <out>] [--target <goos>/<goarch>]"

// errBuildRefused is a build that already reported why on stderr.
var errBuildRefused = errors.New("build refused")

type buildArgs struct {
	entry  string
	out    string
	target ffirun.Target
}

func parseBuildArgs(args []string) (buildArgs, error) {
	var b buildArgs
	var goos, goarch string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("nomi build: %s needs a value\n%s", arg, buildUsage)
			}
			i++
			return args[i], nil
		}
		switch {
		case arg == "-o":
			v, err := value()
			if err != nil {
				return b, err
			}
			b.out = v
		case strings.HasPrefix(arg, "-o="):
			b.out = strings.TrimPrefix(arg, "-o=")
		case arg == "--target":
			v, err := value()
			if err != nil {
				return b, err
			}
			var ok bool
			if goos, goarch, ok = strings.Cut(v, "/"); !ok || goos == "" || goarch == "" {
				return b, fmt.Errorf("nomi build: --target takes <goos>/<goarch>, such as linux/amd64; got %q", v)
			}
		case isFlag(arg):
			return b, unknownFlag("nomi build", arg, buildUsage)
		case b.entry == "":
			b.entry = arg
		default:
			return b, fmt.Errorf("nomi build: unexpected argument %q\n%s", arg, buildUsage)
		}
	}
	if b.entry == "" {
		return b, errors.New(buildUsage)
	}
	b.target = ffirun.ResolveTarget(goos, goarch)
	if b.out == "" {
		// go build's default: the entry's base name, in the working
		// directory.
		b.out = strings.TrimSuffix(filepath.Base(b.entry), ".nomi")
		if b.target.GOOS == "windows" {
			b.out += ".exe"
		}
	}
	return b, nil
}

// runBuild is `nomi build`.
func runBuild(args []string) error {
	b, err := parseBuildArgs(args)
	if err != nil {
		return err
	}
	return build(b, os.Stderr)
}

func build(b buildArgs, stderr io.Writer) error {
	if err := entryFileError("build", b.entry); err != nil {
		return err
	}
	absPath, err := filepath.Abs(b.entry)
	if err != nil {
		return err
	}
	if strings.HasSuffix(filepath.Base(absPath), "_test.nomi") {
		return fmt.Errorf("nomi build: %s is a test file; use `nomi test %s`",
			vmhost.DisplayPath(absPath), vmhost.DisplayPath(absPath))
	}
	if isStdlibTestPath(absPath) {
		return fmt.Errorf("nomi build: %s is a stdlib module, not a program", vmhost.DisplayPath(absPath))
	}
	res, err := ffirun.Prepare(absPath)
	if err != nil {
		return fmt.Errorf("nomi build: %w", ffirun.ForBuild(err))
	}
	var image []byte
	var runner string
	if res.FastPath {
		p, err := vmhost.Load(absPath)
		if err != nil {
			vmhost.WriteFailure(stderr, err)
			return errBuildRefused
		}
		img, usesCompiler, err := p.BuildImage()
		if err != nil {
			if blocked, ok := vmhost.IsBlocked(err); ok {
				blocked.Write(stderr, vmhost.DisplayPath(absPath))
				return errBuildRefused
			}
			return fmt.Errorf("nomi build: %w", err)
		}
		image = img
		if runner, err = ffirun.Runner(b.target, usesCompiler, stderr); err != nil {
			return fmt.Errorf("nomi build: %w", err)
		}
	} else {
		if err := ffirun.CheckProjectBuild(); err != nil {
			return fmt.Errorf("nomi build: %w", err)
		}
		dir, err := os.MkdirTemp("", "nomi-build-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		imagePath := filepath.Join(dir, "image")
		usesCompiler, report, ok, err := ffirun.BuildImageVM(res, absPath, imagePath)
		if err != nil {
			return fmt.Errorf("nomi build: %w", err)
		}
		if !ok {
			fmt.Fprint(stderr, report)
			return errBuildRefused
		}
		if image, err = os.ReadFile(imagePath); err != nil {
			return err
		}
		if runner, err = ffirun.ProjectRunner(res, b.target, usesCompiler); err != nil {
			return fmt.Errorf("nomi build: %w", err)
		}
	}
	if err := writeExecutable(b.out, runner, image); err != nil {
		return fmt.Errorf("nomi build: %w", err)
	}
	return nil
}

// writeExecutable writes runner's bytes, then image, then the trailer to
// out, through a temporary file beside it, so a failed build leaves nothing
// and an existing out is replaced whole.
func writeExecutable(out, runner string, image []byte) error {
	src, err := os.Open(runner)
	if err != nil {
		return err
	}
	defer src.Close()
	absOut, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(absOut), "."+filepath.Base(absOut)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()
	n, err := io.Copy(tmp, src)
	if err != nil {
		return err
	}
	if _, err := tmp.Write(image); err != nil {
		return err
	}
	if _, err := tmp.Write(vmrunner.Trailer(n, int64(len(image)))); err != nil {
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, absOut); err != nil {
		return err
	}
	ok = true
	return nil
}
