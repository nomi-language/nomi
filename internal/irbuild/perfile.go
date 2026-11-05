package irbuild

// Lowering one file of a program against the kept objects of the others.
//
// A lowering of the whole program can be kept (Result.Keep): each of its
// files' modules as an object (ir.EncodeObject), its std instances, and
// what the lowering of a single file needs to know of the program it does
// not read: which listed std modules it links, the instances some file
// holds erased and the enums some file boxes (typeRegistry.erasedInsts and
// boxedEnums, by spelling), the synthesized impls its speculation refused,
// whether the entry starts an app, the linkage names some body names and
// no module defines, and a fingerprint of every top-level declaration of
// each file.
//
// LowerFile lowers one file F of the program again, from F's check against
// its imports' interfaces (analysis.ProgramInterfaces.Check), and links
// it with the kept objects of every other file. The program it is given is
// F, first, and the files F's check read, declared as a whole lowering
// declares them, but each of them is `fromObjects`: nothing of it is
// lowered, since its nodes are its interface's, and every table resolves a
// linkage name the kept objects define to their symbol, as a program
// resolves the listed std modules it links (programstdlink.go). The
// registry's program-wide sets are the kept ones, so F records the
// dispatch entries other files' boxing asks of it. What F's module held
// in the whole lowering that F's own bodies do not make (an instance of
// its generic function another file asked for) is carried from the kept
// object when the declaration it was made from is unchanged.
//
// It answers a *FileMiss, and the caller lowers the whole program, when
// the result could differ from a lowering of the whole program: F's
// lowering declined a body or refused a construct, names a function no
// object defines and the whole program did not name, asks another file
// to hold an instance erased or box an enum the whole program did not,
// or lost something it held that cannot be carried. The object harness
// (`NOMI_LINK_OBJECTS=perfile`, perfileharness.go) relowers every file of
// every program it lowers this way and requires each file's module to
// encode as the whole lowering's did.

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"slices"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ast/astcodec"
	"github.com/nomi-language/nomi/internal/ir"
)

// KeptLowering is a whole program's lowering kept for lowering its files
// one at a time (LowerFile).
type KeptLowering struct {
	// EntryPath is the entry's module name, its path.
	EntryPath string
	HasMain   bool
	// EntryBoots says the entry starts an app: a program boot, or a boot
	// in one of its test groups.
	EntryBoots bool
	// Units are the program's modules in unit order: its files, then the
	// listed std modules whose gens lowered instances for it.
	Units []KeptUnit
	// Instances are the program's std instance modules, as objects, and
	// InstanceFiles the files their positions are in.
	Instances     [][]byte
	InstanceFiles []string
	// Listed are the listed std modules the program links.
	Listed []string
	// Erased and Boxed are the registry's erasedInsts and boxedEnums.
	Erased, Boxed []string
	// SynthRefused names each synthesized impl block whose speculative
	// lowering failed.
	SynthRefused []string
	// Dangling are the linkage names a module names that no module
	// defines: an interface method, a declined body's callee.
	Dangling []string
	// Declines and Refusals are the lowering's declines and refused
	// constructs, by the path of the file each is in: a lowering of one
	// file must make the same ones in it.
	Declines, Refusals map[string][]string
}

// KeptUnit is one module of a kept lowering.
type KeptUnit struct {
	// Key is the module key ("billing/invoice", "std/http") and Path its
	// module name.
	Key, Path string
	// Object is the module as an object, nil when the unit retained
	// nothing.
	Object []byte
	// Decls fingerprints each top-level declaration of a project file by
	// its declaration name: its nodes, positions included.
	Decls map[string][32]byte
	// Files are the files the object's positions are in.
	Files []string
}

// FileMiss is LowerFile's answer when it cannot lower the file on its
// own: the caller lowers the whole program. Undefined, when set, is the
// linkage name the file's lowering named and no module defines: an
// instance of another file's generic declaration no kept object holds,
// which a lowering that reads that file with its bodies can make.
type FileMiss struct{ Why, Undefined string }

func (m *FileMiss) Error() string { return "irbuild: the file cannot be lowered on its own: " + m.Why }

func miss(format string, args ...any) error { return &FileMiss{Why: fmt.Sprintf(format, args...)} }

// perFileState is what a lowering of one file is given of its program.
type perFileState struct {
	// isEntry says the file is the program's entry; entryBoots is
	// KeptLowering.EntryBoots.
	isEntry, entryBoots bool
	erased, boxed       []string
}

