package ir

// LINKING OBJECTS BY NAME.
//
// An object is one module written by EncodeImage on its own: the same format
// as an image, with no entry. A symbol another module can name is written
// with its linkage name (Symbol.Link), and every other one is numbered inside
// the object as before. Linking decodes each object with one Linker, which
// resolves each linkage name to one symbol across all of them, so a call in
// one object reaches the function another defines, and hands the VM ordinary
// modules. ir.FormatVersion versions the format; the version matters only to
// a `.nomic` image, the one encoding that crosses binaries.
//
// A NAME TWO OBJECTS DEFINE IS LINKED ONCE. Two lowerings may each build one
// instance (a std generic body at one set of type arguments) or one thunk,
// and the two bodies are the same code. The linker keeps the first
// definition and drops the rest, so every reference reaches one body.
// CheckLinkage, which the object harness runs over every recorded program,
// requires each such pair to encode identically, so a dropped body is never
// one that differed.
//
// A NAME NO OBJECT DEFINES IS LINKED TOO. A dispatched call names its
// interface method's symbol, which has no body, and each module records its
// own entries for it (Module.Implement). Every object that names the symbol
// links to the one symbol of its name, so a call in one object selects among
// the entries every linked module records.
//
// A LINKER MAY LINK OVER ANOTHER (NewLinkerOver). The objects a process
// decodes once each, the stdlib's, are linked by one linker, and what is
// linked against them later (the std modules declared with each program) by
// a linker over it: a name the base knows is the base's symbol and its
// definition the base's, and a linker over a base never writes it. A
// Linker is not safe for concurrent use, so a caller that links more into
// a base keeps every linker over it from decoding meanwhile
// (internal/irbuild's stdObjects decodes the stdlib's objects one module
// at a time, as the process needs them).

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

// Linker resolves linkage names to symbols while objects are decoded, so the
// objects of one program share their declarations.
type Linker struct {
	// base, when set, is the linker this one links over: what it knows is
	// known here, and it is never written.
	base *Linker
	syms map[string]*Symbol
	// defined is the module each linked symbol's function or cell is in.
	defined map[*Symbol]*Module
}

// NewLinker is a linker that has linked nothing.
func NewLinker() *Linker {
	return &Linker{syms: map[string]*Symbol{}, defined: map[*Symbol]*Module{}}
}

// NewLinkerOver is a linker that has linked what base has and writes
// nothing into base.
func NewLinkerOver(base *Linker) *Linker {
	l := NewLinker()
	l.base = base
	return l
}

// known is the symbol of a linkage name this linker or its base has linked.
func (l *Linker) known(link string) *Symbol {
	for ; l != nil; l = l.base {
		if s := l.syms[link]; s != nil {
			return s
		}
	}
	return nil
}

// definer is the module that defines s's function or cell, in this linker
// or its base.
func (l *Linker) definer(s *Symbol) *Module {
	for ; l != nil; l = l.base {
		if m := l.defined[s]; m != nil {
			return m
		}
	}
	return nil
}

// Known is the symbol this linker or its base has linked under link, or
// nil.
func (l *Linker) Known(link string) *Symbol { return l.known(link) }

// Symbol is the one symbol of written's linkage name: the first one this
// linker saw, decoded or added, else written.
func (l *Linker) Symbol(written *Symbol) *Symbol {
	if s := l.known(written.link); s != nil {
		return s
	}
	l.syms[written.link] = written
	return written
}

// Decode decodes one object (or an image), with opts, and links its
// modules.
func (l *Linker) Decode(data []byte, opts ...DecodeOption) (Image, error) {
	im, err := DecodeImage(data, append(opts, WithLinkedSymbols(l.Symbol))...)
	if err != nil {
		return Image{}, err
	}
	for _, m := range im.Modules {
		l.Define(m)
	}
	return im, nil
}

