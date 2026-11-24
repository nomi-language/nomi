// Package jsprocess gives a js/wasm build the arguments and environment its
// host passes beside the module.
//
// Go's wasm_exec.js writes go.argv and go.env into 8 KiB of the module's
// memory and refuses to start a module whose arguments and environment do
// not fit ("total length of command line and environment variables exceeds
// limit"). A shell's environment often passes that. So a host that runs a
// Nomi program sets two globals before it runs the instance:
//
//	globalThis.nomiArgs = ["first", "two words"]     // the arguments after argv[0]
//	globalThis.nomiEnv = { HOME: "/home/ada", ... }  // the environment, name to value
//
// It passes in go.argv only argv[0], and in go.env only the variables the Go
// runtime reads as it starts, before any package can set them: GODEBUG,
// GOGC, GOMEMLIMIT and TMPDIR, when they are set. nomi-node.mjs (the Node
// loader `nomi build --target js/wasm` writes) and nomi.js (the page loader
// `nomi build --web` writes) both do this, and so the only bound on a
// program's arguments and environment is memory. The 8 KiB holds argv[0] and
// those four variables.
//
// When the package initializes it sets each of nomiEnv's variables in the
// process's environment, over what go.env gave, and makes os.Args argv[0]
// followed by nomiArgs. Every reader of the environment or the arguments
// (os.Environ, os.Getenv and os.Args, so `Startup`, std's environment
// functions and the runtime's own settings such as NOMI_COLOR) then sees what
// the host passed. A host that sets neither global leaves both as go.argv
// and go.env gave them, which is what the tour's run worker does: it passes
// a program no environment of its own. A name the process environment
// cannot hold (empty, or holding "=" or a NUL byte) is skipped.
//
// rt imports the package in a js/wasm build (rt/jsprocess_js.go), so it
// initializes before rt's own variables, one of which reads NOMI_HASH_SEED.
// It has code only for js/wasm (process_js.go).
package jsprocess
