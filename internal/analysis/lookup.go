package analysis

// ReferenceAt returns the symbol referenced at the given position, or nil.
func (f *FileAnalysis) ReferenceAt(pos Pos) *Symbol {
	return f.References[pos]
}

// DefinitionAt returns the symbol defined at the given position, or nil.
func (f *FileAnalysis) DefinitionAt(pos Pos) *Symbol {
	return f.Definitions[pos]
}

// SymbolAt returns whatever is at the given position — checks references first,
// then definitions. Uses range-based matching: the cursor can land anywhere
// within a token. Length comes from sym.Span when set (for synthetic symbols
// like literal-arg hints whose Name doesn't match the source token), otherwise
// falls back to len(sym.Name).
func (f *FileAnalysis) SymbolAt(pos Pos) *Symbol {
	sym, _, _, _ := f.TokenAt(pos)
	return sym
}

// TokenAt is SymbolAt plus the source token it matched: the start position and
// length of the reference/definition whose span contains pos. Callers that
// highlight the hovered token (hover) need the token's *start*, not the cursor
// — anchoring a highlight at the cursor makes it appear to start mid-token.
func (f *FileAnalysis) TokenAt(pos Pos) (sym *Symbol, start Pos, length int, ok bool) {
	span := func(s *Symbol) int {
		if s.Span > 0 {
			return s.Span
		}
		return len(s.Name)
	}
	// Check references (identifier usages)
	for refPos, s := range f.References {
		if refPos.Line == pos.Line && pos.Col >= refPos.Col && pos.Col < refPos.Col+span(s) {
			return s, refPos, span(s), true
		}
	}
	// Check definitions
	for defPos, s := range f.Definitions {
		if defPos.Line == pos.Line && pos.Col >= defPos.Col && pos.Col < defPos.Col+span(s) {
			return s, defPos, span(s), true
		}
	}
	return nil, Pos{}, 0, false
}

// ScopeAt returns the innermost lexical scope that contains the given position.
// It descends from the module scope into the narrowest span-set child that
// contains pos, repeating until no child narrows further. The module scope is
// the guaranteed-containing root, so this is total — a position outside every
// child scope resolves to the module scope. Span-less scaffolding scopes from
// the signature pass are skipped (see Scope.Start/End).
func (f *FileAnalysis) ScopeAt(pos Pos) *Scope {
	scope := f.ModuleScope
	if scope == nil {
		return nil
	}
	for {
		child := tightestContainingChild(scope, pos)
		if child == nil {
			return scope
		}
		scope = child
	}
}

// tightestContainingChild returns the child of s whose span contains pos and is
// narrowest, or nil if none does. Only span-set (lexical) children count.
// Lexical siblings are disjoint, so at most one normally qualifies; the
// narrowest-wins tiebreak is a deterministic guard against any overlap.
func tightestContainingChild(s *Scope, pos Pos) *Scope {
	var best *Scope
	for _, c := range s.Children {
		if !c.Contains(pos) {
			continue
		}
		if best == nil || spanWithin(c, best) {
			best = c
		}
	}
	return best
}

// spanWithin reports whether a's span is contained in b's (a is the tighter).
func spanWithin(a, b *Scope) bool {
	return !posBefore(a.Start, b.Start) && !posBefore(b.End, a.End)
}

// posBefore reports whether a comes strictly before b in source order.
func posBefore(a, b Pos) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Col < b.Col
}
