// Package dap is `nomi dap`: a Debug Adapter Protocol server over stdio that
// runs a Nomi program in process on a debug machine (internal/vm package
// doc, "Debug machines") and lets an editor set breakpoints, step, step
// back, and read the stack and locals.
//
// A session is one launch. It loads the program with vmhost: `mode: "run"`
// runs `main` of a file (or of a project's single entry point), `mode:
// "test"` runs a file's tests, or the one test `line` selects as `nomi test
// --line` does. The program's output becomes `output` events, or, with
// `console: "integratedTerminal"` and a client that supports
// `runInTerminal`, goes to a terminal the editor opens: it runs the hidden
// `nomi dap-terminal <socket>` ([RunTerminal]), which connects back over a
// Unix socket and carries the program's standard input and output. The Zed
// and VS Code extensions and the Neovim plugin start the server;
// `nomi-lsp`'s Debug lenses name the commands `nomi.debugTest` and
// `nomi.debugMain`.
//
// The message types are github.com/google/go-dap's. protocol.go adds the
// parts its v0.12.0 lacks.
//
// # What the debugger reads
//
// The IR builder records each function's variables (name, temporary, Nomi
// type, scope) and statement marks (statements, arms, loop iterations, pipe
// stages), and the image carries both. The machine stops at the marks; the
// stack, scopes and variables are read from the records.
//
// The variables tree is `rt.Inspect`, built from a value's representation
// with no Nomi code run. It names a struct's fields by name and a tuple's
// items by the ordinals that read them (`first` through `twentieth`), so a
// watch or hover that is a variable or a path of names on one (`p.x`,
// `nested.second.first`) follows those children with nothing compiled
// (readPath, evaluate.go).
//
// # Evaluation
//
// Any other watch, hover or debug-console text, `pair.0` included, is
// checked and lowered by vmhost as a function appended to the frame's file,
// whose parameters are the frame's variables it names at their recorded
// types, and accepted or rejected as Nomi. It runs on a view of the stopped
// machine that links its lowering without changing the machine. An
// expression that can reach an effect is refused unless it ends with
// `// allow effects`, and the view has no debugger, so such an expression
// runs outside the step-back recording. evaluate.go states the rules.
//
// # Step back
//
// Step back and reverse continue replay the program rather than restore a
// saved state (internal/vm's debugtravel.go). A debug machine counts the
// statements it runs and records each crossing out of the machine whose
// result a replay cannot compute (input, the clock, randomness, timers,
// files, std/compiler's hosts) with its result and statement. Going back
// replays the program from its start on a fresh machine, answering each
// crossing from the recording and checking that it is the same call at the
// same statement. There are no checkpoints, so going back to statement T
// runs every statement before T again. A read that an `io.capture` or
// `io.replay` answers runs again instead of being recorded, so the capture's
// transcript replays too. A replay cannot reproduce how tasks interleave, so
// step back turns off at the first task, `concurrent`, supervisor or
// channel.
package dap
