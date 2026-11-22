package irbuild

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// stdTypeCases are the types shared std modules declare in Nomi with no
// spec row, each with its run-time spelling.
var stdTypeCases = []struct{ module, name, spelled string }{
	{"json", "Json.Case", "json.Json.Case"},
	{"json", "ToJson.Options", "json.ToJson.Options"},
	{"json", "FromJson.Options", "json.FromJson.Options"},
	{"testing", "Clock", "testing.Clock"},
}

// declaredType is the checker's type of the declaration name in module's
// scope.
func declaredType(t *testing.T, lib *std.StdLib, module, name string) analysis.Type {
	t.Helper()
	sym := resolvedTypeSymbol(lib.Files[module], name)
	if sym == nil || sym.Type == nil {
		t.Fatalf("std/%s declares no %s", module, name)
	}
	return sym.Type
}

// TestStdTypes_OneDefForTheProcess holds each type a shared std module
// declares in Nomi with no spec row to one def for the process: the
// projection with no gen answers it, the same pointer every time, marked
// with its declaring module, package-neutral and named by its declaration
// (`json.Json.Case`); the shared lowering's functions over it take that
// def; and a type a listed module declares has none.
func TestStdTypes_OneDefForTheProcess(t *testing.T) {
	lib := std.Shared().Ensure("json", "testing", "uuid")
	for _, c := range stdTypeCases {
		ty := declaredType(t, lib, c.module, c.name)
		k := projectShared(ty)
		if k.tag != tagNamed || k.def == nil {
			t.Errorf("std/%s.%s has no shared kind: %v (%s)", c.module, c.name, k, stdTypeWhy(stdTypeKey{origin: "std/" + c.module, name: c.name}))
			continue
		}
		d := k.def
		if again := projectShared(ty); again != k {
			t.Errorf("std/%s.%s: a second projection is another kind", c.module, c.name)
		}
		if d.stdOrigin != "std/"+c.module || !d.lowerable || !k.packageNeutral() {
			t.Errorf("std/%s.%s: origin %q, lowerable %v, neutral %v", c.module, c.name, d.stdOrigin, d.lowerable, k.packageNeutral())
		}
		if name, ok := stdSpecTypeName(d); !ok || name != c.spelled {
			t.Errorf("std/%s.%s is spelled %q at run time, want %q", c.module, c.name, name, c.spelled)
		}
	}
	idx := stdlibLowering()
	caseKind := projectShared(declaredType(t, lib, "json", "Json.Case"))
	equal := idx.byType["Json.Case.equal"]
	if len(equal) != 1 || len(equal[0].params) != 2 || equal[0].params[0] != caseKind || !equal[0].lowerable() {
		t.Errorf("the shared lowering's Json.Case.equal does not take the process's def: %+v", equal)
	}
	if k := projectShared(declaredType(t, lib, "uuid", "Uuid")); k != kindInvalid {
		t.Errorf("std/uuid is declared with each program, but its Uuid has a shared kind %v", k)
	}
}

// stdTypesChildEnv marks the child of TestStdTypes_TwoProcessesAgree, and
// names the program it lowers.
const stdTypesChildEnv = "NOMI_IRBUILD_STD_TYPES_CHILD"

// stdTypesProgram holds values of every type stdTypeCases names.
const stdTypesProgram = `import std::io
import std::json.{Json, ToJson, FromJson}
import std::testing

fn main()
    c = Json.Case.Camel
    io.print(Display.to_string(c))
    io.print(c == Json.Case.Snake)
    to = ToJson.Options{rename_all: c}
    from = FromJson.Options{rename_all: Json.Case.Kebab}
    io.print(to == ToJson.Options{rename_all: Json.Case.Camel})
    io.print(from.rename_all)
    clocks = [testing.Clock.System, testing.Clock.Virtual]
    io.print(List.length(clocks))
end
`

