package vmhost

import (
	"runtime/debug"

	"github.com/nomi-language/nomi/internal/frontend"
)

// CheckLowering is the lowering half of Check for the file at path whose text
// is src, as an editor holds it: the program is lowered as `nomi run` lowers
// it, and nothing runs. unsupported is what Program.Unsupported reports (nil
// when everything the program reaches lowers). err is the load's own failure:
// the front end's errors, which an editor reports from its own analysis, or
// an *InternalError when the compiler panicked. A stdlib file is not a
// program and answers nil, nil.
func CheckLowering(path, src string, opts ...Option) (unsupported, err error) {
	if _, _, ok := frontend.StdlibFile(path); ok {
		return nil, nil
	}
	p, err := LoadFileSource(path, src, opts...)
	if err != nil {
		return nil, err
	}
	defer func() {
		// Locating a blocker walks the IR and the AST; a panic there is a
		// compiler bug too, and a long-lived host keeps running.
		if r := recover(); r != nil {
			unsupported, err = nil, &InternalError{Panic: r, Stack: debug.Stack()}
		}
	}()
	if u := p.Unsupported(); u != nil {
		return u, nil
	}
	return nil, nil
}
