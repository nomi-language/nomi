package ir

// Merging what one lowering of a module kept into another lowering of it.
//
// A file lowered on its own (internal/irbuild's perfile.go) builds what its
// own bodies ask for. What the program's other files asked of it, an
// instance of one of its generic functions at their types and that
// instance's dispatch entries, is in the module the whole program's
// lowering made, and is carried from there into the new one
// (CarryEntries). Parts fingerprints a module's parts one by one, so that
// two lowerings that made the same parts in different orders compare
// alike.

import (
	"crypto/sha256"
	"strings"
)

// Know links in-memory modules as Add does, without dropping anything from
// them: the modules are shared (a process's stdlib lowering), so a module
// already defining a name is not checked for.
func (l *Linker) Know(mods ...*Module) {
	for _, m := range mods {
		for _, s := range ModuleSymbols(m) {
			if s.link != "" && l.syms[s.link] == nil {
				l.syms[s.link] = s
			}
		}
		for _, f := range m.funcs {
			if s := f.sym; s != nil && s.link != "" && l.defined[s] == nil {
				l.defined[s] = m
			}
		}
		for _, c := range m.cells {
			if s := c.sym; s != nil && s.link != "" && l.defined[s] == nil {
				l.defined[s] = m
			}
		}
	}
}

// symKey is a symbol's identity across lowerings: its linkage name, or for a
// symbol with none (a type's, whose identity at run time is its spelling)
// its name.
func symKey(s *Symbol) string {
	if s == nil {
		return ""
	}
	if s.link != "" {
		return s.link
	}
	return "\x00" + s.name
}

// CarryEntries appends to m each dispatch, Display, row Debug, Equatable and
// Hashable entry of from that m lacks and whose body carried accepts, and
// each method from records as implemented without an entry that m does not.
func (m *Module) CarryEntries(from *Module, carried func(*Symbol) bool) {
	have := map[string]bool{}
	for _, im := range m.impls {
		have[symKey(im.Method)+"|"+symKey(im.Type)] = true
	}
	for _, im := range from.impls {
		k := symKey(im.Method) + "|" + symKey(im.Type)
		if !have[k] && carried(im.Func) {
			have[k] = true
			m.impls = append(m.impls, im)
		}
	}
	carryRows := func(to *[]DisplayImpl, rows []DisplayImpl) {
		seen := map[string]bool{}
		for _, d := range *to {
			seen[symKey(d.Type)] = true
		}
		for _, d := range rows {
			if !seen[symKey(d.Type)] && carried(d.Func) {
				seen[symKey(d.Type)] = true
				*to = append(*to, d)
			}
		}
	}
	carryRows(&m.displays, from.displays)
	carryRows(&m.rowDebugs, from.rowDebugs)
	carryRows(&m.equates, from.equates)
	carryRows(&m.hashes, from.hashes)
	unretained := map[string]bool{}
	for _, s := range m.unretainedImpls {
		unretained[symKey(s)] = true
	}
	for _, s := range from.unretainedImpls {
		if !unretained[symKey(s)] {
			unretained[symKey(s)] = true
			m.unretainedImpls = append(m.unretainedImpls, s)
		}
	}
}

// Parts fingerprints each part of m by its identity across lowerings: each
// function and cell encoded on its own, and each test, boot and entry by
// the identities it names. Two lowerings of one module that made the same
// parts, in any order and with a type's symbol made once or twice, answer
// the same map.
func Parts(m *Module) (map[string][32]byte, error) {
	out := map[string][32]byte{}
	for _, f := range m.funcs {
		alone := &Module{name: m.name, funcs: []*Func{f}}
		data, err := EncodeObject(alone)
		if err != nil {
			return nil, err
		}
		out["func "+funcKey(f)] = sha256.Sum256(data)
	}
	for _, c := range m.cells {
		alone := &Module{name: m.name}
		alone.cells = []*Cell{{owner: alone, sym: c.sym, ty: c.ty, initializer: c.initializer}}
		data, err := EncodeObject(alone)
		if err != nil {
			return nil, err
		}
		out["cell "+symKey(c.sym)] = sha256.Sum256(data)
	}
	named := func(key string, syms ...*Symbol) {
		var b strings.Builder
		for _, s := range syms {
			b.WriteString(symKey(s) + "\x00")
		}
		out[key] = sha256.Sum256([]byte(b.String()))
	}
	for _, t := range m.tests {
		data, err := EncodeObject(&Module{name: m.name, tests: []TestCase{t}})
		if err != nil {
			return nil, err
		}
		out["test "+t.name] = sha256.Sum256(data)
	}
	named("boot", m.boot, m.mainFailure, m.bootFailure.Boot, m.bootFailure.Render)
	for _, s := range m.testBoots {
		named("test boot "+symKey(s), s)
	}
	for _, im := range m.impls {
		named("impl "+symKey(im.Method)+"|"+symKey(im.Type), im.Func)
	}
	for _, rows := range []struct {
		kind string
		rows []DisplayImpl
	}{{"display", m.displays}, {"row debug", m.rowDebugs}, {"equates", m.equates}, {"hashes", m.hashes}} {
		for _, d := range rows.rows {
			named(rows.kind+" "+symKey(d.Type), d.Func)
		}
	}
	for _, s := range m.unretainedImpls {
		named("unretained "+symKey(s), s)
	}
	return out, nil
}

func funcKey(f *Func) string {
	if f.sym != nil {
		return symKey(f.sym)
	}
	return "\x01" + f.name
}