// TestStdTypes_TwoProcessesAgree lowers stdTypesProgram in two processes
// over one artifact cache, the first lowering the stdlib and writing the
// std bundle, the second reading it, and requires each to name every type
// of stdTypeCases alike (its declaring module, name, run-time spelling and
// layout) and to build the program's image byte for byte alike.
func TestStdTypes_TwoProcessesAgree(t *testing.T) {
	if path := os.Getenv(stdTypesChildEnv); path != "" {
		stdTypesFingerprint(t, path)
		return
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "main.nomi")
	if err := os.WriteFile(path, []byte(stdTypesProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	var prints []string
	for i := range 2 {
		cmd := exec.Command(os.Args[0], "-test.run=^TestStdTypes_TwoProcessesAgree$", "-test.count=1", "-test.v")
		cmd.Env = append(os.Environ(), stdTypesChildEnv+"="+path, stdLoadChildEnv+"=1", artifactcache.RootEnv+"="+cache)
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("child %d failed: %v\n%s", i, err, b)
		}
		_, after, found := bytes.Cut(b, []byte("FINGERPRINT\n"))
		fp, _, ended := bytes.Cut(after, []byte("END\n"))
		if !found || !ended {
			t.Fatalf("child %d printed no fingerprint:\n%s", i, b)
		}
		prints = append(prints, string(fp))
		if lowered := bytes.Contains(b, []byte("std bundle lowered")); lowered != (i == 0) {
			t.Errorf("child %d lowered the stdlib: %v, want %v\n%s", i, lowered, i == 0, b)
		}
	}
	if prints[0] != prints[1] {
		t.Fatalf("the process that lowered the stdlib and the one that decoded it disagree:\n%s\n---\n%s", prints[0], prints[1])
	}
	t.Logf("both processes:\n%s", prints[0])
}

// stdTypesFingerprint prints, between FINGERPRINT and END, each type of
// stdTypeCases as this process holds it, and the sum of path's image.
func stdTypesFingerprint(t *testing.T, path string) {
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the program: %v", err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatalf("the program does not lower: %v", err)
	}
	image, unretained, err := reachedImage(res)
	if err != nil || len(unretained) > 0 {
		t.Fatalf("image: %v, unretained %v", err, unretained)
	}
	var out strings.Builder
	lib := std.Shared()
	for _, c := range stdTypeCases {
		d := projectShared(declaredType(t, lib, c.module, c.name)).def
		if d == nil {
			t.Fatalf("std/%s.%s has no shared def", c.module, c.name)
		}
		name, _ := stdSpecTypeName(d)
		fmt.Fprintf(&out, "%s %s %s", d.stdOrigin, d.nomi, name)
		for _, f := range d.fields {
			fmt.Fprintf(&out, " %s:%s", f.nomi, f.k.nomi())
		}
		for _, v := range d.variants {
			fmt.Fprintf(&out, " %d=%s", v.tag, v.nomi)
		}
		out.WriteString("\n")
	}
	fmt.Fprintf(&out, "image %x\n", sha256.Sum256(image))
	t.Logf("std bundle %s", stdBundleState.from)
	fmt.Printf("FINGERPRINT\n%sEND\n", out.String())
}

// listedModuleStops is, for each std module declared with each program
// (programStdModules), what would stop it from being lowered once for the
// process with the shared modules, as TestStdObjects_WhatStopsEachListedModule
// derives it: the first of its own causes, in the order the test takes
// them, or "imports std/X" for a module whose only cause is a listed module
// it imports that is stopped, or "none".
var listedModuleStops = map[string]string{
	"actors":           "declares a generic type",
	"bits":             "none",
	"bytes/cursor":     "none",
	"csv":              "position: iter",
	"embed":            "position: iter",
	"fs":               "position: iter",
	"fs/testing":       "declares a generic type",
	"html":             "position: interface value",
	"http":             "position: iter",
	"http/testing":     "position: interface value",
	"jobs":             "position: interface value",
	"jobs/testing":     "position: interface value",
	"jwt":              "none",
	"literals/testing": "none",
	"log":              "none",
	"log/testing":      "declares a generic type",
	"mail":             "imports std/html",
	"mail/testing":     "position: std type with no shared def: non-scalar field type",
	"money":            "position: iter",
	"net":              "none",
	"parse":            "position: generic std type instance",
	"passwords":        "position: interface value",
	"path":             "none",
	"phrase":           "position: interface value",
	"presence":         "declares a generic type",
	"process":          "none",
	"process/testing":  "declares a generic type",
	"reverse":          "declares a generic type",
	"sessions":         "declares a generic type",
	"signatures":       "none",
	"sql":              "position: interface value",
	"sql/testing":      "position: interface value",
	"storage":          "position: iter",
	"storage/testing":  "position: interface value",
	"tls":              "none",
	"toml":             "position: interface value",
	"url":              "none",
	"uuid":             "imports std/parse",
	"wasm":             "none",
	"websocket":        "imports std/http",
	"xml":              "position: iter",
}

