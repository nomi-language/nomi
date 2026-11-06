package analysis

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ast/astcodec"
)

// StdFacts is what the stdlib's bodies tell a program's check, worked out
// once from a check of every module's bodies and kept beside the modules'
// interfaces (std.Shared), so that a process which builds the stdlib from
// its interfaces reads none of its bodies:
//
//   - each module's diagnostics, as std.Load gathers them;
//   - the conformance demands each module's bodies add to its declarations'
//     (its ImplManifest), which a program's missing-impl and whole-file
//     import checks fold (StdBase);
//   - each boot-only std function's first boot-only call, which the
//     boot-only check follows into std (CheckBootScope). std's functions
//     call only std's, so which of them are boot-only, and why, is the
//     same in every program.
//
// Positions name a module by its name; the files a module's positions name
// are mapped to and from a portable name by the caller (std.ImageName).
type StdFacts struct {
	// Errors holds each module's diagnostics, by module name.
	Errors map[string][]TypeError
	// manifests holds each module's body demands, by module name.
	manifests map[string][]factRecording
	// boot holds the boot-only functions' first boot-only calls.
	boot []stdBootWhy
}

// factRecording is one recording a module's bodies add to its manifest.
type factRecording struct {
	iface, typeName string
	rec             Recording
}

// stdBootWhy is one boot-only std function and its first boot-only call:
// to Supervisor.new when calleeModule is "", else to the function declared
// at callee in calleeModule, named with owning as its type.
type stdBootWhy struct {
	module             string
	fn                 Pos
	call               Pos
	calleeModule       string
	callee             Pos
	owning, calleeName string
}

// ComputeStdFacts works out the stdlib's facts from full, its analyses from
// source by module name (std.Load), with errs its diagnostics, for decls, its
// analyses by module name built from its interfaces
// (BuildStdDeclarations), whose bodies are unchecked. Each module's body
// demands are those of full's manifest past what decls' declarations demand.
func ComputeStdFacts(full, decls map[string]*FileAnalysis, errs map[string][]TypeError) (*StdFacts, error) {
	f := &StdFacts{Errors: map[string][]TypeError{}, manifests: map[string][]factRecording{}}
	for name, es := range errs {
		if len(es) > 0 {
			f.Errors[name] = slices.Clone(es)
		}
	}
	names := sortedFileNames(full)
	moduleOf := map[*FileAnalysis]string{}
	for _, name := range names {
		fa := full[name]
		moduleOf[fa] = name
		declared := decls[name]
		if declared == nil {
			return nil, fmt.Errorf("std/%s was not built from its interface", name)
		}
		for _, iface := range sortedKeys(fa.ImplManifest) {
			for _, typeName := range sortedKeys(fa.ImplManifest[iface]) {
				recs := fa.ImplManifest[iface][typeName]
				decl := declared.ImplManifest[iface][typeName]
				if len(decl) > len(recs) || !sameRecordings(decl, recs[:len(decl)]) {
					return nil, fmt.Errorf("std/%s: the bodies' demands for %s %s do not follow the declarations'", name, iface, typeName)
				}
				for _, rec := range recs[len(decl):] {
					f.manifests[name] = append(f.manifests[name], factRecording{iface: iface, typeName: typeName, rec: rec})
				}
			}
		}
	}
	// std's own graph: every std function, read from its body.
	all := make([]*FileAnalysis, 0, len(names))
	sums := make([]*bootSummary, 0, len(names))
	for _, name := range names {
		all = append(all, full[name])
		sums = append(sums, full[name].bootSummaryOf())
	}
	g := newBootGraph(all, sums, nil)
	for fd, why := range g.why {
		fn := g.fns[fd]
		w := stdBootWhy{
			module: moduleOf[fn.fa],
			fn:     Pos{Line: fd.Line, Col: fd.Col},
			call:   Pos{Line: why.call.Line, Col: why.call.Col},
		}
		if why.callee != nil {
			callee := g.fns[why.callee]
			if callee == nil {
				return nil, fmt.Errorf("std/%s: %s calls a boot-only function outside std", w.module, fd.Name)
			}
			w.calleeModule = moduleOf[callee.fa]
			w.callee = Pos{Line: why.callee.Line, Col: why.callee.Col}
			w.calleeName = why.callee.Name
			if why.sym != nil {
				w.owning = why.sym.OwningType
			}
		}
		f.boot = append(f.boot, w)
	}
	sort.Slice(f.boot, func(i, j int) bool {
		a, b := f.boot[i], f.boot[j]
		if a.module != b.module {
			return a.module < b.module
		}
		if a.fn.Line != b.fn.Line {
			return a.fn.Line < b.fn.Line
		}
		return a.fn.Col < b.fn.Col
	})
	return f, nil
}

