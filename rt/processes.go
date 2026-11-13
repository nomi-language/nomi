package rt

// The processes std/process starts, as slots rt carries and never opens.
//
// rt starts no process (internal/stdprocess does), so a running child and
// the fake `std::process::testing.run` installs are opaque values here, as the
// file system is (files.go). A machine's frames reach the operating
// system's processes unless a call replaced them: `std::process::testing.run`
// installs its fake for the dynamic extent of one call (EnterProcesses), so
// tasks spawned inside inherit it with the rest of the spawner's frame, and
// a nested call takes over for its own.

// ProcessFake is std/process/host's `Fake`: the recording fake
// `std::process::testing.run` answers std/process's calls with. It wraps
// internal/stdprocess's fake, which rt carries and does not name.
type ProcessFake struct{ fake any }

// NewProcessFake wraps fake as a value.
func NewProcessFake(fake any) ProcessFake { return ProcessFake{fake: fake} }

// Fake is the fake the value wraps.
func (p ProcessFake) Fake() any { return p.fake }

// EnterProcesses returns the frame a call runs on with fake answering its
// std/process calls: a child of parent that carries it, and every frame and
// task derived from it does too.
func EnterProcesses(parent *Frame, fake ProcessFake) *Frame {
	child := *parent
	child.processes = fake
	return &child
}

// ProcessesOf is the fake the frame's std/process calls reach (the
// innermost EnterProcesses), or nil for the operating system's processes.
func ProcessesOf(fr *Frame) any {
	if fr == nil {
		return nil
	}
	if p, ok := fr.processes.(ProcessFake); ok {
		return p.fake
	}
	return nil
}

// ChildProcess is std/process/host's `Child`: a process std/process
// started, or the fake's stand-in for one. It wraps internal/stdprocess's
// child, which rt carries and does not name.
type ChildProcess struct{ child any }

// NewChildProcess wraps child as a value.
func NewChildProcess(child any) ChildProcess { return ChildProcess{child: child} }

// Child is the child the value wraps.
func (c ChildProcess) Child() any { return c.child }