// TestStdObjects_WhatStopsEachListedModule is TestStdObjects_WhatHoldsEachModuleShared
// in the other direction: for every module declared with each program, with
// the registry answering those modules' types too (stdTypesListed), what
// the shared lowering could not hold of it. Its causes, each counted:
//
//   - a signature position of one of its functions or `once`s that still
//     has no kind with no gen, by sharedRefusalCause: an interface held as
//     a value, an Iter, an instance of a generic std type, or a std type
//     the registry built and did not admit, with the reason;
//   - a type it declares that the registry did not admit, with the reason;
//   - a generic type it declares, whose instances at a program's types the
//     process holds none of;
//   - a listed module it imports, at the top of its file, that is itself
//     stopped.
//
// Whether each body lowers in the shared lowering is not tried. -v logs
// every module's causes and the totals; the first cause of each is pinned
// in listedModuleStops.
func TestStdObjects_WhatStopsEachListedModule(t *testing.T) {
	lib := std.All()
	stdTypesListed.Store(true)
	defer stdTypesListed.Store(false)
	var listed []string
	for name := range lib.Nodes {
		if isProgramStd(name) && !strings.HasPrefix(name, "_fixtures/") {
			listed = append(listed, name)
		}
	}
	sort.Strings(listed)
	own := map[string]map[string]int{}
	order := map[string][]string{}
	note := func(module, cause string) {
		if own[module] == nil {
			own[module] = map[string]int{}
		}
		if own[module][cause] == 0 {
			order[module] = append(order[module], cause)
		}
		own[module][cause]++
	}
	positions := 0
	for _, module := range listed {
		fa := lib.Files[module]
		check := func(rec analysis.Type) {
			positions++
			if rec == nil || projectShared(rec) != kindInvalid || analysis.ContainsTypeParam(rec) {
				return
			}
			note(module, "position: "+sharedRefusalCause(rec))
		}
		for _, c := range collectStdCandidates(module, lib.Nodes[module], fa) {
			params, ret := stdSigOf(c.f.node)
			for _, p := range params {
				check(stdParamType(fa, p))
			}
			if ret != nil {
				check(stdTypeExprType(fa, ret))
			}
		}
		for _, o := range collectStdOnces(module, lib.Nodes[module], fa) {
			if o.decl.Value == nil {
				continue
			}
			if o.decl.TypeAnnotation != nil {
				check(stdTypeExprType(fa, o.decl.TypeAnnotation))
			} else {
				check(onceSymbolType(fa, o.decl))
			}
		}
		for _, n := range lib.Nodes[module] {
			var name string
			generic := false
			switch d := n.(type) {
			case *ast.StructDef:
				name, generic = d.Name, len(d.TypeParams) > 0
			case *ast.EnumDef:
				name, generic = d.Name, len(d.TypeParams) > 0
			case *ast.TypeDef:
				if isDistinctRefinementDecl(d) {
					continue
				}
				name, generic = d.Name, len(d.TypeParams) > 0
			default:
				continue
			}
			if generic {
				note(module, "declares a generic type")
				continue
			}
			ty := declaredType(t, lib, module, name)
			if projectShared(ty) != kindInvalid {
				continue
			}
			why := stdTypeWhy(stdTypeKey{origin: "std/" + module, name: name})
			if why == "" {
				why = "no def"
			}
			note(module, "declares a type with no shared def: "+why)
			t.Logf("std/%s.%s has no shared def: %s", module, name, why)
		}
	}
	// A module that imports a stopped listed module is stopped with it: by
	// its own imports, at the top of its file, not its prompts', which run
	// as programs and are there only once its bodies are checked.
	ownImports := func(module string) []string {
		var imports []ast.Node
		for _, n := range lib.Nodes[module] {
			switch n.(type) {
			case *ast.ImportStmt, *ast.ImportBlock:
				imports = append(imports, n)
			}
		}
		return importedStdModules(imports, true)
	}
	stoppedBy := map[string]string{}
	stopped := func(name string) bool { return len(own[name]) > 0 || stoppedBy[name] != "" }
	for changed := true; changed; {
		changed = false
		for _, from := range listed {
			if stopped(from) {
				continue
			}
			for _, imported := range ownImports(from) {
				if isProgramStd(imported) && imported != from && stopped(imported) {
					stoppedBy[from] = imported
					changed = true
					break
				}
			}
		}
	}
	totals := map[string]int{}
	modulesBy := map[string]int{}
	got := map[string]string{}
	for _, module := range listed {
		first := "none"
		switch {
		case len(order[module]) > 0:
			first = order[module][0]
		case stoppedBy[module] != "":
			first = "imports std/" + stoppedBy[module]
		}
		got[module] = first
		var line []string
		for _, cause := range order[module] {
			totals[cause] += own[module][cause]
			modulesBy[cause]++
			line = append(line, fmt.Sprintf("%s: %d", cause, own[module][cause]))
		}
		if by := stoppedBy[module]; by != "" {
			line = append(line, "imports std/"+by)
		}
		if len(line) == 0 {
			line = []string{"none"}
		}
		t.Logf("std/%s: %s", module, strings.Join(line, "; "))
	}
	causes := make([]string, 0, len(totals))
	for c := range totals {
		causes = append(causes, c)
	}
	sort.Strings(causes)
	var summary strings.Builder
	for _, c := range causes {
		fmt.Fprintf(&summary, "\n  %s: %d in %d modules", c, totals[c], modulesBy[c])
	}
	t.Logf("%d listed modules, %d signature positions; causes:%s", len(listed), positions, summary.String())
	var diffs []string
	for _, module := range listed {
		if want := listedModuleStops[module]; want != got[module] {
			diffs = append(diffs, fmt.Sprintf("std/%s: stopped by %q, listed as %q", module, got[module], want))
		}
	}
	for module := range listedModuleStops {
		if _, ok := got[module]; !ok {
			diffs = append(diffs, fmt.Sprintf("std/%s is pinned and is not a listed module", module))
		}
	}
	if len(diffs) > 0 {
		sort.Strings(diffs)
		var pin strings.Builder
		for _, module := range listed {
			fmt.Fprintf(&pin, "\t%q: %q,\n", module, got[module])
		}
		t.Errorf("listedModuleStops differs from the stdlib:\n  %s\nthe stdlib's list:\n%s", strings.Join(diffs, "\n  "), pin.String())
	}
}

