package vmhost

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

// Session is a REPL session: a program that grows by one file per input
// (LoadFile, session_files.go), each lowered on its own and run on one live
// machine that each extends (vm.Machine.Extend). Nothing an earlier input
// computed is computed again, and a function value an earlier input built
// keeps running its own code on the same machine.
//
// A value crosses from one input to the next through the session's store,
// keyed by an id the caller allocates. The program that computes a value
// declares `host fn <SessionPutName(id)>(value: T)` and calls it; a later
// program declares `host fn <SessionGetName(id)>(): T` and reads it, usually
// as `once name: T = <get>()`. A REPL `once` is forced through
// SessionOnceName instead, which stores the value on the first force in the
// session. The session answers all three names for every id,
// so the declarations need no host table, and a file loaded for the
// session may leave its imports unused.
type Session struct {
	out    io.Writer
	errOut io.Writer

	m *vm.Machine

	mu     sync.Mutex
	values map[int]Value
	nextID int

	// sf is the session's program of files, for inputs loaded as files
	// (session_files.go).
	sf *sessionFiles
}

const (
	sessionGetPrefix  = "nomi_repl_get_"
	sessionPutPrefix  = "nomi_repl_put_"
	sessionOncePrefix = "nomi_repl_once_"
)

// SessionGetName is the name of the host function that reads session value
// id.
func SessionGetName(id int) string { return sessionGetPrefix + strconv.Itoa(id) }

// SessionPutName is the name of the host function that stores session value
// id.
func SessionPutName(id int) string { return sessionPutPrefix + strconv.Itoa(id) }

// SessionOnceName is the name of the host function that forces session value
// id: `host fn <name>(init: () -> T): T`. The first call in the session calls
// init and stores what it answers; every later call, from any input's
// program, answers the stored value without calling init. A REPL `once` is
// declared `once x: T = <name>(|| <its initializer>)` in every program that
// reads it, so its initializer runs at most once, on first use.
func SessionOnceName(id int) string { return sessionOncePrefix + strconv.Itoa(id) }

func sessionKey(key string) bool {
	return sessionID(key, sessionGetPrefix) >= 0 || sessionID(key, sessionPutPrefix) >= 0 ||
		sessionID(key, sessionOncePrefix) >= 0
}

func sessionID(key, prefix string) int {
	rest, ok := strings.CutPrefix(key, prefix)
	if !ok {
		return -1
	}
	id, err := strconv.Atoi(rest)
	if err != nil || id < 0 || strconv.Itoa(id) != rest {
		return -1
	}
	return id
}

// NewSession opens a session whose programs write to out and whose runtime
// diagnostics go to errOut (nil for os.Stderr).
func NewSession(out, errOut io.Writer) *Session {
	return &Session{out: out, errOut: errOut, values: map[int]Value{}}
}

// NewID allocates a session value id. Ids are never reused, so an id whose
// program failed before storing it is simply never read.
func (s *Session) NewID() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID
	s.nextID++
	return id
}

// Stored reports whether a program has stored session value id.
func (s *Session) Stored(id int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.values[id]
	return ok
}

