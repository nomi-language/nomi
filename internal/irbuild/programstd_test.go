package irbuild

import (
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// Every module lowered with its program meets programstd.go's rule: it
// declares no host function or host type, no module lowered once for the
// process imports it, the shared lowering holds none of it, and it checks and
// lowers with no error or decline, its `//!` cases included. It runs again in
// a child process whose stdlib holds the test-only `_fixtures/` modules
// (std.FixturesLoaded), which the rule covers too.
func TestProgramStdModulesQualify(t *testing.T) {
	lib := std.Shared()
	if !std.FixturesLoaded() {
		cmd := exec.Command(os.Args[0], "-test.run=^TestProgramStdModulesQualify$", "-test.count=1", "-test.v")
		cmd.Env = append(os.Environ(), "NOMI_STD_FIXTURES=1")
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "--- PASS: TestProgramStdModulesQualify") {
			t.Errorf("the child with std's fixtures failed: %v\n%s", err, out)
		}
	} else if programStdModuleNodes()["_fixtures/conn"] == nil {
		t.Errorf("the stdlib holds its fixtures and std/_fixtures/conn is not lowered per program")
	}
	names := make([]string, 0, len(programStdModules))
	for name := range programStdModules {
		names = append(names, name)
	}
	for name := range programStdModuleNodes() {
		if !programStdModules[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		nodes, known := lib.Nodes[name]
		if !known {
			t.Errorf("std/%s is listed and std declares no such module", name)
			continue
		}
		for _, e := range lib.Errors[name] {
			t.Errorf("std/%s: %s", name, e.Error())
		}
		for _, n := range nodes {
			switch d := n.(type) {
			case *ast.ExternFunc:
				t.Errorf("std/%s declares `host fn %s`, which a program's lowering cannot answer", name, d.Name)
			case *ast.ExternType:
				t.Errorf("std/%s declares `host type %s`, which needs an rt type", name, d.Name)
			}
		}
		if stdlibLowering().views[name] != nil {
			t.Errorf("std/%s is lowered once for the process as well", name)
		}
		declined := map[string]string{}
		prev := IRDeclineObserved
		IRDeclineObserved = func(fn, reason string) { declined[fn] = reason }
		_, res, err := generateProgramStdTestIR(name, nil, nil)
		IRDeclineObserved = prev
		if err != nil {
			t.Errorf("std/%s: %v", name, err)
			continue
		}
		for fn, reason := range declined {
			t.Errorf("std/%s: %s is declined: %s", name, fn, reason)
		}
		if len(res.IR) == 0 {
			t.Errorf("std/%s retained nothing", name)
		}
	}
	for module, nodes := range lib.Nodes {
		if isProgramStd(module) {
			continue
		}
		for _, imported := range importedStdModules(nodes, true) {
			if isProgramStd(imported) {
				t.Errorf("std/%s imports std/%s, which is lowered per program, so its shared lowering cannot name it", module, imported)
			}
		}
	}
}