// TestStdTypes_AGenericDeclarationHasNoSharedDef holds the rule for a
// generic type a shared std module declares in Nomi with no spec row
// (std/iter's `Seq`, std/ranges' `StepByRange`): the registry admits no
// def for the declaration, whose shell refuses `generic type`, and answers
// no instance, so an instance is built in the gen that asks for it, by the
// instance mechanism a program's generic type has.
func TestStdTypes_AGenericDeclarationHasNoSharedDef(t *testing.T) {
	lib := std.Shared().Ensure("iter", "ranges")
	for _, c := range []struct{ module, name string }{{"iter", "Seq"}, {"ranges", "StepByRange"}} {
		ty := declaredType(t, lib, c.module, c.name)
		if k := projectShared(ty); k != kindInvalid {
			t.Errorf("std/%s.%s, a generic declaration, has a shared kind %v", c.module, c.name, k)
		}
		var g *gen
		if d := g.stdDeclaredDef(ty); d != nil {
			t.Errorf("std/%s.%s, a generic declaration, has a process-wide def", c.module, c.name)
		}
		if why := stdTypeWhy(stdTypeKey{origin: "std/" + c.module, name: c.name}); why != "generic type" {
			t.Errorf("std/%s.%s has no shared def because %q, want %q", c.module, c.name, why, "generic type")
		}
	}
}
