package irbuild

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/expectation"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/std"
)

// TestStdObjects_ProgramsLinkAsTheirWholeLowering lowers programs whole and
// links them through objects as the object harness does
// (relinkThroughObjects: each of the program's modules, each shared std
// module, each listed std module it links and its std instances encoded on
// its own and decoded with one ir.Linker). The linked program must define
// the same functions and cells as the whole one, module by module and by
// linkage name, less one an earlier module already defines (the linker
// keeps the first), and each function's IR, encoded on its own, must be the
// same bytes. Its inputs are every program irbuild.expect records (about 8
// seconds for all of them, so there is no sample). A program the front end
// refuses has nothing to link and is counted apart.
//
// That the linked programs run as the whole ones is held elsewhere:
// TestObjectsLinkToTheSameProgram (internal/expectation) runs the corpus,
// the tour and the failure fixtures linked this way against their records,
// and `make check-full`'s objects-irbuild step runs every irbuild.expect
// program so. There is no known-gaps list. Under NOMI_LINK_OBJECTS=1 every
// lowering is already linked, so there is no whole one and the test skips.
//
// The shared std modules a program links are objects linked by name before
// the program lowers: the std bundle holds each as an object, and a process
// decodes each with one ir.Linker when it first needs the module
// (stdbundle.go), which TestStdBundle_DecodesToTheLowering holds to the
// lowering byte for byte.
func TestStdObjects_ProgramsLinkAsTheirWholeLowering(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers every irbuild.expect program; -short")
	}
	if linksThroughObjects() {
		t.Skip("every lowering already links through objects, so there is no whole lowering to compare with")
	}
	set := goldenSet(t, irbuildGoldenPopulation)
	root, err := expectation.Root()
	if err != nil {
		t.Fatal(err)
	}
	objects := newFuncObjects()
	linked, refused, dropped := 0, 0, 0
	for _, c := range set.Cases {
		path, err := goldenCaseEntry(t, c, root)
		if err != nil {
			t.Errorf("%s: %v", c.ID, err)
			continue
		}
		p, err := Analyze(path)
		if err != nil {
			refused++
			continue
		}
		res, _, err := GenerateIR(p)
		if err != nil {
			t.Errorf("%s: the program does not lower: %v", c.ID, err)
			continue
		}
		whole := res.IRModules()
		if err := relinkThroughObjects(res); err != nil {
			t.Errorf("%s: the program does not link through objects: %v", c.ID, err)
			continue
		}
		after := res.IRModules()
		if diffs := objects.linkedDifferences(whole, after); len(diffs) > 0 {
			t.Errorf("%s: linked through objects, the program differs from its whole lowering:\n%s", c.ID, strings.Join(diffs, "\n"))
		}
		dropped += funcCount(whole) - funcCount(after)
		linked++
	}
	if linked < len(set.Cases)/2 {
		t.Fatalf("linked %d of %d programs (%d refused by the front end); the enumeration is wrong", linked, len(set.Cases), refused)
	}
	t.Logf("%d of %d programs linked through objects as their whole lowering (%d bodies defined twice, kept once); %d refused by the front end",
		linked, len(set.Cases), dropped, refused)
}

