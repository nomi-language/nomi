package vmhost

import (
	"reflect"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// A session's inputs as files. Each input is one file of the session's
// program, keyed by the caller ("repl/in7"): it is checked against the
// interfaces of the files before it (analysis.ProgramInterfaces, grown
// with AddFile), lowered on its own against the modules they lowered
// (irbuild.LowerSessionFile), and linked into the live machine with
// vm.Machine.Extend. No earlier file is checked or lowered again, so an
// input costs what its own text and the files it reads cost, however long
// the session.
//
// A file stays in the session's program from LoadFile until Discard; a
// caller discards a file whose input failed, so no later check reads its
// declarations or sees its impls. CheckFile checks a text without keeping
// it.

// sessionFiles is the session's program of files.
type sessionFiles struct {
	fe *frontend.Checker
	sp *analysis.SourceProgram
	pi *analysis.ProgramInterfaces
	// apps are the application types each file's check found, which
	// become the program's when the file is committed, and boot is the
	// file whose boot a file that declares none runs under (SetBoot).
	apps    map[string][]analysis.AppTypeName
	boot    string
	texts   map[string]string
	objects *irbuild.SessionObjects
	// generic holds the files that declare something generic, which a
	// check reads with their bodies (analysis.ProgramInterfaces.SetBodies),
	// so a later file can ask for an instance no earlier lowering made.
	generic map[string]bool
	// cost counts what the session's checks and lowerings of files have
	// built.
	cost SessionCost
}

// SessionCost is what checking and lowering files cost, counted rather than
// timed: the top-level declarations the checks built (each file's own and
// those of the files it read) and the functions the lowerings made.
type SessionCost struct {
	Declarations int
	Functions    int
}

// files opens the session's program of files on first use.
func (s *Session) files() (*sessionFiles, error) {
	if s.sf != nil {
		return s.sf, nil
	}
	cfg := newConfig(nil)
	cfg.session = s
	fc, err := cfg.frontendConfig()
	if err != nil {
		return nil, err
	}
	sf := &sessionFiles{texts: map[string]string{}, objects: irbuild.NewSessionObjects(), generic: map[string]bool{},
		apps: map[string][]analysis.AppTypeName{}}
	sf.fe = frontend.New(fc)
	sp := sf.fe.SessionSource(func(key string) (string, bool) {
		text, ok := sf.texts[key]
		return text, ok
	})
	sf.sp = sp
	sf.pi = analysis.NewStoredInterfaces(sp, nil)
	s.sf = sf
	return sf, nil
}

// sessionPath is the name a session file's diagnostics and positions use.
func sessionPath(key string) string { return key + ".nomi" }

// add puts the file with key and text in the session's program.
func (sf *sessionFiles) add(key, src string) {
	sf.texts[key] = src
	sf.pi.AddFile(key, sessionPath(key))
	delete(sf.generic, key)
	if nodes, err := parser.Parse(lexer.Lex(src)); err == nil && declaresGenerics(nodes) {
		sf.generic[key] = true
	}
	sf.pi.SetBodies(sf.generic)
}

// declaresGenerics reports whether nodes declare anything generic: a
// declaration with type parameters, or an impl for a generic receiver.
func declaresGenerics(nodes []ast.Node) bool {
	found := false
	for _, n := range nodes {
		ast.Inspect(n, func(m ast.Node) bool {
			if found {
				return false
			}
			if b, ok := m.(*ast.ImplBlock); ok {
				if _, generic := b.Receiver.(*ast.GenericType); generic {
					found = true
				}
			}
			v := reflect.ValueOf(m)
			if v.Kind() == reflect.Pointer && !v.IsNil() && v.Elem().Kind() == reflect.Struct {
				for _, name := range []string{"TypeParams", "Generics"} {
					if f := v.Elem().FieldByName(name); f.IsValid() && f.Kind() == reflect.Slice && f.Len() > 0 {
						found = true
					}
				}
			}
			return !found
		})
	}
	return found
}

// check checks the file with key, which sf holds, and answers its program.
func (sf *sessionFiles) check(key, src string) (*irbuild.Program, error) {
	fc, err := sf.fe.CheckSessionFile(sf.pi, key, sessionPath(key), src, sf.boot)
	if fc == nil {
		return nil, err
	}
	sf.cost.Declarations += len(fc.Nodes)
	for _, nodes := range fc.FileNodes {
		sf.cost.Declarations += len(nodes)
	}
	sf.apps[key] = nil
	if fc.FA != nil {
		for _, st := range fc.FA.Program().AppTypes {
			sf.apps[key] = append(sf.apps[key], analysis.AppTypeName{Origin: st.Origin, Name: st.Name})
		}
	}
	prog := irbuild.SessionProgram(fc, key, sessionPath(key))
	return prog, err
}

// CheckFile checks src as the file with key, against the files the session
// holds, lowering and running nothing, and keeps nothing of it.
func (s *Session) CheckFile(key, src string) (c *Checked, err error) {
	lowering.Lock()
	defer lowering.Unlock()
	defer func() {
		if r := recover(); r != nil {
			c, err = nil, &InternalError{Panic: r, Stack: debug.Stack()}
		}
	}()
	sf, err := s.files()
	if err != nil {
		return nil, err
	}
	sf.add(key, src)
	defer sf.discard(key)
	prog, err := sf.check(key, src)
	if err != nil {
		return nil, err
	}
	return &Checked{prog: prog}, nil
}

// LoadFile checks src as the file with key against the files the session
// holds and lowers it on its own. The file stays in the session's program:
// Commit it once it ran, or Discard it.
func (s *Session) LoadFile(key, src string) (*Program, error) {
	sf, err := s.files()
	if err != nil {
		return nil, err
	}
	cfg := newConfig(nil)
	cfg.session = s
	cfg.out = s.out
	cfg.errOut = s.errOut
	cfg.generate = func(prog *irbuild.Program) (*irbuild.Result, error) {
		sf.objects.Boots = sf.boot != ""
		res, err := irbuild.LowerSessionFile(prog, sf.objects)
		if err == nil {
			sf.cost.Functions += irbuild.SessionFunctions(res)
		}
		return res, err
	}
	sf.add(key, src)
	p, err := lower(cfg, func() (*irbuild.Program, error) { return sf.check(key, src) })
	if err != nil {
		return nil, err
	}
	p.sources = map[string]string{sessionPath(key): src}
	return checked(p, nil)
}

// Commit makes p, a program LoadFile made that ran, part of the session:
// the files after it link against its modules.
func (s *Session) Commit(p *Program) {
	if s.sf == nil {
		return
	}
	s.sf.objects.Commit(p.res)
	// An application type an entry boot of the file returns is the
	// program's for the files after it, which run the boot again.
	for _, app := range s.sf.apps[p.prog.Entry().Name] {
		if !slices.Contains(s.sf.sp.AppTypeNames, app) {
			s.sf.sp.AppTypeNames = append(s.sf.sp.AppTypeNames, app)
		}
	}
}

// Supersede redirects each function of the impl a file of the session
// declared under the declaration name decl to the function of the same
// name that p, a program LoadFile made of file key that ran and was
// committed, holds of its impl of the same name: every call to the earlier
// impl's functions, compiled before or after, runs the later one's. A REPL
// input that redefines an impl replaces the earlier one for code earlier
// inputs compiled too.
func (s *Session) Supersede(p *Program, key, file, decl string) {
	if s.sf == nil || s.m == nil || p.entry == nil {
		return
	}
	for _, f := range p.entry.Funcs() {
		if f.Sym() == nil {
			continue
		}
		rest, ok := strings.CutPrefix(f.Sym().Link(), key+".")
		if !ok || !strings.HasPrefix(rest, decl+"[") {
			continue
		}
		s.m.Redirect(s.sf.objects.Func(file+"."+rest), f)
	}
}

// SetBoot makes the boot the file with key declares the one the files
// checked after it run under when they declare none, as the machine runs
// it (vm.Machine.Extend keeps the boot it had); "" for none. A file that
// runs under it imports it.
func (s *Session) SetBoot(key string) {
	if sf, err := s.files(); err == nil {
		sf.boot = key
	}
}

// SetScopeNames gives the names the files checked after it have in scope
// though they import only what their text names, as a REPL input's file
// does: a "did you mean" hint for an undefined name offers them
// (analysis.SourceProgram.ScopeNames).
func (s *Session) SetScopeNames(names []string) {
	if sf, err := s.files(); err == nil {
		sf.sp.ScopeNames = names
	}
}

// Discard takes the file with key out of the session's program.
func (s *Session) Discard(key string) {
	if s.sf != nil {
		s.sf.discard(key)
	}
}

func (sf *sessionFiles) discard(key string) {
	delete(sf.texts, key)
	delete(sf.apps, key)
	delete(sf.generic, key)
	sf.pi.RemoveFile(key)
}

// ReplaceFile gives the file with key, which the session holds, the text
// src from now on, for the checks of later files: a REPL takes out of an
// earlier input's file a declaration a later input replaced. What the file
// lowered is not lowered again.
func (s *Session) ReplaceFile(key, src string) {
	if _, held := s.sf.texts[key]; s.sf != nil && held {
		s.sf.add(key, src)
	}
}

// FileText is the text the session holds for the file with key.
func (s *Session) FileText(key string) (string, bool) {
	if s.sf == nil {
		return "", false
	}
	text, ok := s.sf.texts[key]
	return text, ok
}

// Cost is what the session's checks and lowerings of files have cost so
// far; the difference across an input is what the input cost.
func (s *Session) Cost() SessionCost {
	if s.sf == nil {
		return SessionCost{}
	}
	return s.sf.cost
}
