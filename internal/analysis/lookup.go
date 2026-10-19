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
	// Spans can nest (a hint spanning a literal, a name inside it), so of the
	// entries covering pos the one starting last, the innermost, answers. The
	// tables are maps, and the first match found would depend on their order.
	within := func(table map[Pos]*Symbol) (*Symbol, Pos, bool) {
		var best *Symbol
		var bestPos Pos
		for at, s := range table {
			if s == nil || at.Line != pos.Line || pos.Col < at.Col || pos.Col >= at.Col+span(s) {
				continue
			}
			if best == nil || at.Col > bestPos.Col {
				best, bestPos = s, at
			}
		}
		return best, bestPos, best != nil
	}
	// Check references (identifier usages), then definitions.
	if s, at, found := within(f.References); found {
		return s, at, span(s), true
	}
	if s, at, found := within(f.Definitions); found {
		return s, at, span(s), true
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