// sharedModuleHolds is each std module lowered once for the process, with
// the first thing that keeps it out of programStdModules, the modules
// declared with each program and linked by name as a program's files are
// (programstd.go's rule). TestStdObjects_WhatHoldsEachModuleShared derives
// it. The classes, in the order the first is taken:
//
//   - "host fn": it declares a `host fn`, which a call reaches through the
//     host registry's row (stdFunc.rtCall), not a symbol an object defines.
//   - "host type": it declares a `host type`, whose values are an rt Go type.
//   - "process-wide type": it declares a type the projection with no gen
//     answers (projectShared): one def for the process, from a spec row,
//     which every program shares by pointer. A type's symbol has no linkage
//     name, so no object can carry it.
//   - "imported by a held module": a held module imports it, and a shared
//     body cannot name a type each program declares again.
//   - "prelude": the prelude, every file's scope.
//
// A module with none of these is "none": nothing static keeps it shared,
// and whether it lowers with no decline as a listed module is what listing
// it would test. Declaring a generic type holds nothing (a listed module
// may declare one), so the log only notes it.
var sharedModuleHolds = map[string]string{
	"add":               "imported by a held module",
	"assertions":        "host fn",
	"averageable":       "host fn",
	"base64":            "host fn",
	"bits":              "process-wide type",
	"bitwise":           "imported by a held module",
	"bool":              "host type",
	"bytes":             "host fn",
	"bytes/cursor":      "process-wide type",
	"calendar":          "host fn",
	"channels":          "host fn",
	"checksum":          "host fn",
	"code_points":       "host fn",
	"collation":         "host fn",
	"comparable":        "process-wide type",
	"compiler":          "host fn",
	"contains":          "imported by a held module",
	"context":           "host fn",
	"convert":           "imported by a held module",
	"csv/host":          "host fn",
	"currency":          "host fn",
	"debug":             "process-wide type",
	"decimal":           "host fn",
	"discrete":          "process-wide type",
	"display":           "process-wide type",
	"divide":            "imported by a held module",
	"duration":          "host fn",
	"dynamic":           "host fn",
	"embed/host":        "host fn",
	"equatable":         "process-wide type",
	"float":             "host fn",
	"fs/host":           "host fn",
	"hash":              "host fn",
	"hashable":          "process-wide type",
	"hex":               "host fn",
	"html/host":         "host fn",
	"http/host":         "host fn",
	"instant":           "host fn",
	"int":               "host fn",
	"io":                "host fn",
	"iter":              "host fn",
	"json":              "host fn",
	"lists":             "host fn",
	"literals":          "process-wide type",
	"locale":            "host fn",
	"localize":          "process-wide type",
	"mail/host":         "host fn",
	"maps":              "host fn",
	"matcher":           "process-wide type",
	"option":            "imported by a held module",
	"money/format":      "host fn",
	"multiply":          "imported by a held module",
	"passwords/host":    "host fn",
	"cipher/host":       "host fn",
	"compress/host":     "host fn",
	"key_exchange/host": "host fn",
	"system/host":       "host fn",
	"path":              "none",
	"path/host":         "host fn",
	"phrase/format":     "host fn",
	"plural":            "host fn",
	"prelude":           "prelude",
	"process/host":      "host fn",
	"productable":       "process-wide type",
	"random":            "host fn",
	"ranges":            "imported by a held module",
	"regex":             "host fn",
	"remainder":         "imported by a held module",
	"resources":         "process-wide type",
	"results":           "host type",
	"sets":              "host fn",
	"signals":           "host fn",
	"signatures":        "process-wide type",
	"signatures/host":   "host fn",
	"startup":           "process-wide type",
	"steppable":         "imported by a held module",
	"strings":           "host fn",
	"structs":           "none",
	"subtract":          "imported by a held module",
	"summable":          "process-wide type",
	"supervisors":       "host fn",
	"tasks":             "host fn",
	"terminal/host":     "host fn",
	"testing":           "host fn",
	"timer":             "host fn",
	"toml/host":         "host fn",
	"type":              "host type",
	"unit":              "host type",
	"url/host":          "host fn",
	"variants":          "process-wide type",
	"vectors":           "host fn",
	"wasm/host":         "host fn",
	"xml/host":          "host fn",
}