// Install gives files, the stdlib's analyses by module name built from its
// interfaces, what f says of their bodies: each module's manifest is its
// declarations' with its bodies' demands after them, and the boot-only
// check reads each std function's calls from f instead of its body. It
// answers an error, and changes nothing, when f names a module or a
// function files lack.
func (f *StdFacts) Install(files map[string]*FileAnalysis) error {
	return f.InstallModules(files, sortedFileNames(files))
}

// InstallModules is Install for modules alone, files' analyses by name of
// a stdlib built a module at a time: files holds modules and every module
// they import, and each of modules takes its facts. A boot-only function's
// call names a function of its own module or of one it imports.
func (f *StdFacts) InstallModules(files map[string]*FileAnalysis, modules []string) error {
	installing := make(map[string]bool, len(modules))
	for _, name := range modules {
		installing[name] = true
	}
	defOf := func(module string, pos Pos) (*FileAnalysis, *ast.FuncDef, *Symbol) {
		fa := files[module]
		if fa == nil {
			return nil, nil, nil
		}
		sym := fa.Definitions[pos]
		fd := funcDefOf(sym)
		if fd == nil || fd.Line != pos.Line || fd.Col != pos.Col {
			return nil, nil, nil
		}
		return fa, fd, sym
	}
	whys := map[*ast.FuncDef]bootCall{}
	for _, w := range f.boot {
		if !installing[w.module] {
			continue
		}
		_, fd, _ := defOf(w.module, w.fn)
		if fd == nil {
			return fmt.Errorf("std/%s has no function at %d:%d", w.module, w.fn.Line, w.fn.Col)
		}
		why := bootCall{call: &ast.Call{Line: w.call.Line, Col: w.call.Col}}
		if w.calleeModule != "" {
			_, callee, sym := defOf(w.calleeModule, w.callee)
			if callee == nil || callee.Name != w.calleeName || sym.OwningType != w.owning {
				return fmt.Errorf("std/%s has no function %s at %d:%d", w.calleeModule, w.calleeName, w.callee.Line, w.callee.Col)
			}
			why.callee, why.sym = callee, sym
		}
		whys[fd] = why
	}
	for name := range f.manifests {
		if installing[name] && (files[name] == nil || files[name].kept == nil) {
			return fmt.Errorf("std/%s was not built from its interface", name)
		}
	}
	for _, name := range modules {
		fa := files[name]
		if fa == nil {
			return fmt.Errorf("std/%s was not built", name)
		}
		if fa.kept == nil {
			return fmt.Errorf("std/%s was not built from its interface", name)
		}
		m := cloneManifest(fa.kept.manifest)
		for _, r := range f.manifests[name] {
			if m == nil {
				m = map[string]map[string][]Recording{}
			}
			if m[r.iface] == nil {
				m[r.iface] = map[string][]Recording{}
			}
			m[r.iface][r.typeName] = append(m[r.iface][r.typeName], r.rec)
		}
		fa.ImplManifest = m
		// Each of the module's functions is read from the facts, not from
		// its body, a stub until Bodies grafts the source's back
		// (stdFactsBootFn).
		fa.bootFromFacts = true
	}
	for fd, why := range whys {
		stdBootWhys.Store(fd, why)
	}
	return nil
}

// stdBootWhys holds each boot-only std function's first boot-only call, by
// its declaration, from the facts of the modules installed.
var stdBootWhys sync.Map // *ast.FuncDef -> bootCall

// stdFactsBootFn is fd's node in the boot graph when f, its file, takes its
// boot facts from the stdlib's facts (InstallModules): its first boot-only
// call, or none.
func stdFactsBootFn(f *FileAnalysis, fd *ast.FuncDef) (*bootFn, bool) {
	if !f.bootFromFacts {
		return nil, false
	}
	fn := &bootFn{fd: fd, fa: f, sym: f.Definitions[Pos{Line: fd.Line, Col: fd.Col}]}
	if why, ok := stdBootWhys.Load(fd); ok {
		fn.calls = []bootCall{why.(bootCall)}
	}
	return fn, true
}

// sameRecordings reports whether a and b hold the same recordings, a
// derive's context compared by value: the two analyses each made their own.
func sameRecordings(a, b []Recording) bool {
	return slices.EqualFunc(a, b, func(x, y Recording) bool {
		if (x.Derive == nil) != (y.Derive == nil) || x.Derive != nil && *x.Derive != *y.Derive {
			return false
		}
		x.Derive, y.Derive = nil, nil
		return x == y
	})
}

