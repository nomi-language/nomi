package vmrunner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// A runner that keeps one copy of the VM and hands it each program as data
// (the wasip1 reactor's nomi_load) loads a whole .nomic file, header
// included, from memory: LoadProgram.

// errNothingToRun is a compiled program with neither a `fn main` nor an
// `export fn`.
var errNothingToRun = errors.New("the compiled program has no `fn main` and no `export fn`, so there is nothing to run or call")

// DecodeImageFile reads data, the whole of a .nomic file, and answers its
// image, or why a runner with opts cannot run it: a damaged header, a
// program format or a flag this runner does not read, a crossing into
// std/compiler it does not link (ImageFile's check), or bytes after the
// image. name says where data came from in errors.
func DecodeImageFile(data []byte, name string, opts ...Option) ([]byte, error) {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}
	r := bytes.NewReader(data)
	f, err := ReadImageFile(r, name)
	if err != nil {
		return nil, err
	}
	if r.Len() > 0 {
		return nil, fmt.Errorf("%s has %d bytes after its program; a .nomic file ends with its program", name, r.Len())
	}
	if err := f.check(name, cfg.compiler != nil); err != nil {
		return nil, err
	}
	return f.Image, nil
}

// LoadProgram boots the compiled program data holds, a whole .nomic file,
// as Boot does: ready for calls to its exported functions, with its `main`
// run when the host asks (Instance.Main). It refuses what DecodeImageFile
// refuses, and a program with neither a `fn main` nor an `export fn`.
func LoadProgram(data []byte, stdout, stderr io.Writer, stdin io.Reader, args []string, opts ...Option) (*Instance, error) {
	image, err := DecodeImageFile(data, "the loaded program", opts...)
	if err != nil {
		return nil, err
	}
	return boot(image, stdout, stderr, stdin, args, opts)
}

// ExportsResult is the `{"ok": [<signature>, ...]}` answer listing the
// program's exported functions, each as Export.Signature writes it.
func (in *Instance) ExportsResult() []byte {
	sigs := make([]string, len(in.exports))
	for i, x := range in.exports {
		sigs[i] = x.Signature()
	}
	text, _ := json.Marshal(map[string][]string{"ok": sigs})
	return text
}
