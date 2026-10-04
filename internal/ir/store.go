package ir

// Store represents a scoped field replacement. Unlike Copy, its destination
// is a declaration symbol rather than a temporary value. The corresponding
// RefAppField is volatile because another replacement can change a later read.

// StoreKind says which declaration a Store writes.
type StoreKind uint8

const (
	// StoreAppField writes one field of the running app value, for the extent
	// of the scope the write appears in. It is the write `Ref.Volatile()`
	// reports about.
	StoreAppField StoreKind = iota
	// StoreContext rebinds the app's Context field for the extent of the
	// scope. It is not a plain field write: the stored context is bounded
	// below by the deadline already in force, and blocking operations in the
	// extent wait under the result's deadline. RefContext reads it.
	StoreContext
	// StoreScope restores the scope a RefScope read: every app field, the
	// Context and the deadline return to what they were, which ends the
	// extent of the writes made since. Src is the RefScope's handle.
	StoreScope
)

func (k StoreKind) String() string {
	switch k {
	case StoreAppField:
		return "app field"
	case StoreContext:
		return "context"
	case StoreScope:
		return "scope"
	}
	return "store?"
}

// Store writes the value in Src into a declaration that already exists.
//
// It writes no temporary, so Dst is NoTemp. That is not an omission: a Store's
// effect is on a declaration, and a consumer wanting the stored value in hand
// reads the temporary it stored from. Giving Dst the stored value would make
// one field mean two things.
type Store struct {
	pos  Pos
	kind StoreKind
	sym  *Symbol
	src  Temp
}

// NewStoreAppField writes src into one field of the running app value.
func NewStoreAppField(pos Pos, field *Symbol, src Temp) *Store {
	return newStore(pos, StoreAppField, field, src, "NewStoreAppField")
}

// NewStoreContext rebinds the Context field for the rest of the scope.
func NewStoreContext(pos Pos, field *Symbol, src Temp) *Store {
	return newStore(pos, StoreContext, field, src, "NewStoreContext")
}

// NewStoreScope restores the scope src holds, read by a NewRefScope.
func NewStoreScope(pos Pos, scope *Symbol, src Temp) *Store {
	return newStore(pos, StoreScope, scope, src, "NewStoreScope")
}

func newStore(pos Pos, kind StoreKind, sym *Symbol, src Temp, who string) *Store {
	requirePos(pos, who)
	if sym == nil {
		panic("ir." + who + ": a declaration write needs the declaration's identity")
	}
	if sym.Name() == "" {
		panic("ir." + who + ": a declaration write with an empty target name cannot be read back")
	}
	if src == NoTemp {
		panic("ir." + who + ": a declaration write with no source has no value to store")
	}
	return &Store{pos: pos, kind: kind, sym: sym, src: src}
}

// Kind says which declaration this writes.
func (s *Store) Kind() StoreKind { return s.kind }

// Sym is the declaration written. Its identity is the pointer.
func (s *Store) Sym() *Symbol { return s.sym }

// Src is the temporary whose value is stored.
func (s *Store) Src() Temp { return s.src }

func (s *Store) Pos() Pos { return s.pos }

// Dst is NoTemp: a Store writes a declaration, not a temporary. See the type.
func (s *Store) Dst() Temp { return NoTemp }

func (s *Store) AppendUses(dst []Temp) []Temp { return append(dst, s.src) }

func (s *Store) String() string {
	return "store " + s.kind.String() + " " + s.sym.Name() + " = " + s.src.String()
}

func (s *Store) irNode()  {}
func (s *Store) irInstr() {}
