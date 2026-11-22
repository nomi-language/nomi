// Package families is the optional families of std's host functions: the
// adapters that call one Go package, which a runner may leave out, so a
// program that reaches none of them runs on a runner that links none of
// that package's code. It imports nothing, so the adapter generator
// (internal/hostgen/stdtable), the runner builds (internal/runners) and the
// generated adapters (internal/stdlibadapters, whose Families adds each
// family's crossings and whether a build links it) read the one table.
//
// The lean runner, nomi-vm-nonet, is built with every family's tag
// (LeanTags). A family is absent on a GOOS when no runner for it has the
// family, whatever its tags.
package families

import (
	"fmt"
	"slices"
)

// Family is one optional family.
type Family struct {
	// Key names the family's generated files: adapters_<key>_gen.go holds
	// its adapters, adapters_no<key>_gen.go the stubs a runner without it
	// answers with.
	Key string
	// What is the family as a message names it.
	What string
	// Package is the adapter package whose functions are the family's.
	Package string
	// Tag is the build tag that leaves the family out.
	Tag string
	// Absent is the GOOS values no runner has the family on.
	Absent []string
}

// All are the optional families, in the order a runner's tags name them:
// the network (net/http, crypto/tls and net's sockets), and WebAssembly
// modules (wazero, and the host's engine in a js runner), which no wasip1
// runner has: with wazero the wasip1
// reactor declares 17.3 MiB of initial memory, past the 16 MiB it is held
// to (cmd/nomi's TestBuildCommand_ReactorLoadsOneProgramPerInstance).
var All = []Family{
	{Key: "net", What: "the network", Package: "github.com/nomi-language/nomi/internal/stdhttp", Tag: "nomi_nonet"},
	{Key: "wasm", What: "WebAssembly modules", Package: "github.com/nomi-language/nomi/internal/stdwasm", Tag: "nomi_nowasm", Absent: []string{"wasip1"}},
}

// LeanTags is the build tags of the lean runner, which has no family.
func LeanTags() []string {
	tags := make([]string, len(All))
	for i, f := range All {
		tags[i] = f.Tag
	}
	return tags
}

// OnTarget refuses a program that reaches a family (by Key) that no runner
// for goos has.
func OnTarget(reached []string, goos, target string) error {
	for _, f := range All {
		if slices.Contains(reached, f.Key) && slices.Contains(f.Absent, goos) {
			return fmt.Errorf("this program uses %s, which no runner for %s has; build it for another target", f.What, target)
		}
	}
	return nil
}
