// Package stdcompilerrun is `compiler.run` and `compiler.run_file`: run a
// Nomi source and capture its stdout.
//
// # The engine is the VM, installed rather than imported
//
// The front end is here (stdcompiler.RunFrontEnd), so a parse or analysis
// failure reads the same as `compiler.check`'s front end would report it.
// Running what the front end accepted is an Engine's job,
// and nomi/vmhost installs the VM as the engine when it is linked.
//
// It is installed rather than imported because vmhost's `compiler.run` host
// imports this package, so an import of vmhost here would be a cycle. Nothing
// else calls an engine but that host, which runs in a process that linked
// vmhost, so the installation is always present where a Nomi program can
// reach it.
//
// # Unrunnable is not a failure
//
// A program the engine cannot run at all (a function the VM does not retain)
// answers *Unrunnable. The VM's `compiler.run` host returns that as a machine
// limit, so the CALLING case is reported blocked with the nested program's
// reasons rather than failing with them as its `Err` payload.
package stdcompilerrun

import (
	"sync"

	"github.com/nomi-language/nomi/internal/stdcompiler"
)

// Engine runs a source RunFrontEnd accepted and answers its stdout. A fault
// or a failed assertion is the error; partial output is discarded. hostEnv
// overrides process environment entries in the Startup a boot receives.
type Engine func(source, projectRoot string, virtualFiles, hostEnv map[string]string) (string, error)

// Unrunnable is an engine's answer for a program it could not run at all.
type Unrunnable struct {
	Reason string
}

func (u *Unrunnable) Error() string { return u.Reason }

var (
	engineMu sync.RWMutex
	engine   Engine
)

// SetEngine installs the engine every run goes through. nomi/vmhost calls it
// from its package initializer.
func SetEngine(e Engine) {
	engineMu.Lock()
	engine = e
	engineMu.Unlock()
}

// RunSource is `compiler.run`'s pipeline: the shared front end, then the
// installed engine.
func RunSource(source, projectRoot string, virtualFiles, hostEnv map[string]string) (string, error) {
	if _, _, err := stdcompiler.RunFrontEnd(source, projectRoot, virtualFiles); err != nil {
		return "", err
	}
	engineMu.RLock()
	e := engine
	engineMu.RUnlock()
	if e == nil {
		return "", &Unrunnable{Reason: "compiler.run: no engine is installed (nomi/vmhost installs the VM)"}
	}
	return e(source, projectRoot, virtualFiles, hostEnv)
}

// RunFileSource is `compiler.run_file`'s pipeline: RunSource with the entry
// file lookup, and its four rejections, in front.
func RunFileSource(entryPoint, projectRoot string, virtualFiles, hostEnv map[string]string) (string, error) {
	source, err := stdcompiler.EntryFileSource(entryPoint, projectRoot, virtualFiles)
	if err != nil {
		return "", err
	}
	return RunSource(source, projectRoot, virtualFiles, hostEnv)
}
