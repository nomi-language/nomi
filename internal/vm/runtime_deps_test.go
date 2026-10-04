package vm_test

import (
	"os/exec"
	"sort"
	"strings"
	"testing"
)

// runtimePackagePath is the runtime library, and compilerModulePath is the
// module the compiler and the runtime share. rt/imports_test.go holds rt itself
// to the standard library and its one allowed dependency; this file holds the
// packages a VM runner links to "no front end".
const (
	runtimePackagePath = "github.com/nomi-language/nomi/rt"
	compilerModulePath = "github.com/nomi-language/nomi"
)

// artifactRuntimePackages are the packages a VM runner links: the runtime
// library (the frame, checked arithmetic, scalar rendering, the collections,
// the structural hashing and the record value model) and the machine that
// executes bytecode over it.
//
// nomi/vmrunner and cmd/nomi-runner are `nomi build`'s runner: what a built
// executable links. The nomi_compiler build tag, which a program crossing
// into std/compiler builds its runner with, is the one variant that links
// the front end, and it is not checked here.
var artifactRuntimePackages = []string{
	runtimePackagePath,
	"github.com/nomi-language/nomi/internal/vm",
	"github.com/nomi-language/nomi/vmrunner",
	"github.com/nomi-language/nomi/cmd/nomi-runner",
}

// frontEndPackages must not appear anywhere in a runtime package's transitive
// dependency set. The Nomi front end is a build-time tool and has no business
// inside a runner that executes compiled bytecode.
//
// nomi/ast and nomi/parser are in this list DELIBERATELY. Do not "simplify"
// the list down to analysis: a value type that holds an *ast.Block body or
// keys a map by *ast.FuncDef ships the entire syntax tree with the runtime
// while a guard that forbade only the analyzer stays green.
var frontEndPackages = map[string]string{
	"github.com/nomi-language/nomi/internal/ast":      "the syntax tree; a runner has no source to represent at run time",
	"github.com/nomi-language/nomi/internal/parser":   "the parser; a runner parses nothing",
	"github.com/nomi-language/nomi/internal/analysis": "the analyzer; type checking happened at compile time",
	"github.com/nomi-language/nomi/internal/frontend": "the shared front end; a runner reads an image, not source",
	"github.com/nomi-language/nomi/internal/irbuild":  "the IR builder; a runner decodes the IR `nomi build` lowered",
	"github.com/nomi-language/nomi/internal/lsp":      "the language server",
	"github.com/nomi-language/nomi/vmhost":            "the embedding API, which lowers source before it runs it",
}

func TestRuntimeArtifactLinksNoFrontEnd(t *testing.T) {
	for _, root := range artifactRuntimePackages {
		imports := importGraph(t, root)
		for _, offender := range sortedStringKeys(frontEndPackages) {
			if _, reached := imports[offender]; !reached {
				continue
			}
			chain, ok := importChain(imports, root, offender)
			if !ok {
				// Reached the dependency set but not via the graph we walked:
				// treat it as a violation anyway rather than silently passing.
				chain = []string{root, offender}
			}
			t.Errorf("%s transitively links %s (%s)\n  via %s\n"+
				"The runtime packages must not link the Nomi front end. "+
				"Move the offending declaration out of the runtime packages "+
				"rather than relaxing this test.",
				root, offender, frontEndPackages[offender], strings.Join(chain, " -> "))
		}
	}
}

// importGraph returns the transitive import graph rooted at pkg, keyed by
// import path. Only first-party edges are retained: the standard library and
// third-party collections are legitimate runtime dependencies, and dropping
// them keeps the failure chain readable.
func importGraph(t *testing.T, pkg string) map[string][]string {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}\t{{join .Imports \" \"}}", pkg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps %s: %v\n%s", pkg, err, out)
	}
	graph := make(map[string][]string)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		path, imports, _ := strings.Cut(line, "\t")
		if !isFirstPartyPackage(path) {
			continue
		}
		var kept []string
		for _, imp := range strings.Fields(imports) {
			if isFirstPartyPackage(imp) {
				kept = append(kept, imp)
			}
		}
		graph[path] = kept
	}
	return graph
}

// isFirstPartyPackage reports whether an import path is in this module.
// Third-party and standard-library packages are legitimate runtime
// dependencies, and dropping them from the graph keeps the failure chain
// readable.
func isFirstPartyPackage(path string) bool {
	return path == compilerModulePath || strings.HasPrefix(path, compilerModulePath+"/")
}

// importChain returns the shortest import path from root to target, so a
// failure names not just the forbidden package but the edge that pulled it in.
func importChain(graph map[string][]string, root, target string) ([]string, bool) {
	parent := map[string]string{root: ""}
	queue := []string{root}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == target {
			var chain []string
			for at := cur; at != ""; at = parent[at] {
				chain = append(chain, at)
			}
			for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
				chain[i], chain[j] = chain[j], chain[i]
			}
			return chain, true
		}
		for _, next := range graph[cur] {
			if _, seen := parent[next]; seen {
				continue
			}
			parent[next] = cur
			queue = append(queue, next)
		}
	}
	return nil, false
}

func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