// Define records the linked symbols m defines, and drops from m a function
// or cell another module already defines (the link-once rule above). Decode
// calls it; a caller that decodes an object itself, with
// WithLinkedSymbols(l.Symbol), calls it once it has read the module.
//
// A module of abstract bodies (Module.SetAbstract) links into no program,
// and Define panics on one: only the effect walk reads it, from the lowering
// that built it.
func (l *Linker) Define(m *Module) {
	if m.abstract {
		panic("ir: Linker.Define: " + m.name + " is a module of abstract bodies, which links into no program")
	}
	kept := m.funcs[:0]
	for _, f := range m.funcs {
		if s := f.sym; s != nil && s.link != "" {
			if other := l.definer(s); other != nil && other != m {
				continue
			}
			l.defined[s] = m
		}
		kept = append(kept, f)
	}
	m.funcs = kept
	cells := m.cells[:0]
	for _, c := range m.cells {
		if s := c.sym; s != nil && s.link != "" {
			if other := l.definer(s); other != nil && other != m {
				continue
			}
			l.defined[s] = m
		}
		cells = append(cells, c)
	}
	m.cells = cells
}

// ModuleSymbols is every symbol m's encoding writes: those its functions,
// cells, tests, boots and dispatch entries name, and those the value types
// and functions they reach name.
func ModuleSymbols(m *Module) []*Symbol {
	e := newEncoder()
	e.module(&e.modBuf, m)
	e.drain()
	return e.symL
}

// EncodeObject writes m alone, as an object a Linker links.
func EncodeObject(m *Module, opts ...EncodeOption) ([]byte, error) {
	return EncodeImage(Image{Modules: []*Module{m}, Entry: -1}, opts...)
}

// funcText is f's encoding on its own, with the name and linkage name of
// every symbol it writes, in the order it writes them: two functions with
// equal texts are the same code over the same declarations. opts change how
// it is written (withoutPositions).
func funcText(f *Func, opts ...EncodeOption) []byte {
	e := newEncoder()
	for _, o := range opts {
		o(e)
	}
	e.fn(f)
	e.drain()
	var b bytes.Buffer
	for _, sec := range []*wbuf{&e.fnBuf, &e.vtBuf, &e.tyBuf} {
		b.Write(sec.b)
		b.WriteByte(0)
	}
	for _, s := range e.symL {
		if e.unnamed[s] {
			b.WriteString("\x00\x00")
			continue
		}
		b.WriteString(s.name + "\x00" + s.link + "\x00")
	}
	for _, s := range e.strL {
		b.WriteString(s + "\x00")
	}
	return b.Bytes()
}

// CheckLinkage reports how mods would fail to link as objects encoded apart:
// a symbol two of them name with no linkage name (each object would decode
// its own copy), and two symbols with one linkage name unless both are
// definitions of the same code (linking makes them one, and keeps one
// body). describe names a symbol in the report; nil prints its name.
func CheckLinkage(mods []*Module, describe func(*Symbol) string) error {
	if describe == nil {
		describe = func(s *Symbol) string { return s.name }
	}
	defs := map[*Symbol]*Func{}
	for _, m := range mods {
		for _, f := range m.funcs {
			if f.sym != nil {
				defs[f.sym] = f
			}
		}
	}
	where := map[*Symbol]*Module{}
	byLink := map[string]*Symbol{}
	var problems []string
	reported := map[*Symbol]bool{}
	for _, m := range mods {
		for _, s := range ModuleSymbols(m) {
			if s.link != "" {
				other := byLink[s.link]
				if other == nil {
					byLink[s.link] = s
					continue
				}
				if other == s || reported[s] {
					continue
				}
				reported[s] = true
				a, b := defs[other], defs[s]
				if a != nil && b != nil && bytes.Equal(funcText(a), funcText(b)) {
					continue
				}
				problems = append(problems, fmt.Sprintf("two symbols have the linkage name %q (%s, %s)",
					s.link, describe(other), describe(s)))
				continue
			}
			if first, seen := where[s]; seen && first != m && !reported[s] {
				reported[s] = true
				problems = append(problems, fmt.Sprintf("%s is named by %s and %s and has no linkage name",
					describe(s), first.name, m.name))
			} else if !seen {
				where[s] = m
			}
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	const most = 20
	more := ""
	if len(problems) > most {
		more = fmt.Sprintf("\n... and %d more", len(problems)-most)
		problems = problems[:most]
	}
	return fmt.Errorf("ir: linkage:\n%s%s", strings.Join(problems, "\n"), more)
}
