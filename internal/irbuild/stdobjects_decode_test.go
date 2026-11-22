package irbuild

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/expectation"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/std"
)

// sharedSectionForTest is this process's bundle's shared section, which a
// process under go test always has (loadStdlibIndex encodes the lowering
// when it finds no bundle).
func sharedSectionForTest(t *testing.T) []byte {
	t.Helper()
	stdlibLowering()
	if stdBundleState.shared == nil {
		t.Fatal("this process has no std bundle's shared section")
	}
	return stdBundleState.shared
}

// TestStdObjects_AModuleDecodesWithWhatItNames decodes each shared module
// from a fresh read of the bundle's shared section and requires what was
// decoded to be the module and its dependencies at any depth, and to define
// every linkage name the decoded objects write that any module of the
// section defines, so a decoded module never calls into one that was not.
// It then decodes the modules one at a time in the reverse of the bundle's
// order, so each import cycle's members come before the modules they
// depend on, and requires every module to encode as it does decoded with
// all the others at once: which modules a process decodes first does not
// change a module.
func TestStdObjects_AModuleDecodesWithWhatItNames(t *testing.T) {
	shared := sharedSectionForTest(t)
	read := func() *stdObjects {
		o, err := readSharedSection(shared)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	all := read()
	bodies, deps := 0, 0
	for _, rec := range all.recs {
		bodies += len(rec.body)
		deps += len(rec.deps)
	}
	t.Logf("the shared section: %d bytes, %d of them its table of contents (%d entries naming %d dependencies), which reading the bundle reads, and %d its bodies",
		len(shared), len(shared)-bodies, len(all.recs), deps, bodies)
	if err := all.decodeAll(); err != nil {
		t.Fatal(err)
	}
	defined := map[string]bool{}
	for link := range all.linkedFuncs {
		defined[link] = true
	}
	for link := range all.linkedCells {
		defined[link] = true
	}
	withDeps := 0
	for _, rec := range all.recs {
		o := read()
		if err := o.decode(rec.name); err != nil {
			t.Fatalf("std/%s does not decode on its own: %v", rec.name, err)
		}
		want := map[string]bool{}
		var close func(r *stdModuleRecord)
		close = func(r *stdModuleRecord) {
			if want[r.name] {
				return
			}
			want[r.name] = true
			for _, d := range r.deps {
				close(o.recs[d])
			}
		}
		close(o.byName[rec.name])
		if len(want) > 1 {
			withDeps++
		}
		got := map[string]bool{}
		mine := map[string]bool{}
		for _, r := range o.recs {
			if r.decoded {
				got[r.name] = true
			}
		}
		if !maps.Equal(got, want) {
			t.Errorf("decoding std/%s decoded %v, not it and its dependencies %v", rec.name, sortedKeys(got), sortedKeys(want))
		}
		for link := range o.linkedFuncs {
			mine[link] = true
		}
		for link := range o.linkedCells {
			mine[link] = true
		}
		for m := range o.mods {
			for _, s := range ir.ModuleSymbols(m) {
				if link := s.Link(); link != "" && defined[link] && !mine[link] {
					t.Errorf("decoding std/%s decoded a module that names %s, which a module it did not decode defines", rec.name, link)
				}
			}
		}
	}
	if withDeps == 0 {
		t.Fatal("no module has a dependency, so the closure was not tested")
	}
	one := read()
	for i := len(one.recs) - 1; i >= 0; i-- {
		if err := one.decode(one.recs[i].name); err != nil {
			t.Fatal(err)
		}
	}
	for i, rec := range one.recs {
		a, b := rec.mod, all.recs[i].mod
		if (a == nil) != (b == nil) {
			t.Errorf("std/%s has IR decoded one way and not the other", rec.name)
			continue
		}
		if a == nil {
			continue
		}
		ea, err := ir.EncodeObject(a)
		if err != nil {
			t.Fatal(err)
		}
		eb, err := ir.EncodeObject(b)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(ea, eb) {
			t.Errorf("std/%s decoded after the modules that depend on it differs from it decoded with every module", rec.name)
		}
	}
}

// decodeModeEnv names the mode a child of
// TestStdObjects_DecodedWhenNeededLowersAsAllDecoded lowers in ("eager",
// "lazy"), and decodeOutEnv the file it writes to.
const (
	decodeModeEnv = "NOMI_IRBUILD_DECODE_MODE"
	decodeOutEnv  = "NOMI_IRBUILD_DECODE_OUT"
	// decodeProgramsEnv is the directory the children unpack each stored
	// program into, the same for both, since a program's IR names its files.
	decodeProgramsEnv = "NOMI_IRBUILD_DECODE_PROGRAMS"
)

// decodeCaseEntry is goldenCaseEntry with a stored program unpacked under
// decodeProgramsEnv's directory, in a directory named by its place in the
// set, so both children's lowerings name the same files.
func decodeCaseEntry(c expectation.Case, root string, i int) (string, error) {
	entryName, _, _ := strings.Cut(c.ID, "@")
	if c.Source == "" {
		return filepath.Join(root, filepath.FromSlash(entryName)), nil
	}
	dir := filepath.Join(os.Getenv(decodeProgramsEnv), fmt.Sprint(i))
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	files, err := unpackGoldenArchive(c.Source, dir, root)
	if err != nil {
		return "", err
	}
	if files[entryName] == "" {
		return "", fmt.Errorf("the stored archive has no entry %s", entryName)
	}
	return files[entryName], nil
}

// TestStdObjects_DecodedWhenNeededLowersAsAllDecoded lowers every program
// irbuild.expect records in two fresh processes over one cached bundle: one
// decoding each shared module's object when it restores the module, as
// every process does, and one decoding them all when it reads the bundle
// (stdDecodeEagerly). The children build the stdlib a module at a time
// (stdLoadChildEnv), as a process outside this package's tests does, so
// the first decodes part of it.
//
// Each program's lowering must be the same in both (decodeRecordLine): its
// own modules and the std instances and listed modules it links, each as an
// object, the shared modules it links (its closure, linkedShared), the
// declarations the walk from its own functions finds no body for (none,
// for a program that lowers), and the image that walk keeps
// (vm.PruneImage), which holds every shared std function the program calls
// and every impl a call through an interface can select. A read that missed
// a module the first process had not decoded, or a program that linked what
// the process happened to have decoded, would show there.
func TestStdObjects_DecodedWhenNeededLowersAsAllDecoded(t *testing.T) {
	if mode := os.Getenv(decodeModeEnv); mode != "" {
		decodeModeChild(t, mode)
		return
	}
	if testing.Short() {
		t.Skip("lowers every irbuild.expect program twice; -short")
	}
	cache, programs := t.TempDir(), t.TempDir()
	run := func(mode string) (head string, records map[string]string) {
		out := filepath.Join(t.TempDir(), "lowerings")
		cmd := exec.Command(os.Args[0], "-test.run=^TestStdObjects_DecodedWhenNeededLowersAsAllDecoded$", "-test.count=1")
		cmd.Env = append(os.Environ(), decodeModeEnv+"="+mode, decodeOutEnv+"="+out,
			stdLoadChildEnv+"=1", artifactcache.RootEnv+"="+cache, decodeProgramsEnv+"="+programs)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("the %s child failed: %v\n%s", mode, err, b)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("the %s child wrote nothing: %v", mode, err)
		}
		head, body, _ := strings.Cut(string(data), "\n")
		records = map[string]string{}
		for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
			id, rec, _ := strings.Cut(line, " ")
			records[id] = rec
		}
		return head, records
	}
	// The first child finds no bundle, lowers the stdlib and writes it with
	// its listed section; the two after read it.
	run("lazy")
	eagerHead, eager := run("eager")
	lazyHead, lazy := run("lazy")
	t.Logf("every module decoded: %s; decoded when needed: %s", eagerHead, lazyHead)
	if !strings.HasPrefix(eagerHead, "cache ") || !strings.HasPrefix(lazyHead, "cache ") {
		t.Fatalf("a child did not read the cached bundle (%q, %q)", eagerHead, lazyHead)
	}
	compared, refused := 0, 0
	for id, want := range eager {
		got, ok := lazy[id]
		switch {
		case !ok:
			t.Errorf("%s: lowered with every module decoded and not with modules decoded when needed", id)
		case got != want:
			t.Errorf("%s: lowered with modules decoded when needed it is %s, with every module decoded %s", id, got, want)
		case got == "refused":
			refused++
		default:
			compared++
		}
	}
	if len(lazy) != len(eager) {
		t.Errorf("%d programs lowered with modules decoded when needed, %d with every module decoded", len(lazy), len(eager))
	}
	if compared < len(eager)/2 {
		t.Fatalf("compared %d of %d programs (%d refused by the front end); the enumeration is wrong", compared, len(eager), refused)
	}
	t.Logf("%d programs lower to the same bytes with modules decoded when needed and with every module decoded; %d refused by the front end alike",
		compared, refused)
}

