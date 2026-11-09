// Package stdprocess answers std/process_host's crossings: starting a
// program, its streams, waiting for it and killing it, either on the
// operating system (os.go) or, inside `process_testing.run`, on the
// recording fake the frame carries (fake.go). std/process and
// std/process_testing are lowered with each program and cross through
// std/process_host, whose functions carry the `processes` class.
//
// One function, Start, takes program, arguments, environment, directory and
// a mode per stream: null, the program's own, captured in memory, or piped.
// `process.output` captures stdout and stderr; `process.run` sends them to
// the program's own output (through `io.capture` when in force);
// `process.spawn` pipes all three as os.Pipe files the child holds
// directly, so its readers outlive the wait. Nothing is run through a
// shell: the program is looked up on PATH when its name has no separator,
// and its arguments are passed as they are.
//
// `Child.wait` closes piped input and waits once. `Child.kill`, which the
// caller defers, is `process_host.kill`: it kills and reaps a running child
// and does nothing after a wait, so a function that returns, traps or is
// cancelled leaves no process. A child starts under its frame's context, so
// cancellation or a passed deadline kills it and the wait unwinds. A
// relative `dir` resolves against the program's working directory, which
// `nomi test` sets to the module root without changing the process's.
//
// # The fake
//
// `process_testing.run` installs a recording fake on the frame
// (rt.EnterProcesses, through `process_host.run_on`, a VM intrinsic beside
// `fs_host.run_on`), inherited by tasks. Under it Start records the call and
// answers from the first matching `Answer`, never the operating system.
// TestOSAndFakeAgree runs one program against the test binary as a helper
// and against the fake, and requires the same transcript.
//
// js/wasm and wasip1 lack the `processes` class (vmhost.TargetLacks); built
// anyway, Start returns `Unsupported` (unsupported.go).
package stdprocess
