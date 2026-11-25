// Package stdsystem answers std/system/host's crossings: facts about the
// machine and the process that std::system turns into its values. Each is
// read when it is called, from Go's os, os/user, runtime and
// runtime/metrics packages, and `nomi_release` from the binary's build
// info (internal/buildversion's Release): the tag a release's binaries are
// built at, `nomi` and every runner alike, and none for a build from
// source.
//
// The directory functions read the process's own environment ($HOME,
// $XDG_CACHE_HOME, $XDG_CONFIG_HOME, $XDG_DATA_HOME, %LocalAppData%,
// %AppData%), as the operating system gave it when the process started,
// which is the environment `Startup.env` holds; a program does not hand
// them its Startup. `data_dir` follows the rules os.UserCacheDir and
// os.UserConfigDir follow, since Go has no function for it.
//
// A failure is the parts of std::system's `Error`: `["Unsupported",
// operation, target]` for a target that has no such thing, and
// `["Unavailable", operation, reason]` when the system could not answer.
// A js/wasm or wasip1 module (system_wasm.go) answers Unsupported for the
// hostname, the executable, the directories and the user, and 1 for the
// CPU count; its OS, architecture and memory are what Go reports there.
// No row has an effect class, so `nomi build` refuses no target for them.
//
// `memory` reads runtime/metrics, which does not stop the world: the heap's
// values, live or not yet swept, and every byte the runtime has obtained
// from the operating system. Go reports no total of the machine's memory
// that holds on every system, so std::system has none. `runtime` reads
// the process's task and supervisor counts from rt (rt.RuntimeCounts,
// which both task spawns keep with an atomic add) and the collector's
// cycles and pause histogram from runtime/metrics, stopping nothing.
package stdsystem
