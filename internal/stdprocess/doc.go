// Package stdprocess answers std/process/host's crossings: starting a
// program, its streams, waiting for it and killing it, either on the
// operating system (os.go) or, inside `std::process::testing.run`, on the
// recording fake the frame carries (fake.go). std/process and
// std/process/testing are lowered with each program and cross through
// std/process/host, whose functions carry the `processes` class.
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
// caller defers, is `std::process::host.kill`: it kills and reaps a running child
// and does nothing after a wait, so a function that returns, traps or is
// cancelled leaves no process. The kill is sent before the wait is taken,
// so it also ends a child another task is waiting for, and that wait reaps
// it; os.Process sends nothing to a process its wait has reaped. A child
// starts under its frame's context, so cancellation or a passed deadline
// kills it and the wait unwinds. A relative `dir` resolves against the
// program's working directory, which `nomi test` sets to the package root
// without changing the process's.
//
// # Signals and groups
//
// `signal` takes the POSIX name std/process's `Signal` displays as and
// sends it to the child, or with `group` to the process group the child
// leads (signal_unix.go: kill(-pid)). A Command's `new_group` makes the
// child a group leader (Setpgid on Unix, CREATE_NEW_PROCESS_GROUP on
// Windows). Windows sends only SIGKILL, to the child alone
// (signal_windows.go); every other signal, and every group signal, is
// Unsupported there. A signal to a child that has ended answers `Other`
// with code ESRCH, on Windows too, where Go answers a kill after the
// child's wait with EINVAL (signal_windows.go). `Child.stop` is Nomi in std/process over `signal`,
// `wait`, `kill` and a channel's `receive_within`: it sends SIGTERM, waits
// for the child on a task up to the grace, and past it kills, so it has no
// kill path of its own. `child_id` is the child's pid; `id` is this
// process's, and 1 on js/wasm and wasip1, which have none.
//
// The children are not tied to this process by the operating system. When
// the program is ended by SIGINT or SIGTERM (rt.InstallShutdownSignals) it
// drains its supervisors, which cancels their tasks and so kills the
// children those tasks started, and exits; a child started under another
// context outlives it unless the signal reached the child too.
//
// # The fake
//
// `std::process::testing.run` installs a recording fake on the frame
// (rt.EnterProcesses, through `std::process::host.run_on`, a VM intrinsic beside
// `std::fs::host.run_on`), inherited by tasks. Under it Start records the call and
// answers from the first matching `Answer`, never the operating system. A
// fake child has ended when it starts: its id is its call's position, from
// 1, and a signal to it answers ESRCH.
// TestOSAndFakeAgree runs one program against the test binary as a helper
// and against the fake, and requires the same transcript.
//
// js/wasm and wasip1 lack the `processes` class (vmhost.TargetLacks); built
// anyway, Start returns `Unsupported` (unsupported.go).
package stdprocess
