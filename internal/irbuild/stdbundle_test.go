package irbuild

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/ir"
)

// stdBundleOutEnv makes TestStdBundle_WrittenByAFreshProcess write a bundle
// there.
const stdBundleOutEnv = "NOMI_IRBUILD_STD_BUNDLE_OUT"

// freshStdBundle is a bundle, both sections, that a fresh process made
// with its stdlib at another path, so its positions name other files until
// the bundle renames them.
func freshStdBundle(t *testing.T) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "std.bundle")
	cmd := exec.Command(os.Args[0], "-test.run=^TestStdBundle_WrittenByAFreshProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(), stdBundleOutEnv+"="+out, "NOMI_STD_PATH="+t.TempDir())
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the child that writes the bundle failed: %v\n%s", err, b)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("the child wrote no bundle: %v", err)
	}
	return data
}

// TestStdBundle_WrittenByAFreshProcess is the child of freshStdBundle.
func TestStdBundle_WrittenByAFreshProcess(t *testing.T) {
	out := os.Getenv(stdBundleOutEnv)
	if out == "" {
		t.Skip("runs only as the child of freshStdBundle")
	}
	data, err := StdBundle()
	if err != nil {
		t.Fatalf("StdBundle: %v", err)
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestStdBundle_DecodesToTheLowering holds the bundle's shared section to the
// lowering it replaces: decoded in this process, the bundle a fresh process
// made, with std at another path (NOMI_STD_PATH), must hold the modules
// this process's lowering makes, byte for byte as an ir image, and the same
// index printed by reflection: every view, declaration, `once`, overload
// set and table, each IR function named by its module and place. So a
// stdFunc field that lowering sets and the bundle drops fails it.
//
// Every process under `go test` runs on a decoded bundle: one that lowers
// the stdlib encodes the lowering and decodes it before use
// (loadStdlibIndex, keptListedStd), so every test and expectation suite
// runs on decoded std. TestStdBundle_ListedDecodesToTheLowering holds the
// listed section to this process's lowering of every listed module linked
// by name, and TestStdlibLowersTheSameWithoutLint runs over a decoded
// bundle too. TestStdBundle_RejectsAStaleOrCorruptBundle requires refusal
// of a truncated or changed bundle, one made for another version, stdlib,
// host registry or fixtures setting, and one whose modules or declarations
// do not match the process's; TestStdBundle_TheCacheRewritesABundleItCannotUse
// requires a corrupt cached one to be lowered past and written again.
// internal/artifactcache's tests hold that a test never writes the user's
// cache.
func TestStdBundle_DecodesToTheLowering(t *testing.T) {
	if os.Getenv(stdBundleOutEnv) != "" {
		t.Skip("the child writes the bundle")
	}
	decoded, err := decodeStdBundle(freshStdBundle(t))
	if err != nil {
		t.Fatalf("the bundle a fresh process wrote does not decode: %v", err)
	}
	lowered := buildStdlibIndex()
	if got, want := stdModulesImage(t, decoded.sortedModules()), stdModulesImage(t, lowered.sortedModules()); !bytes.Equal(got, want) {
		t.Errorf("the decoded modules differ from the lowered ones (%d bytes as an image against %d)", len(got), len(want))
	}
	if got, want := stdIndexDump(decoded), stdIndexDump(lowered); got != want {
		t.Errorf("the decoded index differs from the lowered one:\n%s", dumpDifference(got, want))
	}
	if len(decoded.irModules) == 0 || len(decoded.byKey) == 0 {
		t.Fatalf("the decoded index is empty, so the comparison compared nothing")
	}
}

// TestStdBundle_ListedDecodesToTheLowering holds the bundle's listed section
// to the lowering of every listed module it replaces: decoded against this
// process's shared lowering, the section a fresh process made must hold the
// modules this process lowers, linked by name as objects are
// (relinkThroughObjects: two instance modules that define one instance keep
// one body), each byte for byte as an object, with the same needs, literal
// sites, refusals and refused impl blocks.
func TestStdBundle_ListedDecodesToTheLowering(t *testing.T) {
	if os.Getenv(stdBundleOutEnv) != "" {
		t.Skip("the child writes the bundle")
	}
	_, listed, err := splitStdBundle(freshStdBundle(t))
	if err != nil {
		t.Fatal(err)
	}
	if listed == nil {
		t.Fatal("the bundle has no listed section")
	}
	decoded, err := decodeListedSection(listed)
	if err != nil {
		t.Fatalf("the listed section a fresh process wrote does not decode: %v", err)
	}
	lowered, _ := lowerListedStd()
	if lowered.all.err != nil {
		t.Fatal(lowered.all.err)
	}
	relinkListedByName(t, lowered)
	if got, want := listedDump(t, decoded), listedDump(t, lowered); got != want {
		t.Errorf("the decoded listed lowering differs from this process's:\n%s", dumpDifference(got, want))
	}
	if len(decoded.all.units) == 0 || len(decoded.all.mods) == len(decoded.all.units) {
		t.Fatalf("the decoded lowering has %d units and %d modules, so the comparison compared little",
			len(decoded.all.units), len(decoded.all.mods))
	}
}

// relinkListedByName replaces k's modules with their copies encoded as
// objects and linked by name against this process's shared lowering, and
// each literal site's cell and renderer with theirs.
func relinkListedByName(t *testing.T, k *listedStd) {
	t.Helper()
	l := ir.NewLinker()
	if err := l.Add(stdlibLowering().sortedModules()...); err != nil {
		t.Fatal(err)
	}
	funcs := map[*ir.Func]*ir.Func{}
	cells := map[*ir.Cell]*ir.Cell{}
	needs := map[*ir.Module][]string{}
	for i, m := range k.all.mods {
		data, err := ir.EncodeObject(m)
		if err != nil {
			t.Fatal(err)
		}
		im, err := ir.DecodeImage(data, ir.WithLinkedSymbols(l.Symbol))
		if err != nil {
			t.Fatal(err)
		}
		dm := im.Modules[0]
		for j, f := range m.Funcs() {
			funcs[f] = dm.Funcs()[j]
		}
		for j, c := range m.Cells() {
			cells[c] = dm.Cells()[j]
		}
		l.Define(dm)
		needs[dm] = k.needs[m]
		k.all.mods[i] = dm
	}
	k.needs = needs
	for i := range k.all.literals {
		s := &k.all.literals[i]
		s.Cell = cells[s.Cell]
		if s.Render != nil {
			s.Render = funcs[s.Render]
		}
	}
}

// listedDump prints a listed lowering: each module as an object, its needs,
// and the rest by reflection, a cell or function named by its module and
// place.
func listedDump(t *testing.T, k *listedStd) string {
	t.Helper()
	var b strings.Builder
	names := map[uintptr]string{}
	for i, m := range k.all.mods {
		data, err := ir.EncodeObject(m)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "module %d %s needs %v object %x\n", i, m.Name(), k.needs[m], data)
		for j, f := range m.Funcs() {
			names[reflect.ValueOf(f).Pointer()] = fmt.Sprintf("func %d#%d", i, j)
		}
		for j, c := range m.Cells() {
			names[reflect.ValueOf(c).Pointer()] = fmt.Sprintf("cell %d#%d", i, j)
		}
	}
	val := reflectDump(names)
	fmt.Fprintf(&b, "units %d\n", len(k.all.units))
	fmt.Fprintf(&b, "literals %s\n", val(reflect.ValueOf(k.all.literals)))
	fmt.Fprintf(&b, "unsupported %s\n", val(reflect.ValueOf(k.all.unsupported)))
	fmt.Fprintf(&b, "masked %s\n", val(reflect.ValueOf(k.all.masked)))
	fmt.Fprintf(&b, "synthRefused %s\n", val(reflect.ValueOf(k.all.synthRefused)))
	fmt.Fprintf(&b, "defined %d own %d\n", len(k.all.defined), len(k.all.own))
	return strings.ReplaceAll(b.String(), "; ", ";\n  ")
}

// TestStdBundle_RejectsAStaleOrCorruptBundle requires every bundle that
// does not match this process to be refused rather than decoded.
func TestStdBundle_RejectsAStaleOrCorruptBundle(t *testing.T) {
	var recs []*stdModuleRecord
	buildStdlibIndexBy(lowerSharedStdModule, func(name string, v *stdModuleView, m *ir.Module) {
		recs = append(recs, recordStdModule(name, v, m))
	})
	shared, err := encodeSharedSection(recs)
	if err != nil {
		t.Fatal(err)
	}
	head := thisStdBundleHead()
	good := assembleStdBundle(head, shared, nil)
	if _, err := decodeStdBundle(good); err != nil {
		t.Fatalf("the unchanged bundle does not decode, so nothing below is tested: %v", err)
	}
	reencode := func(recs []*stdModuleRecord, head stdBundleHead) []byte {
		shared, err := encodeSharedSection(recs)
		if err != nil {
			t.Fatal(err)
		}
		return assembleStdBundle(head, shared, nil)
	}
	// Each case reads its records again, since a case may change them.
	fresh := func() []*stdModuleRecord {
		recs, err := readSharedSection(shared)
		if err != nil {
			t.Fatal(err)
		}
		return recs
	}
	flipped := append([]byte(nil), good...)
	flipped[len(flipped)-10] ^= 0x40
	cases := map[string][]byte{
		"empty":                               nil,
		"truncated":                           good[:len(good)/2],
		"a byte changed after it was written": flipped,
		"another bundle version":              reencode(fresh(), func() stdBundleHead { h := head; h.version++; return h }()),
		"another IR format":                   reencode(fresh(), func() stdBundleHead { h := head; h.format++; return h }()),
		"another stdlib":                      reencode(fresh(), func() stdBundleHead { h := head; h.stdDigest = "0000000000000000"; return h }()),
		"other host functions":                reencode(fresh(), func() stdBundleHead { h := head; h.hostDigest = "00000000"; return h }()),
		"the other fixtures setting":          reencode(fresh(), func() stdBundleHead { h := head; h.fixtures = !h.fixtures; return h }()),
		"a declaration renamed": func() []byte {
			recs := fresh()
			for _, rec := range recs {
				if len(rec.cands) > 0 {
					rec.cands[0].key += "_renamed"
					break
				}
			}
			return reencode(recs, head)
		}(),
		"a once renamed": func() []byte {
			recs := fresh()
			for _, rec := range recs {
				if len(rec.onces) > 0 {
					rec.onces[0].key += "_renamed"
					break
				}
			}
			return reencode(recs, head)
		}(),
		"two modules swapped": func() []byte {
			recs := fresh()
			recs[0], recs[1] = recs[1], recs[0]
			return reencode(recs, head)
		}(),
		"a module missing": func() []byte {
			recs := fresh()
			return reencode(recs[:len(recs)-1], head)
		}(),
		"a declaration missing": func() []byte {
			recs := fresh()
			for _, rec := range recs {
				if len(rec.cands) > 1 {
					rec.cands = rec.cands[1:]
					break
				}
			}
			return reencode(recs, head)
		}(),
	}
	for name, data := range cases {
		if _, err := decodeStdBundle(data); err == nil {
			t.Errorf("%s: the bundle decoded", name)
		}
	}
	// A listed section is checked when it is decoded, so a corrupt one costs
	// only itself: the shared section still decodes.
	all, needs := lowerListedStd()
	if _, err := encodeListedSection(all, nil); err == nil {
		t.Fatal("a listed section encoded with no needs for its instance modules")
	}
	listed, err := encodeListedSection(all, needs)
	if err != nil {
		t.Fatal(err)
	}
	framed := frameSection(listed)
	if _, err := decodeListedSection(framed); err != nil {
		t.Fatalf("the unchanged listed section does not decode: %v", err)
	}
	bad := append([]byte(nil), framed...)
	bad[len(bad)/2] ^= 0x40
	withBad := assembleStdBundle(head, shared, nil)
	withBad = append(withBad[:len(withBad)-1], 1)
	withBad = append(withBad, bad...)
	_, gotListed, err := splitStdBundle(withBad)
	if err != nil {
		t.Fatalf("a bundle whose listed section is corrupt does not split: %v", err)
	}
	if _, err := decodeListedSection(gotListed); err == nil {
		t.Error("a corrupt listed section decoded")
	}
	if _, err := decodeListedSection(framed[:len(framed)-3]); err == nil {
		t.Error("a truncated listed section decoded")
	}
}

// TestStdBundle_TheCacheRewritesABundleItCannotUse runs loadStdlibIndex
// over a cache holding a corrupt bundle: the process lowers the stdlib and
// writes a good bundle in its place, which the next process decodes.
func TestStdBundle_TheCacheRewritesABundleItCannotUse(t *testing.T) {
	t.Setenv(artifactcache.RootEnv, t.TempDir())
	path := stdBundlePath()
	if path == "" {
		t.Fatal("this test binary has no bundle path, so the cache is not used under go test")
	}
	if err := artifactcache.WriteFile(path, []byte("not a bundle")); err != nil {
		t.Fatal(err)
	}
	saved := stdBundleState
	defer func() { stdBundleState = saved }()
	if loadStdlibIndex(); stdBundleState.from != "lowered" {
		t.Fatalf("a corrupt cached bundle was used (%s)", stdBundleState.from)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the lowering wrote no bundle: %v", err)
	}
	if _, err := decodeStdBundle(data); err != nil {
		t.Fatalf("the bundle written in place of the corrupt one does not decode: %v", err)
	}
	if loadStdlibIndex(); stdBundleState.from != "cache" {
		t.Fatalf("the next load did not read the cached bundle (%s)", stdBundleState.from)
	}
}

// dumpDifference names the first line where two dumps differ.
func dumpDifference(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(g) && i < len(w); i++ {
		if g[i] != w[i] {
			return fmt.Sprintf("line %d:\n decoded %s\n lowered %s", i+1, g[i], w[i])
		}
	}
	return fmt.Sprintf("decoded %d lines, lowered %d", len(g), len(w))
}

// stdModulesImage is mods as one ir image.
func stdModulesImage(t *testing.T, mods []*ir.Module) []byte {
	t.Helper()
	data, err := ir.EncodeImage(ir.Image{Modules: mods, Entry: -1})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// stdIndexDump prints everything idx holds, by reflection, so a field added
// to the index or its declarations is compared without this test knowing of
// it. An IR function, cell or module is named by its package and place, a
// view, declaration or `once` by its module and place in the view, and any
// other pointer by its address: two indexes built in one process share
// std's analysis, so its nodes and type declarations are the same pointers.
func stdIndexDump(idx *stdlibIndex) string {
	names := map[uintptr]string{}
	pkgs := make([]string, 0, len(idx.irModules))
	for pkg := range idx.irModules {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)
	for _, pkg := range pkgs {
		m := idx.irModules[pkg]
		names[reflect.ValueOf(m).Pointer()] = "module " + pkg
		for i, f := range m.Funcs() {
			names[reflect.ValueOf(f).Pointer()] = fmt.Sprintf("func %s#%d", pkg, i)
			names[reflect.ValueOf(f.Sym()).Pointer()] = fmt.Sprintf("sym %s#%d", pkg, i)
		}
		for i, c := range m.Cells() {
			names[reflect.ValueOf(c.Sym()).Pointer()] = fmt.Sprintf("cell %s#%d", pkg, i)
		}
	}
	modules := make([]string, 0, len(idx.views))
	for module := range idx.views {
		modules = append(modules, module)
	}
	sort.Strings(modules)
	for _, module := range modules {
		v := idx.views[module]
		names[reflect.ValueOf(v).Pointer()] = "view " + module
		for i, c := range v.cands {
			names[reflect.ValueOf(c).Pointer()] = fmt.Sprintf("cand %s#%d", module, i)
			names[reflect.ValueOf(c.f).Pointer()] = fmt.Sprintf("func %s#%d", module, i)
		}
		for i, o := range v.onces {
			names[reflect.ValueOf(o).Pointer()] = fmt.Sprintf("once %s#%d", module, i)
		}
	}
	val := reflectDump(names)
	var b strings.Builder
	for _, module := range modules {
		v := idx.views[module]
		fmt.Fprintf(&b, "view %s %s\n", module, val(reflect.ValueOf(v).Elem()))
		for i, c := range v.cands {
			fmt.Fprintf(&b, "cand %s#%d %s\n", module, i, val(reflect.ValueOf(c).Elem()))
			fmt.Fprintf(&b, "func %s#%d %s\n", module, i, val(reflect.ValueOf(c.f).Elem()))
		}
		for i, o := range v.onces {
			fmt.Fprintf(&b, "once %s#%d %s\n", module, i, val(reflect.ValueOf(o).Elem()))
		}
	}
	fmt.Fprintf(&b, "index %s\n", val(reflect.ValueOf(idx).Elem()))
	return strings.ReplaceAll(b.String(), "; ", ";\n  ")
}

// reflectDump prints a value by reflection: a pointer names holds by its
// name, a stdFunc or stdOnce it does not by its fields, and any other
// pointer by its address, without following it.
func reflectDump(names map[uintptr]string) func(reflect.Value) string {
	var val func(v reflect.Value) string
	val = func(v reflect.Value) string {
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return "nil"
			}
			if name, ok := names[v.Pointer()]; ok {
				return "<" + name + ">"
			}
			if v.Type() == reflect.TypeFor[*stdFunc]() || v.Type() == reflect.TypeFor[*stdOnce]() {
				return "orphan" + val(v.Elem())
			}
			return fmt.Sprintf("%#x", v.Pointer())
		case reflect.Interface:
			if v.IsNil() {
				return "nil"
			}
			return val(v.Elem())
		case reflect.Struct:
			var s strings.Builder
			s.WriteString(v.Type().Name() + "{")
			for i := range v.NumField() {
				fmt.Fprintf(&s, "%s: %s; ", v.Type().Field(i).Name, val(v.Field(i)))
			}
			return s.String() + "}"
		case reflect.Slice:
			if v.IsNil() {
				return "nil"
			}
			fallthrough
		case reflect.Array:
			parts := make([]string, v.Len())
			for i := range parts {
				parts[i] = val(v.Index(i))
			}
			return "[" + strings.Join(parts, ", ") + "]"
		case reflect.Map:
			if v.IsNil() {
				return "nil"
			}
			parts := make([]string, 0, v.Len())
			for it := v.MapRange(); it.Next(); {
				parts = append(parts, val(it.Key())+": "+val(it.Value()))
			}
			sort.Strings(parts)
			return "map[" + strings.Join(parts, ", ") + "]"
		case reflect.Func:
			if v.IsNil() {
				return "nil"
			}
			return "func"
		case reflect.String:
			return fmt.Sprintf("%q", v.String())
		case reflect.Bool:
			return fmt.Sprint(v.Bool())
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return fmt.Sprint(v.Int())
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			return fmt.Sprint(v.Uint())
		}
		return "<" + v.Kind().String() + ">"
	}
	return val
}
