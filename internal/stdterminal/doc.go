// Package stdterminal is std/terminal/host and std/io's `stream_is_terminal`:
// the device facts about the terminal a program's standard streams are
// connected to, and the modes std::terminal changes on it.
//
// # Which streams are terminals
//
// rt knows the file behind each of a frame's standard streams
// (rt.StandardFile): the input's file, and the output and error writers'
// destinations through the lock a machine writes them with
// (rt.Destination). It answers none inside `io.capture` or a test case,
// whose streams are the capture's, and none for a stream that is not a
// file, as a browser's output and an embedding host's buffer are not. This
// package asks whether that file is a terminal and how big it is
// (isTerminal, size): through golang.org/x/term, except on Windows, where
// a console is a handle with a console mode and its size is its window's
// (mode_windows.go). A js/wasm or wasip1 build has no terminal: x/term
// answers no there, so std::terminal answers NotATerminal at run time, and
// one program builds for every target. `supports_color` asks the
// environment first, by the rule the runtime's own colour follows
// (rt.ColorChosen).
//
// # Modes
//
// Raw mode and a password read change standard input's terminal: termios
// through golang.org/x/sys/unix on Linux, macOS and the BSDs (the request
// numbers differ: ioctl_linux.go, ioctl_bsd.go), and the console input
// mode on Windows, where raw mode turns on virtual terminal input so keys
// arrive as the escape sequences a Unix terminal sends. Another system has
// no modes here, and a change is `Unsupported`.
//
// On Windows every call goes through the syscall package's kernel32
// procedures, never golang.org/x/sys/windows or x/term, which imports it:
// x/sys/windows imports net, and the lean runner links this package and
// none of net (cmd/nomi-vm's TestLeanVMImportsNoFamilyCode).
//
// Every change goes through one stack (modeStack), which holds the mode
// each change found. Leaving the newest change on a terminal restores that
// mode; leaving an older one first hands its mode to the change after it,
// so the last one left restores the terminal's first mode, however tasks
// interleave. While a change is held, a drain hook (rt.OnDrain) restores
// each terminal's first mode. The drain runs when a program ends, and when
// `nomi run` or a built program is interrupted (SIGINT, SIGTERM;
// rt.InstallShutdownSignals) before it exits, so a program killed inside
// raw mode or a password read leaves the shell's terminal as it found it.
// std::terminal releases raw mode with a deferred `RawMode.restore`, which
// runs when its block ends, traps or is cancelled; a password read restores
// echo with a Go defer around its read, which a cancellation unwinds
// through. SIGKILL and SIGQUIT end the process with no drain.
//
// # Reading
//
// Keys and passwords are read from the buffer `io.read_line` reads
// (rt.Input), never from the file directly, so nothing typed is read twice
// or lost between them, and a read waiting for a key is a safe point. A
// key's bytes are read one at a time (rt.StdinReadByte), each waiting at
// most the time std::terminal gives it; std/terminal.nomi decodes them.
//
// # Effects
//
// Each row is World Outside, with no class: it reads or changes the
// terminal outside the machine, and every target has it, answering
// NotATerminal where there is none, as `io.read_line` answers the end of
// the input where there is no input.
package stdterminal
