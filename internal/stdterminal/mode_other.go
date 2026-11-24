//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd || windows)

package stdterminal

import "errors"

// state is nothing: this build reads and sets no terminal modes. A browser
// and a WASI module have no terminal (rt.StandardFile, golang.org/x/term),
// so std::terminal answers NotATerminal before it asks; on another system
// whose terminals x/term recognizes, a mode change is Unsupported.
type state struct{}

var errNoModes = errors.New("this build has no terminal modes")

func getState(uintptr) (state, error) { return state{}, errNoModes }

func setState(uintptr, state) error { return errNoModes }

func rawState(s state, _, _ bool) state { return s }

func noEchoState(s state) state { return s }
