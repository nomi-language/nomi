package analysis

// Scope represents a lexical scope containing symbol definitions.
type Scope struct {
	Parent   *Scope
	Children []*Scope
	// shared marks a scope that outlives the builds parented under it: the
	// stdlib's module scopes and the prelude, built once per process and
	// the parent of every user file's scope. A child of a shared scope is
	// not added to Children. Nothing reads them there (ScopeAt descends
	// from a file's own module scope), and recording them would keep every
	// build a long-lived process ever made reachable, and have concurrent
	// builds append to one slice.
	shared  bool
	Symbols map[string]*Symbol
	// Start and End delimit the source region this scope covers, as a
	// half-open [Start, End) interval (same convention as ast.Block.Contains).
	// They are set only for *lexical* scopes — those built by the bodies pass
	// (function/lambda/case/with/concurrent bodies, impl & interface method
	// bodies, and the type-param scopes enclosing them). The
	// signature/annotations pass builds transient symbol-resolution scopes that
	// are NOT lexical regions; those are left span-less (End.Line == 0) and are
	// skipped by ScopeAt. So a span-set scope is exactly one ScopeAt resolves a
	// cursor into.
	Start Pos
	End   Pos
	// ReceiverDisplay is set on lexical scopes inside an impl block. Hover uses
	// it to render concrete impl receivers with the spelling from the impl
	// header, e.g. `Maybe<T>`.
	ReceiverDisplay string
}

// Contains reports whether the cursor position pos falls within this scope's
// span. This is a cursor-POINT query, not a byte-range test: a cursor sits
// between characters, so the End boundary is treated as touching (inclusive) —
// a cursor immediately after the last token of a body is still "in" that body,
// which is exactly where completion is invoked while writing it. (The Start
// boundary stays exclusive: a cursor before the opening token is in the parent,
// not yet in this scope.) This differs deliberately from ast.Block.Contains,
// which is a strict byte-in-range check. A span-less scope (End.Line == 0)
// contains nothing.
func (s *Scope) Contains(pos Pos) bool {
	if s == nil || s.End.Line == 0 {
		return false
	}
	if pos.Line < s.Start.Line || (pos.Line == s.Start.Line && pos.Col <= s.Start.Col) {
		return false
	}
	if pos.Line > s.End.Line || (pos.Line == s.End.Line && pos.Col > s.End.Col) {
		return false
	}
	return true
}

func NewScope(parent *Scope) *Scope {
	s := &Scope{
		Parent:  parent,
		Symbols: make(map[string]*Symbol),
	}
	if parent != nil && !parent.shared {
		parent.Children = append(parent.Children, s)
	}
	return s
}

// MarkShared records that s outlives the builds that will be parented under
// it, so NewScope stops adding their scopes to its Children. The stdlib marks
// its module scopes once it has finished analyzing itself.
func (s *Scope) MarkShared() { s.shared = true }

func (s *Scope) Define(sym *Symbol) {
	s.Symbols[sym.Name] = sym
}

// Lookup searches this scope and all parent scopes.
func (s *Scope) Lookup(name string) *Symbol {
	if sym, ok := s.Symbols[name]; ok {
		return sym
	}
	if s.Parent != nil {
		return s.Parent.Lookup(name)
	}
	return nil
}

// LookupLocal searches only this scope.
func (s *Scope) LookupLocal(name string) *Symbol {
	return s.Symbols[name]
}

func (s *Scope) ReceiverTypeDisplay() string {
	for scope := s; scope != nil; scope = scope.Parent {
		if scope.ReceiverDisplay != "" {
			return scope.ReceiverDisplay
		}
	}
	return ""
}

// AllVisible returns all symbols visible from this scope (local + parents).
// Closer scopes shadow outer ones.
func (s *Scope) AllVisible() []*Symbol {
	seen := make(map[string]bool)
	var result []*Symbol
	for scope := s; scope != nil; scope = scope.Parent {
		for name, sym := range scope.Symbols {
			if !seen[name] {
				seen[name] = true
				result = append(result, sym)
			}
		}
	}
	return result
}