// lookup answers the session's host functions for the machine.
func (s *Session) lookup(name string) hostadapt.Func {
	if id := sessionID(name, sessionGetPrefix); id >= 0 {
		return func(_ *rt.Frame, _ []rt.Value) (rt.Value, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			v, ok := s.values[id]
			if !ok {
				return nil, fmt.Errorf("repl: session value %d was never stored", id)
			}
			return v, nil
		}
	}
	if id := sessionID(name, sessionPutPrefix); id >= 0 {
		return func(_ *rt.Frame, args []rt.Value) (rt.Value, error) {
			if len(args) != 1 {
				return nil, fmt.Errorf("repl: %s takes one value, got %d", name, len(args))
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			s.values[id] = args[0]
			return rt.Unit{}, nil
		}
	}
	if id := sessionID(name, sessionOncePrefix); id >= 0 {
		return func(fr *rt.Frame, args []rt.Value) (rt.Value, error) {
			s.mu.Lock()
			v, ok := s.values[id]
			s.mu.Unlock()
			if ok {
				return v, nil
			}
			if len(args) != 1 {
				return nil, fmt.Errorf("repl: %s takes one initializer, got %d", name, len(args))
			}
			// The lock is not held across the call: the initializer may force
			// other session onces. Within one program the calling `once` cell
			// serializes forces and rejects a cycle; programs run one at a time.
			v, err := s.m.Invoke(fr, args[0], nil)
			if err != nil {
				return nil, err
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			s.values[id] = v
			return v, nil
		}
	}
	return nil
}

// Run links p into the session's machine and calls its `main`. A program
// with no `fn main` runs nothing. The error is what Program.Run answers.
//
// A `main` the builder declined is reported before anything is linked. When
// the builder retained none of the entry's functions there is no entry
// module at all (p.entry is nil), and linking one would dereference it.
func (s *Session) Run(ctx context.Context, p *Program) error {
	var mainFn *ir.Func
	if p.HasMain() {
		if mainFn = p.entryFunc("main"); mainFn == nil {
			return &Blocked{Reasons: []string{p.notRetained("main")}}
		}
	}
	if p.entry == nil {
		return nil
	}
	if s.m == nil {
		s.m = vm.NewProgram(p.entry, p.res.IRModules(), s.out).WithHosts(p.hosts...).WithHostLookup(hostadapt.ProjectLookup(s.lookup)).
			WithLocaleLoader(p.localeData).WithPhrases(p.phrases())
		if s.errOut != nil {
			s.m = s.m.WithErrorOutput(s.errOut)
		}
		if src := p.embeddedSource(); src != nil {
			s.m = s.m.WithEmbeddedSource(src)
		}
	} else {
		s.m.Extend(p.entry, p.res.IRModules())
	}
	if mainFn == nil {
		return nil
	}
	var boots []*ir.Symbol
	if boot := p.entry.Boot(); boot != nil {
		boots = append(boots, boot)
	}
	if reasons := p.reasons(s.m.Unretained(vm.MainRoots(p.entry, mainFn), boots)); len(reasons) > 0 {
		return &Blocked{Reasons: reasons}
	}
	failure, limit := programFailure(s.m.Main(ctx, nil, false))
	if limit {
		return &Blocked{Reasons: []string{machineLimit(failure)}}
	}
	return failure
}

// Retains reports whether the builder retained the entry's function name.
// A session store whose value's type the builder cannot carry across a
// crossing is not retained.
func (p *Program) Retains(name string) bool {
	f, err := p.callable(name)
	return err == nil && f != nil
}

// Checked is a session input the front end accepted.
type Checked struct {
	prog *irbuild.Program
}

// LocalType is the source spelling of the checked type of the entry's local
// binding named name, and whether it has one. The spelling is the checker's
// rendering, with each nominal type whose declaring file's module key
// qualify accepts written through that file (`repl_in1.Point`); a type with
// an unsolved variable has none.
func (c *Checked) LocalType(name string, qualify func(module string) bool) (string, bool) {
	t := localType(c.prog, name)
	if t == nil {
		return "", false
	}
	if strings.Contains(t.String(), "?") {
		return "", false
	}
	return analysis.TypeTextQualifying(t, qualify), true
}

// IsUnit reports whether the entry's local binding named name is Unit.
func (c *Checked) IsUnit(name string) bool {
	t := localType(c.prog, name)
	return t != nil && analysis.TypesEqual(t, analysis.TypeUnit)
}

// Ref is an identifier in the entry that names a top-level function or
// `once`: the identifier's position and name, and the path of the file
// that declares what it names, the entry's own included.
type Ref struct {
	Line, Col  int
	Name, Path string
}

// Refs is every identifier in the entry that resolves to a top-level
// function or once of a project file, the entry's own or one it imports, as
// the checker resolved it: a parameter or local of the same name is not
// one.
func (c *Checked) Refs() []Ref {
	fa := c.prog.Entry().FA
	if fa == nil {
		return nil
	}
	var refs []Ref
	for pos, sym := range fa.References {
		for sym != nil && sym.Resolved != nil {
			sym = sym.Resolved
		}
		if sym == nil || sym.SourceFile == "" || (sym.Kind != analysis.SymbolFunction && sym.Kind != analysis.SymbolOnce) {
			continue
		}
		refs = append(refs, Ref{Line: pos.Line, Col: pos.Col, Name: sym.Name, Path: sym.SourceFile})
	}
	return refs
}

func localType(prog *irbuild.Program, name string) analysis.Type {
	fa := prog.Entry().FA
	if fa == nil {
		return nil
	}
	for _, sym := range fa.Definitions {
		if sym != nil && sym.Name == name && sym.Kind == analysis.SymbolBinding && sym.Type != nil {
			return sym.Type
		}
	}
	return nil
}