// entryFirst reports whether the first module is the program's entry: in
// a lowering of one file, only when that file is.
func (p *Program) entryFirst() bool { return p.perFile == nil || p.perFile.isEntry }

func setOf(list []string) map[string]bool {
	if len(list) == 0 {
		return nil
	}
	out := make(map[string]bool, len(list))
	for _, s := range list {
		out[s] = true
	}
	return out
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// keptState is what Keep reads of a whole lowering.
type keptState struct {
	entryPath    string
	entryBoots   bool
	units        []keptUnitState
	listed       []string
	erased       []string
	boxed        []string
	synthRefused []string
	// why says why the lowering cannot be kept, "" when it can.
	why string
}

type keptUnitState struct {
	key, path string
	nodes     []ast.Node
	mod       *ir.Module
	project   bool
}

// keptStateOf records what Keep needs of a lowering of p, whose first own
// modules are its files.
func keptStateOf(p *Program, own int, gens []*gen, reg *typeRegistry, linked *linkedStd, cases bool) *keptState {
	if p.objects != nil {
		return nil
	}
	k := &keptState{entryPath: p.Modules[0].Path}
	switch {
	case cases:
		k.why = "a std module's cases"
	case p.noLinkStd:
		k.why = "the kept lowering of the listed std modules"
	}
	if len(gens) > 0 && gens[0] != nil {
		k.entryBoots = gens[0].irDeclaresBoot()
	}
	for i := range p.Modules {
		m := &p.Modules[i]
		if m.twins != nil {
			k.why = "a std module checked from edited source"
		}
		if i >= own && linked == nil {
			k.why = "listed std modules lowered with the program"
		}
		project := i < own && !strings.HasPrefix(m.Name, "std/")
		k.units = append(k.units, keptUnitState{key: m.Name, path: m.Path, nodes: m.Nodes, mod: gens[i].irMod, project: project})
		if !project {
			continue
		}
		for _, d := range gens[i].implOrder {
			if d.synth && !d.lowerable && d.why == "impl block" && d.decl != nil {
				if name, ok := reg.linkNames[d.decl]; ok {
					k.synthRefused = append(k.synthRefused, name.String())
				}
			}
		}
	}
	if linked != nil {
		k.listed = linkedStdNames(p, own)
	}
	k.erased, k.boxed = sortedSet(reg.erasedInsts), sortedSet(reg.boxedEnums)
	sort.Strings(k.synthRefused)
	return k
}

// Keep is the lowering kept for lowering its files one at a time, or an
// error saying why it cannot be: it was not a whole program's lowering, or
// it holds what a file's lowering on its own does not reproduce (typed
// literals checked at load, exported functions, Go bindings).
func (r *Result) Keep() (*KeptLowering, error) {
	k := r.kept
	switch {
	case k == nil:
		return nil, errors.New("irbuild: not a whole program's lowering")
	case k.why != "":
		return nil, errors.New("irbuild: " + k.why)
	case len(r.Literals) > 0:
		return nil, errors.New("irbuild: the program checks typed literals at load")
	case len(r.Exports) > 0:
		return nil, errors.New("irbuild: the program exports functions")
	case len(r.irHostKeys) > 0:
		return nil, errors.New("irbuild: the program has Go bindings")
	}
	out := &KeptLowering{EntryPath: k.entryPath, HasMain: r.HasMain, EntryBoots: k.entryBoots,
		Listed: k.listed, Erased: k.erased, Boxed: k.boxed, SynthRefused: k.synthRefused,
		Declines: r.declines, Refusals: r.refusals}
	for _, u := range k.units {
		ku := KeptUnit{Key: u.key, Path: u.path}
		if u.mod != nil {
			data, files, err := encodeWithFiles(u.mod)
			if err != nil {
				return nil, err
			}
			ku.Object, ku.Files = data, files
		}
		if u.project {
			ku.Decls = DeclFingerprints(u.nodes)
		}
		out.Units = append(out.Units, ku)
	}
	files := map[string]bool{}
	for _, m := range r.instMods {
		data, fs, err := encodeWithFiles(m)
		if err != nil {
			return nil, err
		}
		out.Instances = append(out.Instances, data)
		for _, f := range fs {
			files[f] = true
		}
	}
	out.InstanceFiles = sortedSet(files)
	out.Dangling = danglingLinks(r.IRModules())
	return out, nil
}

// encodeWithFiles is m as an object and the files its positions are in.
func encodeWithFiles(m *ir.Module) ([]byte, []string, error) {
	files := map[string]bool{}
	data, err := ir.EncodeObject(m, ir.WithFileNames(func(name string) string {
		files[name] = true
		return name
	}))
	return data, sortedSet(files), err
}

// danglingLinks are the linkage names mods name and do not define, sorted.
func danglingLinks(mods []*ir.Module) []string {
	defined := map[string]bool{}
	for _, m := range mods {
		for _, l := range definedLinks(m) {
			defined[l] = true
		}
	}
	named := map[string]bool{}
	for _, m := range mods {
		for _, s := range ir.ModuleSymbols(m) {
			if s.Link() != "" && !defined[s.Link()] {
				named[s.Link()] = true
			}
		}
	}
	return sortedSet(named)
}

// definedLinks are the linkage names of m's functions and cells.
func definedLinks(m *ir.Module) []string {
	var out []string
	for _, f := range m.Funcs() {
		if f.Sym() != nil && f.Sym().Link() != "" {
			out = append(out, f.Sym().Link())
		}
	}
	for _, c := range m.Cells() {
		if c.Sym().Link() != "" {
			out = append(out, c.Sym().Link())
		}
	}
	return out
}

// DeclFingerprints fingerprints each top-level declaration of nodes by its
// declaration name (analysis.DeclarationNames): a hash of its encoding,
// positions included, so a declaration that moved is a changed one.
func DeclFingerprints(nodes []ast.Node) map[string][32]byte {
	out := map[string][32]byte{}
	names := analysis.DeclarationNames(nodes)
	for _, n := range nodes {
		name, ok := names[n]
		if !ok {
			continue
		}
		out[name] = sha256.Sum256(astcodec.Encode([]ast.Node{n}))
	}
	return out
}

// LowerFile lowers p's first module, a file of the program kept lowered,
// on its own, and links it with the kept objects of the program's other
// modules into the program a whole lowering makes. p's other modules are
// the files the first one's check against interfaces read, as that check
// left them. moved says the file's declarations are not where they were
// when it was kept (its interface holds other positions): another
// module's object that holds a position in the file was lowered from the
// old ones, and is a miss. A *FileMiss says the file cannot be lowered on
// its own.
func LowerFile(p *Program, kept *KeptLowering, moved bool) (res *Result, err error) {
	return LowerFileAmong(p, kept, moved, nil)
}

// LowerFileAmong is LowerFile for one of several files lowered on their
// own after one edit: relowering holds the paths of the others, true for
// those not lowered yet. An object of one of them that holds a position in
// a file that moved is not a miss, since each is lowered against the new
// positions. What the file's old object held that was made from a
// declaration that changed (a default accessor of a parameter it no
// longer has, an impl it no longer declares) is dropped rather than a
// miss, when no kept object of a file outside the edit, or of one already
// lowered again, names it; a file still to be lowered is checked when its
// turn comes. A file the program's check reads with
// its bodies (analysis.ProgramInterfaces.SetBodies) is lowered as a linked
// library is, so the file can ask it for an instance of a generic
// declaration no kept object holds; Rekeep keeps what it lowered.
func LowerFileAmong(p *Program, kept *KeptLowering, moved bool, relowering map[string]bool) (res *Result, err error) {
	if len(p.Modules) == 0 {
		return nil, miss("no file")
	}
	// Each file is named as the whole lowering named it, which its linkage
	// names spell: a check against interfaces keys the entry by its path
	// under the root, a whole lowering by its base name.
	byPath := map[string]int{}
	for i, u := range kept.Units {
		byPath[u.Path] = i
	}
	for i := range p.Modules {
		if j, ok := byPath[p.Modules[i].Path]; ok {
			p.Modules[i].Name = kept.Units[j].Key
		}
	}
	f := &p.Modules[0]
	old := -1
	if j, ok := byPath[f.Path]; ok {
		old = j
	}
	if old < 0 {
		return nil, miss("%s is not a file of the kept program", f.Name)
	}
	if moved {
		for i, u := range kept.Units {
			if i != old && !relowering[u.Path] && slices.Contains(u.Files, f.Path) {
				return nil, miss("%s's declarations moved, and %s's object holds positions in it", f.Name, u.Key)
			}
		}
		if slices.Contains(kept.InstanceFiles, f.Path) {
			return nil, miss("%s's declarations moved, and a std instance holds positions in it", f.Name)
		}
	}
	std := stdlibLowering()
	var ls *linkedStd
	if len(kept.Listed) > 0 {
		if ls = linkedStdFor(kept.Listed); ls == nil || ls.err != nil {
			return nil, miss("the listed std modules are not linked")
		}
	}
	l := ir.NewLinker()
	l.Know(std.sortedModules()...)
	if ls != nil {
		l.Know(ls.mods...)
	}
	decoded := make([]*ir.Module, len(kept.Units))
	objects := &linkedStd{defined: map[string]*ir.Symbol{}, own: map[string]bool{}}
	for i, u := range kept.Units {
		if i == old || u.Object == nil {
			continue
		}
		m, err := decodeObject(l, u.Object)
		if err != nil {
			return nil, fmt.Errorf("irbuild: decoding %s's object: %w", u.Key, err)
		}
		decoded[i] = m
		objects.mods = append(objects.mods, m)
	}
	var instances []*ir.Module
	for _, data := range kept.Instances {
		m, err := decodeObject(l, data)
		if err != nil {
			return nil, fmt.Errorf("irbuild: decoding an instance object: %w", err)
		}
		instances = append(instances, m)
		objects.mods = append(objects.mods, m)
	}
	for _, m := range objects.mods {
		for _, s := range ir.ModuleSymbols(m) {
			if s.Link() != "" {
				objects.defined[s.Link()] = s
			}
		}
		for _, link := range definedLinks(m) {
			objects.own[link] = true
		}
	}
	refused := setOf(kept.SynthRefused)
	objects.synthRefused = map[*ast.ImplBlock]bool{}
	for n, name := range programLinkNames(p) {
		if b, ok := n.(*ast.ImplBlock); ok && refused[name.String()] {
			objects.synthRefused[b] = true
		}
	}
	p.objects = objects
	p.perFile = &perFileState{isEntry: f.Path == kept.EntryPath, entryBoots: kept.EntryBoots, erased: kept.Erased, boxed: kept.Boxed}

	whole, _, err := GenerateIR(p)
	if err != nil {
		return nil, err
	}
	// The file declines and refuses what it did in the whole lowering;
	// every other file's declines are among the whole lowering's (a std
	// instance the file asks for declines as it did for another).
	for path, got := range whole.declines {
		if path == f.Path {
			if !slices.Equal(got, kept.Declines[path]) {
				return nil, miss("the file declines %v, the whole lowering %v", got, kept.Declines[path])
			}
		} else if extra := missing(got, kept.Declines[path]); extra != "" {
			return nil, miss("the file's lowering declines %s in %s", extra, path)
		}
	}
	if len(whole.declines[f.Path]) == 0 && len(kept.Declines[f.Path]) > 0 {
		return nil, miss("the file declines nothing, and did in the whole lowering")
	}
	if got := whole.refusals[f.Path]; !slices.Equal(got, kept.Refusals[f.Path]) {
		return nil, miss("the file refuses %v, the whole lowering %v", got, kept.Refusals[f.Path])
	}
	if len(whole.Literals) > 0 || len(whole.Exports) > 0 || len(whole.irHostKeys) > 0 {
		return nil, miss("the file checks typed literals at load, exports a function or has Go bindings")
	}
	st := whole.perFileTables
	if extra := missing(st.erased, kept.Erased); extra != "" {
		return nil, miss("the file holds erased an instance no other file did: %s", extra)
	}
	if extra := missing(st.boxed, kept.Boxed); extra != "" {
		return nil, miss("the file boxes an enum no other file did: %s", extra)
	}

	var mod *ir.Module
	var others []*ir.Module
	for _, m := range whole.IR {
		if m.Name() == f.Path {
			mod = m
		} else {
			others = append(others, m)
		}
	}
	if kept.Units[old].Object != nil {
		if mod == nil {
			mod = ir.NewModule(f.Path)
		}
		// The new module's symbols are the ones its old object names.
		l.Know(mod)
		was, err := decodeObject(l, kept.Units[old].Object)
		if err != nil {
			return nil, fmt.Errorf("irbuild: decoding %s's object: %w", f.Name, err)
		}
		dropped, err := carryKept(mod, was, f.Name, kept.Units[old].Decls, DeclFingerprints(f.Nodes), relowering != nil)
		if err != nil {
			return nil, err
		}
		if len(dropped) > 0 {
			gone := setOf(dropped)
			for i, u := range kept.Units {
				if i == old || decoded[i] == nil || relowering[u.Path] {
					continue
				}
				for _, s := range ir.ModuleSymbols(decoded[i]) {
					if gone[s.Link()] {
						return nil, miss("%s names %s, which %s no longer makes", u.Key, s.Link(), f.Name)
					}
				}
			}
			for _, m := range instances {
				for _, s := range ir.ModuleSymbols(m) {
					if gone[s.Link()] {
						return nil, miss("an instance names %s, which %s no longer makes", s.Link(), f.Name)
					}
				}
			}
		}
	}
	if mod != nil && mod.Empty() {
		mod = nil
	}

	// Every name the new modules name is defined, or was named and not
	// defined by the whole lowering too.
	defined := map[string]bool{}
	all := append(append(append([]*ir.Module(nil), objects.mods...), whole.instMods...), others...)
	all = append(all, std.sortedModules()...)
	if ls != nil {
		all = append(all, ls.mods...)
	}
	if mod != nil {
		all = append(all, mod)
	}
	for _, m := range all {
		for _, link := range definedLinks(m) {
			defined[link] = true
		}
	}
	dangling := setOf(kept.Dangling)
	fresh := append(append([]*ir.Module(nil), whole.instMods...), others...)
	if mod != nil {
		fresh = append(fresh, mod)
	}
	for _, m := range fresh {
		for _, s := range ir.ModuleSymbols(m) {
			if link := s.Link(); link != "" && !defined[link] && !dangling[link] {
				return nil, &FileMiss{Why: fmt.Sprintf("%s names %s, which no module defines", m.Name(), link), Undefined: link}
			}
		}
	}

	units := make([]*ir.Module, 0, len(kept.Units)+len(others))
	for i := range kept.Units {
		switch {
		case i == old:
			if mod != nil {
				units = append(units, mod)
			}
		case decoded[i] != nil:
			units = append(units, decoded[i])
		}
	}
	units = append(units, others...)
	libs := append([]*ir.Module(nil), std.sortedModules()...)
	if ls != nil {
		libs = append(libs, ls.mods...)
	}
	insts := append(instances, whole.instMods...)
	libs = append(libs, insts...)
	res = &Result{HasMain: kept.HasMain, IR: units, irLibraries: libs, instMods: insts}
	if ls != nil {
		res.linkedMods = ls.units
	}
	res.file = &relowered{unit: old, mod: mod, instMods: whole.instMods, decls: DeclFingerprints(f.Nodes)}
	for _, m := range p.Modules[1:] {
		if m.bodies {
			res.file.linked = others
			break
		}
	}
	return res, nil
}

// relowered is what LowerFile made of its file, for Rekeep.
type relowered struct {
	unit     int
	mod      *ir.Module
	instMods []*ir.Module
	decls    map[string][32]byte
	// linked are the modules of the files read with their bodies, which
	// hold the instances the file asked them for.
	linked []*ir.Module
}

// Rekeep is kept with the file LowerFile relowered into res replaced by
// its new lowering, for lowering another file of the program on its own
// later.
func (r *Result) Rekeep(kept *KeptLowering) (*KeptLowering, error) {
	f := r.file
	if f == nil {
		return nil, errors.New("irbuild: not a lowering of one file")
	}
	out := *kept
	out.Units = append([]KeptUnit(nil), kept.Units...)
	u := out.Units[f.unit]
	u.Object, u.Decls, u.Files = nil, f.decls, nil
	if f.mod != nil {
		data, files, err := encodeWithFiles(f.mod)
		if err != nil {
			return nil, err
		}
		u.Object, u.Files = data, files
	}
	out.Units[f.unit] = u
	out.Instances = append([][]byte(nil), kept.Instances...)
	files := setOf(kept.InstanceFiles)
	for _, m := range append(append([]*ir.Module(nil), f.instMods...), f.linked...) {
		if m.Empty() {
			continue
		}
		data, fs, err := encodeWithFiles(m)
		if err != nil {
			return nil, err
		}
		out.Instances = append(out.Instances, data)
		for _, f := range fs {
			if files == nil {
				files = map[string]bool{}
			}
			files[f] = true
		}
	}
	out.InstanceFiles = sortedSet(files)
	return &out, nil
}

// WithDecls is kept with the declaration fingerprints of the files at the
// paths decls holds replaced by decls' (DeclFingerprints): a caller that
// kept a lowering whose nodes were rewritten since gives each changed
// file's fingerprints from its old text, so nothing made from a
// declaration that changed is carried.
func (kept *KeptLowering) WithDecls(decls map[string]map[string][32]byte) *KeptLowering {
	out := *kept
	out.Units = append([]KeptUnit(nil), kept.Units...)
	for i, u := range out.Units {
		if d, ok := decls[u.Path]; ok {
			out.Units[i].Decls = d
		}
	}
	return &out
}

// AddUnit gives kept a unit for a file the program did not hold, with key
// and path, which LowerFile then lowers as it lowers a file whose kept
// object retained nothing. It goes after the program's other files, before
// the listed std modules.
func (kept *KeptLowering) AddUnit(key, path string) {
	at := len(kept.Units)
	for i, u := range kept.Units {
		if analysis.IsStdlibKey(u.Key) {
			at = i
			break
		}
	}
	kept.Units = slices.Insert(kept.Units, at, KeptUnit{Key: key, Path: path})
}

// RemoveUnit takes the unit of the file at path out of kept, with its
// declines and refusals: the program no longer holds the file.
func (kept *KeptLowering) RemoveUnit(path string) {
	kept.Units = slices.DeleteFunc(kept.Units, func(u KeptUnit) bool { return u.Path == path })
	delete(kept.Declines, path)
	delete(kept.Refusals, path)
}

// missing is the first of have not in want, or "".
func missing(have, want []string) string {
	w := setOf(want)
	for _, s := range have {
		if !w[s] {
			return s
		}
	}
	return ""
}

// carryKept adds to mod, the new lowering of the file with module key key,
// what was, the whole lowering's module of it, holds and mod lacks: a
// function made from a declaration whose fingerprint is unchanged (an
// instance of a generic function another file asked for), and the entries
// that dispatch to what mod now holds. Anything else it lacks is a miss,
// unless drop allows one made from a declaration that changed or went,
// whose linkage name it answers.
func carryKept(mod, was *ir.Module, key string, oldDecls, newDecls map[string][32]byte, drop bool) (dropped []string, err error) {
	have := map[string]bool{}
	for _, link := range definedLinks(mod) {
		have[link] = true
	}
	unnamed := map[string]bool{}
	for _, fn := range mod.Funcs() {
		if fn.Sym() == nil || fn.Sym().Link() == "" {
			unnamed[fn.Name()] = true
		}
	}
	for _, fn := range was.Funcs() {
		link := ""
		if fn.Sym() != nil {
			link = fn.Sym().Link()
		}
		if link == "" {
			// Only its own module names it: the new lowering made it too,
			// or nothing of it can be carried.
			if !unnamed[fn.Name()] && !drop {
				return nil, miss("%s's lowering lacks %s", key, fn.Name())
			}
			continue
		}
		if have[link] {
			continue
		}
		decl, ok := templateDecl(link, key, oldDecls)
		if !ok {
			return nil, miss("%s's lowering lacks %s", key, link)
		}
		before, known := oldDecls[decl]
		if after, ok := newDecls[decl]; !known || !ok || before != after {
			if drop && known {
				dropped = append(dropped, link)
				continue
			}
			return nil, miss("%s's lowering lacks %s, and %s changed", key, link, decl)
		}
		mod.AddFunc(fn)
		have[link] = true
	}
	for _, c := range was.Cells() {
		if c.Sym().Link() == "" || !have[c.Sym().Link()] {
			if drop && c.Sym().Link() != "" {
				dropped = append(dropped, c.Sym().Link())
				continue
			}
			return nil, miss("%s's lowering lacks the cell %s", key, c.Sym().Name())
		}
	}
	mod.CarryEntries(was, func(s *ir.Symbol) bool { return s != nil && have[s.Link()] })
	return dropped, nil
}

// templateDecl is the top-level declaration of the module with key, one
// of decls, that the function with linkage name link was made from: a
// declaration's linkage name (`billing/util.first_or<...>`,
// `sat.impl Equatable for Tree<T>[...].equal<...>`, `sat.helper/step`), a
// parameter default accessor's (`billing/util.probe default 2`) or a field
// default accessor's (`type 6@pov.Tree..children default`), with the
// module, any instance's arguments and receiver, and any path into a body
// or member taken off. Declaration names may hold `<`, `.`, `/` and spaces
// themselves, so the longest one link continues is it.
func templateDecl(link, key string, decls map[string][32]byte) (string, bool) {
	rest, ok := strings.CutPrefix(link, key+".")
	if !ok {
		typ, isType := strings.CutPrefix(link, "type ")
		if !isType {
			return "", false
		}
		typ = strings.TrimLeft(typ, "0123456789")
		if rest, ok = strings.CutPrefix(typ, "@"+key+"."); !ok {
			return "", false
		}
	}
	best := ""
	for i := 1; i <= len(rest); i++ {
		if i < len(rest) && !strings.ContainsRune("<[./ ", rune(rest[i])) {
			continue
		}
		if _, known := decls[rest[:i]]; known {
			best = rest[:i]
		}
	}
	return best, best != ""
}

// decodeObject decodes one object with l and links it.
func decodeObject(l *ir.Linker, data []byte) (*ir.Module, error) {
	im, err := l.Decode(data)
	if err != nil {
		return nil, err
	}
	if len(im.Modules) != 1 {
		return nil, errors.New("not one module")
	}
	return im.Modules[0], nil
}

// The kept lowering's encoding.
const (
	keptMagic   = "NOMIKEPT"
	keptVersion = 1
)

// Encode writes k as bytes, with a CRC over them.
func (k *KeptLowering) Encode() []byte {
	var b bytes.Buffer
	w := keptWriter{&b}
	w.str(k.EntryPath)
	w.flag(k.HasMain)
	w.flag(k.EntryBoots)
	w.uv(uint64(len(k.Units)))
	for _, u := range k.Units {
		w.str(u.Key)
		w.str(u.Path)
		w.bytes(u.Object)
		w.strs(u.Files)
		names := make([]string, 0, len(u.Decls))
		for n := range u.Decls {
			names = append(names, n)
		}
		sort.Strings(names)
		w.uv(uint64(len(names)))
		for _, n := range names {
			sum := u.Decls[n]
			w.str(n)
			b.Write(sum[:])
		}
	}
	w.uv(uint64(len(k.Instances)))
	for _, data := range k.Instances {
		w.bytes(data)
	}
	w.strs(k.InstanceFiles)
	for _, list := range [][]string{k.Listed, k.Erased, k.Boxed, k.SynthRefused, k.Dangling} {
		w.strs(list)
	}
	for _, m := range []map[string][]string{k.Declines, k.Refusals} {
		paths := make([]string, 0, len(m))
		for p := range m {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		w.uv(uint64(len(paths)))
		for _, p := range paths {
			w.str(p)
			w.strs(m[p])
		}
	}
	body := b.Bytes()
	var out bytes.Buffer
	out.WriteString(keptMagic)
	keptWriter{&out}.uv(keptVersion)
	var crc [4]byte
	binary.LittleEndian.PutUint32(crc[:], crc32.ChecksumIEEE(body))
	out.Write(crc[:])
	out.Write(body)
	return out.Bytes()
}

// DecodeKeptLowering reads what Encode wrote. Anything else, a truncated or
// changed encoding included, is an error.
func DecodeKeptLowering(data []byte) (k *KeptLowering, err error) {
	bad := errors.New("irbuild: not a kept lowering")
	rest, ok := bytes.CutPrefix(data, []byte(keptMagic))
	if !ok {
		return nil, bad
	}
	r := &keptReader{b: rest}
	if r.uv() != keptVersion || len(r.b) < 4 {
		return nil, bad
	}
	if crc32.ChecksumIEEE(r.b[4:]) != binary.LittleEndian.Uint32(r.b) {
		return nil, bad
	}
	r.b = r.b[4:]
	k = &KeptLowering{}
	k.EntryPath = r.str()
	k.HasMain = r.flag()
	k.EntryBoots = r.flag()
	n := r.count()
	for i := 0; i < n && r.err == nil; i++ {
		u := KeptUnit{Key: r.str(), Path: r.str(), Object: r.bytes()}
		u.Files = r.strs()
		d := r.count()
		if d > 0 {
			u.Decls = map[string][32]byte{}
		}
		for j := 0; j < d && r.err == nil; j++ {
			name := r.str()
			var sum [32]byte
			copy(sum[:], r.raw(32))
			u.Decls[name] = sum
		}
		k.Units = append(k.Units, u)
	}
	n = r.count()
	for i := 0; i < n && r.err == nil; i++ {
		k.Instances = append(k.Instances, r.bytes())
	}
	k.InstanceFiles = r.strs()
	for _, list := range []*[]string{&k.Listed, &k.Erased, &k.Boxed, &k.SynthRefused, &k.Dangling} {
		*list = r.strs()
	}
	for _, m := range []*map[string][]string{&k.Declines, &k.Refusals} {
		n := r.count()
		for i := 0; i < n && r.err == nil; i++ {
			if *m == nil {
				*m = map[string][]string{}
			}
			p := r.str()
			(*m)[p] = r.strs()
		}
	}
	if r.err != nil || len(r.b) != 0 {
		return nil, bad
	}
	return k, nil
}

type keptWriter struct{ b *bytes.Buffer }

func (w keptWriter) uv(v uint64) {
	var buf [binary.MaxVarintLen64]byte
	w.b.Write(buf[:binary.PutUvarint(buf[:], v)])
}

func (w keptWriter) bytes(data []byte) {
	if data == nil {
		w.uv(0)
		return
	}
	w.uv(uint64(len(data)) + 1)
	w.b.Write(data)
}

func (w keptWriter) str(s string) {
	w.uv(uint64(len(s)))
	w.b.WriteString(s)
}

func (w keptWriter) flag(f bool) {
	if f {
		w.b.WriteByte(1)
	} else {
		w.b.WriteByte(0)
	}
}

func (w keptWriter) strs(list []string) {
	w.uv(uint64(len(list)))
	for _, s := range list {
		w.str(s)
	}
}

type keptReader struct {
	b   []byte
	err error
}

func (r *keptReader) fail() {
	if r.err == nil {
		r.err = errors.New("irbuild: a kept lowering ends early")
	}
	r.b = nil
}

func (r *keptReader) uv() uint64 {
	v, n := binary.Uvarint(r.b)
	if n <= 0 {
		r.fail()
		return 0
	}
	r.b = r.b[n:]
	return v
}

func (r *keptReader) count() int {
	v := r.uv()
	if v > uint64(len(r.b)) {
		r.fail()
		return 0
	}
	return int(v)
}

func (r *keptReader) raw(n int) []byte {
	if n > len(r.b) {
		r.fail()
		return make([]byte, n)
	}
	out := r.b[:n:n]
	r.b = r.b[n:]
	return out
}

func (r *keptReader) bytes() []byte {
	n := r.uv()
	if n == 0 || r.err != nil {
		return nil
	}
	if n-1 > uint64(len(r.b)) {
		r.fail()
		return nil
	}
	return r.raw(int(n - 1))
}

func (r *keptReader) str() string { return string(r.raw(r.count())) }

func (r *keptReader) flag() bool {
	b := r.raw(1)
	return b[0] == 1
}

func (r *keptReader) strs() []string {
	n := r.count()
	var out []string
	for i := 0; i < n && r.err == nil; i++ {
		out = append(out, r.str())
	}
	return out
}

// mergeLinked is the linking state of a program that links both listed std
// modules (std, possibly nil) and its own files' objects.
func mergeLinked(std, objects *linkedStd) (map[string]*ir.Symbol, *linkedStd) {
	if std == nil {
		return objects.defined, objects
	}
	out := &linkedStd{defined: map[string]*ir.Symbol{}, own: map[string]bool{}, synthRefused: map[*ast.ImplBlock]bool{}}
	for _, ls := range []*linkedStd{std, objects} {
		for k, v := range ls.defined {
			out.defined[k] = v
		}
		for k := range ls.own {
			out.own[k] = true
		}
		for k, v := range ls.synthRefused {
			out.synthRefused[k] = v
		}
	}
	return out.defined, out
}

// refusalsByFile is each refused construct by its file, each file's sorted.
func refusalsByFile(items []Unsupported) map[string][]string {
	if len(items) == 0 {
		return nil
	}
	out := map[string][]string{}
	for _, u := range items {
		out[u.File] = append(out[u.File], fmt.Sprintf("%d:%d %s %s", u.Line, u.Col, u.Construct, u.Detail))
	}
	for path, list := range out {
		sort.Strings(list)
		out[path] = slices.Compact(list)
	}
	return out
}
