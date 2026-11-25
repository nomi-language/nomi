package stdlibbindings

import (
	"go/build"
	"slices"
	"testing"
)

// The rows, and so each host function's Class, are the same on every
// system: a class is a property of the function, not of how a platform
// carries it out. The walk `nomi build` checks a target with, and the
// stdlib reference marks a prompt by, reads Classes, so its answer does
// not depend on the machine that runs it. This holds the package to no
// file a build constraint chooses, compared across the systems Nomi builds
// for. A platform's difference belongs in the function a row names.
func TestTheRowsAreTheSameOnEverySystem(t *testing.T) {
	targets := [][2]string{
		{"darwin", "arm64"}, {"linux", "amd64"}, {"windows", "amd64"},
		{"freebsd", "amd64"}, {"js", "wasm"}, {"wasip1", "wasm"},
	}
	var first []string
	for _, target := range targets {
		ctx := build.Default
		ctx.GOOS, ctx.GOARCH, ctx.CgoEnabled = target[0], target[1], false
		pkg, err := ctx.ImportDir(".", 0)
		if err != nil {
			t.Fatalf("%s/%s: %v", target[0], target[1], err)
		}
		if first == nil {
			first = pkg.GoFiles
			continue
		}
		if !slices.Equal(pkg.GoFiles, first) {
			t.Errorf("%s/%s builds the package from %v, and %s/%s from %v; a row must not depend on the system",
				target[0], target[1], pkg.GoFiles, targets[0][0], targets[0][1], first)
		}
	}
}
