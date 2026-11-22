// Package hostpair derives, from a std module's `.nomi` source, the keys
// each of its host declarations answers to, and its tests hold
// internal/stdlibbindings' hand-written rows to them
// (registered_test.go).
//
// Every std declaration is a bare `host fn` or `host type`, so the Go half
// of a row is in no `.nomi` file (symbolsource_test.go measures that), and
// the key half is what source determines. The derivation is kept here,
// apart from the IR builder's stdKey, so the check reads the declarations
// rather than the builder.
//
// No production code reads this package (unreferenced_test.go).
package hostpair

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// Kind separates a host FUNCTION declaration from a host TYPE declaration.
// A type's Go side is a named type (registered by prototype) and a
// function's is a func value, so internal/stdlibbindings holds them in two
// tables.
type Kind uint8

const (
	// KindFunc is a `host fn` declaration.
	KindFunc Kind = iota + 1
	// KindType is a `host type` declaration.
	KindType
)

func (k Kind) String() string {
	switch k {
	case KindFunc:
		return "func"
	case KindType:
		return "type"
	}
	return "invalid"
}

// Pairing is one host declaration as the keys a binding row may name it by.
//
// The declaration is kept DECOMPOSED (module, receiver, interface
// instantiation, name) rather than pre-joined into a key, because the
// spellings join those four parts differently. Joining is Keys and
// BuilderKey.
type Pairing struct {
	Kind Kind

	// Module is the std module the declaration is in: "regex" for
	// std/regex.
	Module string
	// Receiver is the BASE name of the enclosing `impl` block's receiver type,
	// or "" for a file-top-level declaration. Base name, so `impl Box<T>`
	// gives "Box".
	//
	// The receiver's type parameters are a BINDER, not an instantiation: a
	// module holds at most one `impl Box<T>`, so `<T>` cannot tell two
	// declarations apart, and carrying it produces a key no lookup forms.
	Receiver string
	// Interface is the interface instantiation an `impl Iface<Args> for T`
	// block names, and "" for an inherent block or a non-generic interface.
	// irbuild/stdlib.go's stdImplKey rule, which keys
	// `calendar.NaiveDateTime.Add<Duration, NaiveDateTime>.add` verbatim:
	// eleven `impl Add<X, NaiveDateTime>` blocks each declare `add`, so
	// dropping the instantiation collapses eleven declarations onto one key.
	Interface string
	// Name is the declaration's own name, exactly as written, a trailing
	// `?` or `!` included.
	Name string
}

// BuilderKey is the spelling irbuild.stdKey produces: the module is ALWAYS
// present, and an instantiated generic interface contributes a segment. It
// is the FIRST member of Keys.
func (p Pairing) BuilderKey() string {
	parts := make([]string, 0, 4)
	if p.Module != "" {
		parts = append(parts, p.Module)
	}
	if p.Receiver != "" {
		parts = append(parts, p.Receiver)
		if p.Interface != "" {
			parts = append(parts, p.Interface)
		}
	}
	return strings.Join(append(parts, p.Name), ".")
}

// Keys is every registration spelling this declaration answers to, most
// specific first.
//
// A TOP-LEVEL declaration has exactly one spelling, `module.name`. An
// impl-owned one has its module-prefixed spellings before its bare ones,
// interface-qualified before not: internal/stdlibbindings names
// `Regex.compile`, with no `regex.` on it, and an interface's instantiation
// only where the interface takes type arguments.
func (p Pairing) Keys() []string {
	if p.Receiver == "" {
		if p.Module != "" {
			return []string{p.Module + "." + p.Name}
		}
		return []string{p.Name}
	}
	bare := make([]string, 0, 2)
	if p.Interface != "" {
		bare = append(bare, p.Receiver+"."+p.Interface+"."+p.Name)
	}
	bare = append(bare, p.Receiver+"."+p.Name)
	if p.Module == "" {
		return bare
	}
	keys := make([]string, 0, len(bare)*2)
	for _, k := range bare {
		keys = append(keys, p.Module+"."+k)
	}
	return append(keys, bare...)
}

// receiverBaseName is the base name of an impl block's receiver, so
// `impl Generator<T>` gives "Generator".
func receiverBaseName(recv ast.TypeExpr) string {
	switch t := recv.(type) {
	case *ast.SimpleType:
		return t.Name
	case *ast.GenericType:
		return t.Name
	case *ast.QualifiedType:
		return receiverBaseName(t.Member)
	}
	return ""
}

// interfaceInstantiation is irbuild.stdImplKey: the interface's full
// instantiation when it takes type arguments, and "" otherwise.
//
// "" for a non-generic interface is not a shortcut. A non-generic interface
// can be implemented for a receiver at most once, so the instantiation would
// add no information; a generic one can be implemented once per
// instantiation, which is where the collapse lived.
func interfaceInstantiation(iface ast.TypeExpr) string {
	switch t := iface.(type) {
	case *ast.GenericType:
		if len(t.Params) == 0 {
			return ""
		}
		return t.TypeString()
	case *ast.QualifiedType:
		if interfaceInstantiation(t.Member) == "" {
			return ""
		}
		return t.TypeString()
	}
	return ""
}
