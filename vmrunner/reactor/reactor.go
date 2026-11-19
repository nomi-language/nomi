// Package reactor is the WASI reactor's call ABI: a wasip1 main package
// that imports it and is linked with -buildmode=c-shared exports
// `_initialize` and, from this package (//go:wasmexport),
//
//	nomi_alloc(len: i32) -> i32
//	nomi_call(name_ptr, name_len, args_ptr, args_len: i32) -> i64
//	nomi_call_with(name_ptr, name_len, args_ptr, args_len, opts_ptr, opts_len: i32) -> i64
//	nomi_free(ptr, len: i32)
//	nomi_load(ptr, len: i32) -> i64
//	nomi_load_with(ptr, len, opts_ptr, opts_len: i32) -> i64
//	nomi_main() -> i32
//
// over the program `nomi build` embedded in the module's memory, or one a
// runner hands nomi_load. cmd/nomi-vm-reactor is such a main package, and
// so is the runner `nomi build --target wasip1/wasm` generates for a
// project with Go bindings and an `export fn` (internal/ffirun), which
// passes its host table to Use. On any other target the package declares
// Use and reads the options, and nothing else.
//
// # Options
//
// nomi_load_with and nomi_call_with take their options as a JSON object
// (parseOptions); an empty one is `{}`. A key the export does not take is
// an error naming it, so more keys can come without an old reactor
// silently ignoring them.
//
//   - "std" (nomi_load_with): an array of std names (`std::io`,
//     `std::startup`), held as vmrunner.WithCapabilities holds a program:
//     the load refuses a program that reaches an outside call other than a
//     host function that the names do not allow, from main, the boot or
//     any exported function, before it runs anything. An empty array
//     allows nothing; without the key such calls are not held.
//   - "host" (nomi_load_with): an array of the keys of the host functions
//     linked into the runner that the program may call, each as its table
//     registers it, without `host:`. It holds host functions as "std"
//     holds the rest: with "std" given and "host" not, no host function
//     is allowed; "host" alone holds only host functions.
//   - "timeout_ms" (both): a whole number of milliseconds above zero. On
//     nomi_load_with it bounds the boot and is the default bound of every
//     later nomi_call, nomi_call_with and nomi_main; on nomi_call_with it
//     bounds that call, in place of the default. A call past its bound is
//     stopped (vmrunner.Instance.Call) and answers an error saying so. The
//     program stays loaded and takes more calls.
package reactor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nomi-language/nomi/vmrunner"
)

// options are the host options the program boots with.
var options []vmrunner.Option

// Use adds host options to the ones the reactor boots its program with. A
// main package calls it from an init function: `_initialize` runs package
// initialization and nothing else, and the program boots on the first call
// after it.
func Use(opts ...vmrunner.Option) {
	options = append(options, opts...)
}

// The option keys, and the exports that take them.
const (
	keyStd     = "std"
	keyHost    = "host"
	keyTimeout = "timeout_ms"
)

// stdVia and hostVia are how a refusal names the lists nomi_load_with's
// "std" and "host" hold.
const (
	stdVia  = `nomi_load_with's "std"`
	hostVia = `nomi_load_with's "host"`
)

// callOptions are the options a host passes nomi_load_with or
// nomi_call_with.
type callOptions struct {
	// std is "std"'s names and host "host"'s keys, each nil when its key
	// was not given.
	std  []string
	host []string
	// timeout is "timeout_ms", or 0 when it was not given.
	timeout time.Duration
}

// parseOptions reads data, the options export was handed, a JSON object
// whose keys are among keys. Empty data is no options.
func parseOptions(export string, data []byte, keys ...string) (callOptions, error) {
	var opts callOptions
	if len(bytes.TrimSpace(data)) == 0 {
		return opts, nil
	}
	var raw map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil || raw == nil || dec.More() {
		return opts, fmt.Errorf("%s: the options must be one JSON object, such as {%s}", export, example(keys))
	}
	names := make([]string, 0, len(raw))
	for k := range raw {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		v := raw[k]
		switch {
		case !slices.Contains(keys, k):
			return opts, fmt.Errorf("%s has no option %q; its options are %s", export, k, quoted(keys))
		case k == keyStd:
			var names []string
			if err := json.Unmarshal(v, &names); err != nil || names == nil {
				return opts, fmt.Errorf(`%s: %q must be an array of std names, such as ["std::io"]; [] allows nothing`, export, k)
			}
			for _, name := range names {
				if key, ok := strings.CutPrefix(name, "host:"); ok {
					return opts, fmt.Errorf(`%s: %q holds std names, and %s is a host function; list its key, %q, in %q`, export, k, name, key, keyHost)
				}
			}
			opts.std = names
		case k == keyHost:
			var keys []string
			if err := json.Unmarshal(v, &keys); err != nil || keys == nil {
				return opts, fmt.Errorf(`%s: %q must be an array of host function keys, such as ["lookup"]; [] allows none`, export, k)
			}
			for _, key := range keys {
				if bare, ok := strings.CutPrefix(key, "host:"); ok {
					return opts, fmt.Errorf(`%s: %q lists keys as a host table registers them: write %q, not %q`, export, k, bare, key)
				}
			}
			opts.host = keys
		case k == keyTimeout:
			ms, err := strconv.ParseInt(string(v), 10, 64)
			if err != nil || ms <= 0 || ms > math.MaxInt64/int64(time.Millisecond) {
				return opts, fmt.Errorf("%s: %q must be a whole number of milliseconds above 0, such as 500, not %s", export, k, v)
			}
			opts.timeout = time.Duration(ms) * time.Millisecond
		}
	}
	return opts, nil
}

// stopped is the error of what (`the call to `add“, `the load`, `main`)
// when its bound of timeout stopped it.
func stopped(what string, timeout time.Duration) error {
	return fmt.Errorf("%s was stopped after %d ms, its timeout_ms", what, timeout.Milliseconds())
}

// quoted is keys as a reader lists them: "a", "a" and "b", "a", "b" and "c".
func quoted(keys []string) string {
	q := make([]string, len(keys))
	for i, k := range keys {
		q[i] = strconv.Quote(k)
	}
	if len(q) == 1 {
		return q[0]
	}
	return strings.Join(q[:len(q)-1], ", ") + " and " + q[len(q)-1]
}

// example is an options object with each of keys.
func example(keys []string) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		switch k {
		case keyStd:
			parts[i] = `"std": ["std::io"]`
		case keyHost:
			parts[i] = `"host": ["lookup"]`
		case keyTimeout:
			parts[i] = `"timeout_ms": 500`
		}
	}
	return strings.Join(parts, ", ")
}