// TestStdObjects_WhatHoldsEachModuleShared names, for every std module
// lowered once for the process, the first thing that keeps it from being
// lowered as a program's files are (sharedModuleHolds), and requires the
// pinned list: a module whose first cause changes, one that starts to
// qualify among them, fails until the list is changed, and so does a module
// added to or moved out of the shared lowering. -v logs every cause of each
// module, the module that holds it by import, and how large the shared
// lowering is: modules, functions and cells, and the bytes of the modules'
// objects.
//
// Linking is not what holds a module. Every shared module is already an
// object linked by name: the std bundle holds each one encoded with
// ir.EncodeObject, a process decodes each with one ir.Linker when the
// stdlib builds the module, and
// TestStdBundle_DecodesToTheLowering holds that to the lowering byte for
// byte; TestStdObjects_ProgramsLinkAsTheirWholeLowering and
// internal/expectation's TestObjectsLinkToTheSameProgram link a program
// beside them. What differs is how a program's lowering reads a shared
// module: through the std index's records and process-wide defs, where a
// listed module is declared with the program and read by linkage name.
func TestStdObjects_WhatHoldsEachModuleShared(t *testing.T) {
	lib := std.All()
	names := make([]string, 0, len(lib.Nodes))
	for name := range lib.Nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	causes := map[string][]string{}
	generics := map[string]bool{}
	var shared, listed []string
	for _, name := range names {
		if isProgramStd(name) {
			listed = append(listed, name)
			continue
		}
		shared = append(shared, name)
		if name == "prelude" {
			causes[name] = []string{"prelude"}
			continue
		}
		fa := lib.Files[name]
		has := map[string]bool{}
		for _, n := range lib.Nodes[name] {
			ast.Inspect(n, func(m ast.Node) bool {
				var typeName string
				generic := false
				switch d := m.(type) {
				case *ast.ExternFunc:
					has["host fn"] = true
					return false
				case *ast.ExternType:
					has["host type"] = true
					return false
				case *ast.StructDef:
					typeName, generic = d.Name, len(d.TypeParams) > 0
				case *ast.EnumDef:
					typeName, generic = d.Name, len(d.TypeParams) > 0
				case *ast.TypeDef:
					typeName, generic = d.Name, len(d.TypeParams) > 0
				case *ast.InterfaceDef:
					typeName, generic = d.Name, len(d.TypeParams) > 0
				}
				switch {
				case typeName == "":
				case generic:
					has["generic type"] = true
				case fa != nil && fa.ModuleScope != nil:
					if sym := fa.ModuleScope.Lookup(typeName); sym != nil && sym.Type != nil && projectShared(sym.Type) != kindInvalid {
						has["process-wide type"] = true
					}
				}
				return true
			})
		}
		for _, class := range []string{"host fn", "host type", "process-wide type"} {
			if has[class] {
				causes[name] = append(causes[name], class)
			}
		}
		if has["generic type"] {
			generics[name] = true
		}
	}
	// A module a held module imports is held with it, whatever else holds
	// it. The prelude only re-exports, so its imports hold nothing
	// (programstd.go's rule).
	heldBy := map[string]string{}
	held := func(name string) bool { return len(causes[name]) > 0 || heldBy[name] != "" }
	for changed := true; changed; {
		changed = false
		for _, from := range shared {
			if from == "prelude" || !held(from) {
				continue
			}
			for _, imported := range importedStdModules(lib.Nodes[from], true) {
				if isProgramStd(imported) || imported == from || heldBy[imported] != "" || lib.Nodes[imported] == nil {
					continue
				}
				heldBy[imported] = from
				changed = true
			}
		}
	}
	for _, name := range shared {
		if heldBy[name] != "" {
			causes[name] = append(causes[name], "imported by a held module")
		}
	}
	got := map[string]string{}
	count := map[string]int{}
	for _, name := range shared {
		first := "none"
		if len(causes[name]) > 0 {
			first = causes[name][0]
		}
		got[name] = first
		count[first]++
		for _, c := range causes[name][min(1, len(causes[name])):] {
			count["also "+c]++
		}
		if generics[name] {
			count["declares a generic type"]++
		}
	}
	classes := make([]string, 0, len(count))
	for c := range count {
		classes = append(classes, c)
	}
	sort.Strings(classes)
	var summary strings.Builder
	for _, c := range classes {
		fmt.Fprintf(&summary, "\n  %s: %d", c, count[c])
	}
	t.Logf("%d std modules: %d listed, %d shared; the first cause that holds each shared module:%s", len(names), len(listed), len(shared), summary.String())
	for _, name := range shared {
		line := fmt.Sprintf("std/%s: %s", name, got[name])
		if len(causes[name]) > 1 {
			line = fmt.Sprintf("std/%s: %s", name, strings.Join(causes[name], ", "))
		}
		if by := heldBy[name]; by != "" {
			line += " (std/" + by + ")"
		}
		if generics[name] {
			line += "; declares a generic type"
		}
		t.Log(line)
	}
	var diffs []string
	for _, name := range shared {
		if want, ok := sharedModuleHolds[name]; !ok {
			diffs = append(diffs, fmt.Sprintf("std/%s is shared, held by %q, and not listed", name, got[name]))
		} else if want != got[name] {
			diffs = append(diffs, fmt.Sprintf("std/%s: held by %q, listed as %q", name, got[name], want))
		}
	}
	for name := range sharedModuleHolds {
		if _, ok := got[name]; !ok {
			diffs = append(diffs, fmt.Sprintf("std/%s is listed and is not a shared module", name))
		}
	}
	if len(diffs) > 0 {
		sort.Strings(diffs)
		var pin strings.Builder
		for _, name := range shared {
			fmt.Fprintf(&pin, "\t%q: %q,\n", name, got[name])
		}
		t.Errorf("sharedModuleHolds differs from the stdlib:\n  %s\nthe stdlib's list:\n%s", strings.Join(diffs, "\n  "), pin.String())
	}

	// The shared lowering's size, for the record.
	idx := stdlibLowering()
	funcs, cells, bytesTotal, withIR := 0, 0, 0, 0
	for _, name := range shared {
		m := idx.irModules[idx.modulePkg[name]]
		if m == nil {
			continue
		}
		data, err := ir.EncodeObject(m)
		if err != nil {
			t.Errorf("std/%s does not encode as an object: %v", name, err)
			continue
		}
		withIR++
		funcs += len(m.Funcs())
		cells += len(m.Cells())
		bytesTotal += len(data)
	}
	t.Logf("the shared lowering: %d modules restored (%d pending), %d with IR, %d functions, %d cells, %d bytes as objects",
		len(idx.order), len(idx.pending), withIR, funcs, cells, bytesTotal)
	if withIR == 0 {
		t.Fatal("no shared module has IR, so the census counted nothing")
	}
}