// decodeModeChild lowers every program irbuild.expect records and writes a
// line naming where its bundle came from and what the first program and
// then all of them decoded, then each program's id and record
// (decodeRecordLine).
func decodeModeChild(t *testing.T, mode string) {
	stdDecodeEagerly = mode == "eager"
	set := goldenSet(t, irbuildGoldenPopulation)
	root, err := expectation.Root()
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	first := ""
	for i, c := range set.Cases {
		path, err := decodeCaseEntry(c, root, i)
		if err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		p, err := Analyze(path)
		if err != nil {
			lines = append(lines, c.ID+" refused")
			continue
		}
		res, _, err := GenerateIR(p)
		if err != nil {
			lines = append(lines, fmt.Sprintf("%s error:%q", c.ID, err.Error()))
			continue
		}
		if first == "" {
			m, _, _ := StdObjectsDecodedForTest()
			first = fmt.Sprintf("%d modules after the first program", m)
		}
		rec, err := decodeRecordLine(res)
		if err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		lines = append(lines, c.ID+" "+rec)
	}
	m, f, b := StdObjectsDecodedForTest()
	head := fmt.Sprintf("%s %s, %d modules (%d functions, %d bytes) after all", stdBundleState.from, first, m, f, b)
	data := head + "\n" + strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(os.Getenv(decodeOutEnv), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// decodeRecordLine is what res's program is, in one line: the sums of its
// own modules and of the modules it links after the shared stdlib's, each
// as an object, the shared modules it links by name, the declarations the
// walk from its own functions finds no body for, and the sum of the image
// that walk keeps.
func decodeRecordLine(res *Result) (string, error) {
	sum := func(mods []*ir.Module) (string, error) {
		h := sha256.New()
		for _, m := range mods {
			obj, err := ir.EncodeObject(m)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(h, "%d:", len(obj))
			h.Write(obj)
		}
		return fmt.Sprintf("%x", h.Sum(nil)[:6]), nil
	}
	own, err := sum(res.IR)
	if err != nil {
		return "", err
	}
	linked, err := sum(res.irLibraries[res.sharedStd:])
	if err != nil {
		return "", err
	}
	var shared []string
	for _, m := range res.irLibraries[:res.sharedStd] {
		name, _ := std.ImageName(m.Name())
		shared = append(shared, name)
	}
	rec := fmt.Sprintf("own %s linked %s shared %d:%x", own, linked, len(shared), sha256.Sum256([]byte(strings.Join(shared, ","))))
	image, unretained, err := reachedImage(res)
	if err != nil {
		return "", err
	}
	for _, u := range unretained {
		rec += " unretained " + u
	}
	return rec + fmt.Sprintf(" image %x", sha256.Sum256(image))[:len(" image ")+12], nil
}

// reachedImage is the image the walk from every function of res's own
// modules keeps (vm.PruneImage), encoded, and the declarations that walk
// finds no body for; nil for a lowering with no module of its own.
func reachedImage(res *Result) ([]byte, []string, error) {
	if len(res.IR) == 0 {
		return nil, nil, nil
	}
	var roots []*ir.Func
	for _, m := range res.IR {
		roots = append(roots, m.Funcs()...)
	}
	mods := res.IRModules()
	_, unretained := vm.NewProgram(res.IR[0], mods[1:], io.Discard).Reached(roots, nil)
	var names []string
	for _, u := range unretained {
		names = append(names, u.Name)
	}
	_, pruned := vm.PruneImage(res.IR[0], mods, roots, nil)
	data, err := ir.EncodeImage(ir.Image{Modules: pruned, Entry: -1})
	return data, names, err
}

// sameImageDynamic is a program that holds, and boxes into an interface, a
// value of a type its file does not import: Json.to_dynamic answers a
// std/dynamic Dynamic, which only std/json imports.
const sameImageDynamic = `import std::json.Json

fn main()
    d = Json.to_dynamic(Json.Obj{"name" => Json.String("Ada")})
    dbg d
    shown: List<Debug> = [d]
    dbg shown
end
`

// sameImageEnv marks the child of
// TestStdObjects_AProgramBuildsTheSameImageInEveryProcess.
const sameImageEnv = "NOMI_IRBUILD_SAME_IMAGE_CHILD"

// TestStdObjects_AProgramBuildsTheSameImageInEveryProcess lowers programs
// in one fresh process that builds the stdlib a module at a time, then has
// the stdlib build every module (std.All), which decodes every shared
// module's object, and lowers them again: each program must link the same
// shared modules and keep the same image, byte for byte (reachedImage).
// The programs are the seven whose built images a process that decoded
// every module made larger (each boxes a std value into an interface), and
// sameImageDynamic, which holds a value of a module it does not import: the
// shared modules it links must include std/dynamic, and its image must run.
func TestStdObjects_AProgramBuildsTheSameImageInEveryProcess(t *testing.T) {
	root, err := expectation.Root()
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv(sameImageEnv) == "" {
		// The first child finds no bundle and lowers the stdlib, which
		// decodes every module, and writes the bundle the second reads.
		cache := t.TempDir()
		var b []byte
		for range 2 {
			cmd := exec.Command(os.Args[0], "-test.run=^TestStdObjects_AProgramBuildsTheSameImageInEveryProcess$", "-test.count=1", "-test.v")
			cmd.Env = append(os.Environ(), sameImageEnv+"=1", stdLoadChildEnv+"=1", artifactcache.RootEnv+"="+cache)
			if b, err = cmd.CombinedOutput(); err != nil {
				t.Fatalf("the child failed: %v\n%s", err, b)
			}
		}
		if !bytes.Contains(b, []byte("--- PASS")) {
			t.Fatalf("the child over the cached bundle compared nothing:\n%s", b)
		}
		t.Logf("the child:\n%s", b)
		return
	}
	dyn := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(dyn, []byte(sameImageDynamic), 0o644); err != nil {
		t.Fatal(err)
	}
	paths := []string{dyn}
	for _, rel := range []string{
		"internal/irbuild/testdata/seed_function_value.nomi",
		"internal/irbuild/testdata/std_iface_display.nomi",
		"internal/irbuild/testdata/std_iface_identity/main.nomi",
		"internal/irbuild/testdata/typed_literal.nomi",
		"tests/01-foundations/standard_streams/streams.nomi",
		"tests/16-concurrency/jobs/app.nomi",
		"tests/20-data-access/websocket_chat/chat.nomi",
	} {
		paths = append(paths, filepath.Join(root, filepath.FromSlash(rel)))
	}
	type lowered struct {
		shared []string
		image  []byte
		res    *Result
	}
	lower := func(path string) lowered {
		p, err := Analyze(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		res, _, err := GenerateIR(p)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		var shared []string
		for _, m := range res.irLibraries[:res.sharedStd] {
			name, _ := std.ImageName(m.Name())
			shared = append(shared, name)
		}
		image, unretained, err := reachedImage(res)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if len(unretained) > 0 {
			t.Errorf("%s: the walk finds no body for %v", path, unretained)
		}
		return lowered{shared, image, res}
	}
	first := make([]lowered, len(paths))
	for i, path := range paths {
		first[i] = lower(path)
	}
	if stdBundleState.from == "lowered" {
		t.Skip("this child lowered the stdlib, which decodes every module, and wrote the bundle the next child reads")
	}
	before, _, _ := StdObjectsDecodedForTest()
	std.All()
	stdlibLowering()
	after, _, _ := StdObjectsDecodedForTest()
	if after <= before {
		t.Fatalf("building every std module decoded no module (%d before, %d after), so nothing was compared", before, after)
	}
	for i, path := range paths {
		again := lower(path)
		if strings.Join(again.shared, ",") != strings.Join(first[i].shared, ",") {
			t.Errorf("%s links other shared modules once every module is decoded:\n  %v\nfirst %v", path, again.shared, first[i].shared)
		}
		if !bytes.Equal(again.image, first[i].image) {
			t.Errorf("%s keeps another image once every module is decoded (%d bytes, first %d)", path, len(again.image), len(first[i].image))
		}
	}
	t.Logf("%d programs: the same shared modules and image with %d shared modules decoded and with %d", len(paths), before, after)
	// The program holding a module's value it does not import links that
	// module, and its image, pruned, runs.
	d := first[0]
	if !slices.Contains(d.shared, "std/dynamic.nomi") {
		t.Errorf("a program that holds a Dynamic links %v, without std/dynamic", d.shared)
	}
	var roots []*ir.Func
	for _, m := range d.res.IR {
		roots = append(roots, m.Funcs()...)
	}
	entry, pruned := vm.PruneImage(d.res.IR[0], d.res.IRModules(), roots, nil)
	var out bytes.Buffer
	if _, err := vm.NewProgram(entry, pruned, &out).Run("main"); err != nil {
		t.Fatalf("the pruned image of a program that holds a Dynamic does not run: %v\n%s", err, out.String())
	}
	want := "dbg line 5: d = {\"name\": \"Ada\"}\ndbg line 7: shown = [{\"name\": \"Ada\"}]\n"
	if out.String() != want {
		t.Errorf("the pruned image of a program that holds a Dynamic printed\n%s\nnot\n%s", out.String(), want)
	}
}