func sortedFileNames(files map[string]*FileAnalysis) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// stdFactsVersion versions Encode's layout.
const stdFactsVersion = 1

// Encode writes f, naming each file a position names by fileName.
func (f *StdFacts) Encode(fileName func(string) string) []byte {
	e := astcodec.NewEncoder()
	e.Uint(stdFactsVersion)
	pos := func(p Pos) {
		e.String(fileName(p.File))
		e.Uint(uint64(p.Line))
		e.Uint(uint64(p.Col))
	}
	errNames := sortedKeys(f.Errors)
	e.Uint(uint64(len(errNames)))
	for _, name := range errNames {
		e.String(name)
		e.Uint(uint64(len(f.Errors[name])))
		for _, te := range f.Errors[name] {
			e.Int(int64(te.Line))
			e.Int(int64(te.Col))
			e.Int(int64(te.EndLine))
			e.Int(int64(te.EndCol))
			e.String(te.Message)
			e.Uint(uint64(len(te.Hints)))
			for _, h := range te.Hints {
				e.String(h)
			}
			e.String(te.Code)
			e.String(fileName(te.File))
		}
	}
	names := sortedKeys(f.manifests)
	e.Uint(uint64(len(names)))
	for _, name := range names {
		e.String(name)
		e.Uint(uint64(len(f.manifests[name])))
		for _, r := range f.manifests[name] {
			e.String(r.iface)
			e.String(r.typeName)
			pos(r.rec.Pos)
			e.Int(int64(r.rec.Kind))
			e.Bool(r.rec.Derive != nil)
			if d := r.rec.Derive; d != nil {
				e.String(d.Iface)
				e.String(d.TypeName)
				pos(d.DerivePos)
				e.String(d.FieldName)
				pos(d.FieldPos)
			}
			e.String(r.rec.Op)
			e.String(r.rec.TypeOrigin)
		}
	}
	e.Uint(uint64(len(f.boot)))
	for _, w := range f.boot {
		e.String(w.module)
		pos(w.fn)
		pos(w.call)
		e.String(w.calleeModule)
		pos(w.callee)
		e.String(w.owning)
		e.String(w.calleeName)
	}
	return e.Bytes()
}

// DecodeStdFacts reads what Encode wrote, naming each file a position names
// by filePath, Encode's fileName's inverse.
func DecodeStdFacts(data []byte, filePath func(string) string) (*StdFacts, error) {
	d := astcodec.NewDecoder(data)
	f := &StdFacts{Errors: map[string][]TypeError{}, manifests: map[string][]factRecording{}}
	ok := false
	err := d.Run(func() {
		if d.Uint() != stdFactsVersion {
			return
		}
		pos := func() Pos {
			p := Pos{File: d.String()}
			if p.File != "" {
				p.File = filePath(p.File)
			}
			p.Line, p.Col = int(d.Uint()), int(d.Uint())
			return p
		}
		for n := d.Uint(); n > 0; n-- {
			name := d.String()
			for m := d.Uint(); m > 0; m-- {
				te := TypeError{Line: int(d.Int()), Col: int(d.Int()), EndLine: int(d.Int()), EndCol: int(d.Int()), Message: d.String()}
				for h := d.Uint(); h > 0; h-- {
					te.Hints = append(te.Hints, d.String())
				}
				te.Code = d.String()
				if file := d.String(); file != "" {
					te.File = filePath(file)
				}
				f.Errors[name] = append(f.Errors[name], te)
			}
		}
		for n := d.Uint(); n > 0; n-- {
			name := d.String()
			for m := d.Uint(); m > 0; m-- {
				r := factRecording{iface: d.String(), typeName: d.String()}
				r.rec.Pos = pos()
				r.rec.Kind = RecordingKind(d.Int())
				if d.Bool() {
					r.rec.Derive = &DeriveCtx{Iface: d.String(), TypeName: d.String(), DerivePos: pos(), FieldName: d.String(), FieldPos: pos()}
				}
				r.rec.Op = d.String()
				r.rec.TypeOrigin = d.String()
				f.manifests[name] = append(f.manifests[name], r)
			}
		}
		for n := d.Uint(); n > 0; n-- {
			w := stdBootWhy{module: d.String(), fn: pos(), call: pos(), calleeModule: d.String(), callee: pos()}
			w.owning, w.calleeName = d.String(), d.String()
			f.boot = append(f.boot, w)
		}
		ok = d.Done()
	})
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("analysis: not std facts of this format")
	}
	return f, nil
}
