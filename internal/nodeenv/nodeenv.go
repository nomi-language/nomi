// Package nodeenv gives a test the environment to run Node with when Node
// runs a WebAssembly module.
//
// Go's js/wasm loader (wasm_exec_node.js, and go_js_wasm_exec, which `go
// test` runs a js/wasm test binary with) copies Node's whole environment,
// with the arguments, into a fixed area of the module's memory, and refuses
// to start when they total more than about 12 KB: "total length of command
// line and environment variables exceeds limit". A test that hands Node its
// caller's environment fails for a caller with a large one, so every test
// that runs a module under Node passes it Minimal's answer instead.
package nodeenv

import "strings"

// kept are the variables Node and the tools it starts need: the search path,
// the home and temporary directories, and Windows' system variables.
var kept = []string{"PATH", "HOME", "TMPDIR", "TMP", "TEMP", "USERPROFILE", "SYSTEMROOT", "LOCALAPPDATA", "APPDATA"}

// Minimal answers the entries of env that Node needs, every NOMI_* entry,
// and the entries named in also, in env's order. A later entry for a name
// replaces an earlier one, as exec does.
func Minimal(env []string, also ...string) []string {
	keep := func(name string) bool {
		if strings.HasPrefix(name, "NOMI_") {
			return true
		}
		for _, k := range kept {
			if strings.EqualFold(name, k) {
				return true
			}
		}
		for _, k := range also {
			if name == k {
				return true
			}
		}
		return false
	}
	var out []string
	index := map[string]int{}
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || !keep(name) {
			continue
		}
		if i, seen := index[name]; seen {
			out[i] = kv
			continue
		}
		index[name] = len(out)
		out = append(out, kv)
	}
	return out
}
