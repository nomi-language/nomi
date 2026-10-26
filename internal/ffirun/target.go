package ffirun

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"

	"github.com/nomi-language/nomi/internal/gotoolchain"
)

// A bound Go package must build for the target the runner is built for. The
// cgo check (cgo.go) runs before anything is built, since every build
// refuses cgo; whether a package builds for one GOOS/GOARCH is Go's own
// answer, so it is read off the runner build that failed: a package with no
// file for the target (modernc.org/libc has none for js/wasm or wasip1), or
// one whose files do not compile there. Each `gopkg` that reaches such a
// package is reported at its declaration, with the import chain and Go's
// first error, instead of the compiler's whole output.

// goBuildFailure is a failed `go build` and what it printed.
type goBuildFailure struct {
	err    error
	output string
}

func (e *goBuildFailure) Error() string { return e.err.Error() }
func (e *goBuildFailure) Unwrap() error { return e.err }

// goListPackage is what targetBuildProblem reads of `go list -json`.
type goListPackage struct {
	ImportPath string
	Imports    []string
	Error      *struct{ Err string }
}

// compileHeader is the line `go build` prints before a package's compile
// errors.
var compileHeader = regexp.MustCompile(`^# (\S+)$`)

// positionPrefix is a compile error's `file.go:12:3: ` prefix.
var positionPrefix = regexp.MustCompile(`^\S+?\.(go|s):\d+(:\d+)?: `)

// targetBuildProblem is buildErr, the failed build of r's runner for target
// in dir under env, as the `gopkg` declarations whose packages do not build
// for target, or nil when the failure is not one of those.
func targetBuildProblem(r *Result, dir string, env []string, target Target, buildErr error) error {
	var failure *goBuildFailure
	if !errors.As(buildErr, &failure) {
		return nil
	}
	// The packages that failed, each with what is wrong with it, in words
	// that follow "which" or "it".
	why := map[string]string{}
	lines := strings.Split(failure.output, "\n")
	for i, line := range lines {
		m := compileHeader.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		reason := "does not compile for " + target.String()
		if i+1 < len(lines) && !strings.HasPrefix(lines[i+1], "#") {
			if first := positionPrefix.ReplaceAllString(strings.TrimSpace(lines[i+1]), ""); first != "" {
				reason += ": " + first
			}
		}
		why[m[1]] = reason
	}
	pkgs, err := goListDeps(dir, env, r.discovered)
	if err != nil {
		return nil
	}
	for path, p := range pkgs {
		if p.Error == nil || why[path] != "" {
			continue
		}
		if strings.Contains(p.Error.Err, "build constraints exclude all Go files") {
			why[path] = "has no Go files for " + target.String() + " (build constraints exclude every one)"
		} else {
			why[path] = "does not load for " + target.String() + ": " + p.Error.Err
		}
	}
	if len(why) == 0 {
		return nil
	}
	var problems []string
	for _, d := range r.discovered {
		chain := failingChain(d.ImportPath, pkgs, why)
		if chain == nil {
			continue
		}
		last := chain[len(chain)-1]
		msg := fmt.Sprintf("%s: Go package %q does not build for %s: ", packageLocation(r.projectRoot, d), d.ImportPath, target)
		if len(chain) == 1 {
			msg += "it " + why[last]
		} else {
			msg += fmt.Sprintf("it imports %s (%s), which %s", last, strings.Join(chain, " → "), why[last])
		}
		problems = append(problems, msg)
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("the project's Go bindings do not build for %s:\n  - %s", target, strings.Join(problems, "\n  - "))
}

// failingChain is the import chain from root to the nearest package in why,
// or nil when root reaches none.
func failingChain(root string, pkgs map[string]goListPackage, why map[string]string) []string {
	parent := map[string]string{root: ""}
	queue := []string{root}
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		if why[path] != "" {
			var chain []string
			for p := path; p != ""; p = parent[p] {
				chain = append([]string{p}, chain...)
			}
			return chain
		}
		for _, imp := range pkgs[path].Imports {
			if _, seen := parent[imp]; !seen {
				parent[imp] = path
				queue = append(queue, imp)
			}
		}
	}
	return nil
}

// goListDeps is every package the discovered packages reach, built as env
// builds them, by import path.
func goListDeps(dir string, env []string, discovered []DiscoveredPackage) (map[string]goListPackage, error) {
	goBin, err := gotoolchain.FindForProjectRunner()
	if err != nil {
		return nil, err
	}
	args := []string{"list", "-e", "-deps", "-mod=mod", "-json=ImportPath,Imports,Error"}
	for _, d := range discovered {
		args = append(args, d.ImportPath)
	}
	cmd := exec.Command(goBin, args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, err
	}
	pkgs := map[string]goListPackage{}
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p goListPackage
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		pkgs[p.ImportPath] = p
	}
	return pkgs, nil
}
