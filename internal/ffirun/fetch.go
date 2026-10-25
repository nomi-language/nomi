package ffirun

import "fmt"

// Fetch readies the Go side of the project whose go.mod is at projectRoot
// for a later run with no network (`nomi fetch`). When its files, or a Nomi
// module it needs, declare Go bindings, it builds the wrapper, which brings
// the Go toolchain (the managed one when there is no `go`), the compiler
// module and every Go module the bindings link into their caches, and the
// project's runner for each of targets. A project with no Go bindings needs
// nothing here.
func Fetch(projectRoot string, targets []Target) error {
	r, err := prepareModule(projectRoot, findNomiPackageRoot(projectRoot, projectRoot), "")
	if err != nil {
		return err
	}
	if r.FastPath {
		return nil
	}
	if _, err := ensureCachedWrapperBinary(r); err != nil {
		return err
	}
	for _, t := range targets {
		if _, err := ProjectRunner(r, t, false); err != nil {
			return fmt.Errorf("the runner for %s: %w", t, err)
		}
	}
	return nil
}