// funcObjects is each lowered function's IR encoded on its own, cached by
// function: a shared std module is the same in every program's whole
// lowering.
type funcObjects map[*ir.Func][]byte

func newFuncObjects() funcObjects { return funcObjects{} }

// lowered is f's object, encoded once.
func (o funcObjects) lowered(f *ir.Func) ([]byte, error) {
	if data, ok := o[f]; ok {
		return data, nil
	}
	data, err := funcObject(f)
	if err == nil {
		o[f] = data
	}
	return data, err
}

// funcObject is f written as an object of one function: its IR and every
// symbol, type and string it reaches, a symbol by its name and linkage name.
func funcObject(f *ir.Func) ([]byte, error) {
	m := ir.NewModule("function")
	m.AddFunc(f)
	return ir.EncodeObject(m)
}

// linkedDifferences compares a lowering's modules, whole, with their copies
// linked by name, linked, module by module: the same functions and cells in
// the same order, less one whose linkage name an earlier module defines (the
// linker keeps the first, and ir.CheckLinkage has required two bodies of one
// name to be the same code), each function with the same name, linkage name
// and IR. It answers each difference, at most twenty.
func (o funcObjects) linkedDifferences(whole, linked []*ir.Module) []string {
	if len(whole) != len(linked) {
		return []string{fmt.Sprintf("%d modules linked, %d lowered", len(linked), len(whole))}
	}
	var out []string
	note := func(format string, args ...any) {
		if len(out) < 20 {
			out = append(out, fmt.Sprintf(format, args...))
		}
	}
	// definedBy is the module each linkage name's kept definition is in.
	definedBy := map[string]int{}
	keeps := func(i int, sym *ir.Symbol) bool {
		link := sym.Link()
		if link == "" {
			return true
		}
		if at, ok := definedBy[link]; ok && at != i {
			return false
		}
		definedBy[link] = i
		return true
	}
	for i, m := range whole {
		var funcs []*ir.Func
		for _, f := range m.Funcs() {
			if keeps(i, f.Sym()) {
				funcs = append(funcs, f)
			}
		}
		cells := 0
		for _, c := range m.Cells() {
			if keeps(i, c.Sym()) {
				cells++
			}
		}
		if got := len(linked[i].Cells()); got != cells {
			note("module %s: %d cells linked, %d lowered and kept", m.Name(), got, cells)
		}
		got := linked[i].Funcs()
		if len(got) != len(funcs) {
			note("module %s: %d functions linked, %d lowered and kept", m.Name(), len(got), len(funcs))
			continue
		}
		for j, f := range funcs {
			g := got[j]
			if f.Sym().Name() != g.Sym().Name() || f.Sym().Link() != g.Sym().Link() {
				note("module %s function %d: linked as %s (%q), lowered as %s (%q)",
					m.Name(), j, g.Sym().Name(), g.Sym().Link(), f.Sym().Name(), f.Sym().Link())
				continue
			}
			want, err := o.lowered(f)
			if err != nil {
				note("module %s function %s does not encode: %v", m.Name(), f.Sym().Name(), err)
				continue
			}
			have, err := funcObject(g)
			if err != nil {
				note("module %s function %s does not encode once linked: %v", m.Name(), g.Sym().Name(), err)
				continue
			}
			if !bytes.Equal(have, want) {
				note("module %s function %s (%q): its IR differs once linked", m.Name(), f.Sym().Name(), f.Sym().Link())
			}
		}
	}
	return out
}

// funcCount is how many functions mods define.
func funcCount(mods []*ir.Module) int {
	n := 0
	for _, m := range mods {
		n += len(m.Funcs())
	}
	return n
}
