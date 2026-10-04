package ir

// THE MODULE CONTAINER.
//
// WHAT IT HOLDS, AND THE LIST IS SHORT ON PURPOSE. A module is the functions
// it declares and the module-scoped storage they share. Nothing else is here,
// and every absence below is argued rather than deferred, because a container
// built for a consumer that does not exist yet costs reshaping later.
//
// SO THE TEST APPLIED TO EVERY CANDIDATE FIELD IS "WHICH READER NEEDS IT".
// The core two:
//
//	funcs   the retained ir.Funcs, so "every declaration this module names"
//	        has a place to ask it.
//	cells   module-scoped storage. `internal/irbuild`'s `flushAppCells` emits
//	        one Go `var` per app struct a module declares, EVERY one rather
//	        than only the ones something reads, so that the emitted text does
//	        not depend on the order units are rendered in. That list was
//	        recomputed at the one call that emitted it; it is a property of
//	        the module and now lives on it.
//
// AND WHAT IS DELIBERATELY ABSENT, each with the reader that would have
// justified it and does not exist:
//
//   - TYPES. `Table` holds them, and its scope is already the same as this
//     container's — "one per LOWERING, NOT PROCESS-WIDE" — so a second home
//     for a type would be a second identity for it. See table.go.
//   - IMPORTS, and the import GRAPH. `internal/irbuild`'s `implUnits` answers
//     "which units declare an impl for this type" and is read as SYNTAX
//     before any type is resolved, because `closeReferences` needs the
//     reference graph before lowering starts and the graph decides cycle
//     REFUSALS. There is no IR at that point, so a field here could not be
//     the thing that answers it.
//   - A `main` OR ENTRY POINT. `internal/irbuild` decides that from the front
//     end's own rule and its emitted `func main` is Go's, not Nomi's. A
//     program BOOT is recorded, because it is Nomi semantics rather than a
//     host choice: the runtime runs it before any entry and publishes its
//     result as the app fields every app-field read resolves against. A VM
//     starting a program reads it; see SetBoot.
//   - AN IMPL MANIFEST for the type table. `irtable.go` records why the table
//     is lazily populated: mirroring "does this type implement this
//     interface" eagerly would be a second implementation of a question
//     `g.bindsImpl` already answers, and the two would have to agree for
//     byte-identity to hold. What IS recorded is narrower: which retained
//     function implements an interface method for a declared type, because a
//     dispatched call has to find a body and the Go consumer's table
//     registrations are text. See Implement.
//
// It is not a `Node` and it carries no position, which follows from the
// per-node position rule rather than being an exception to it. Every node has
// a position because a node is an operation at a point in a source file. A
// module is a SET OF FILES; it has no point. The only position available
// would be `At(file, 1, 1)`, the first line of one of its files standing in
// for the whole compilation unit, and Region's header names exactly that as
// the fabricated position the rule forbids. So the absence of a Pos here is
// the rule applied, not a hole.
//
// SCOPE, AND WHETHER IT CONTRADICTS `table.go`'s CORRECTNESS ARGUMENT. It does
// not, and the reason is that "module" here is the COMPILATION UNIT and not
// the program. `Table`'s requirement is "ONE PER LOWERING, NOT PROCESS-WIDE",
// and its argument is about process-wide sharing: the producer's identity
// tokens include pointers `internal/irbuild` shares process-wide under a mutex,
// so a process-wide table would be a map two lowerings mutate. A Module is one
// per lowering, exactly like the Table, and `internal/irbuild` holds one per
// `gen` beside `gen.irTable`. Nothing here is shared between two lowerings and
// nothing here needs a lock.
//
// WHAT WOULD CONTRADICT IT IS AN `ir.Program`, AND THIS IS NOT ONE. Three of
// `internal/irbuild`'s eight type-identity side tables need program scope, and
// `equatable` is the clearest: it is a RELATION between declarations at
// PROGRAM scope (`typeRegistry`), which is why the side-table inventory
// records that expressing it needs a table at a second scope and why
// `ir/table.go` argues against that on correctness grounds rather than as a
// preference. This container does not supply that scope and must not be read
// as a step toward it.

// Cell is one piece of module-scoped storage: a declaration whose value
// outlives every frame that reads or writes it.
// A lazy cell owns the initializer body forced by RefOnce. Getter names and
// runtime storage layout remain consumer-specific delivery choices.
//
// Its identity is its Symbol's, which is the producer's identity for the
// declaration. `internal/irbuild` identifies an app cell by the app struct's own
// declaration, because `flushAppCells` emits exactly one cell per app struct a
// module declares, so the two are in bijection.
type Cell struct {
	owner       *Module
	sym         *Symbol
	ty          *Type
	initializer *Func
}

// Initializer is the zero-parameter body forced by a lazy cell's first read.
// Ordinary app storage has no initializer.
func (c *Cell) Initializer() *Func {
	if c == nil {
		return nil
	}
	return c.initializer
}

// Sym is the cell's declaration identity.
func (c *Cell) Sym() *Symbol {
	if c == nil {
		return nil
	}
	return c.sym
}

// Type is the type of the value the cell holds.
func (c *Cell) Type() *Type {
	if c == nil {
		return nil
	}
	return c.ty
}

// Module is the module this cell belongs to.
func (c *Cell) Module() *Module {
	if c == nil {
		return nil
	}
	return c.owner
}

func (c *Cell) String() string {
	if c == nil {
		return "cell?"
	}
	return "cell " + c.sym.Name()
}

// Module is one lowered Nomi compilation unit: the functions it declares, the
// module-scoped storage they share, and — for a file that declares tests — the
// cases a test runner runs.
type Module struct {
	name  string
	funcs []*Func
	cells []*Cell
	// tests are the `test "name" { … }` cases this module's file declares.
	// A case is NOT a declaration and is deliberately not in `funcs`; see
	// test.go for which routing reads this and why the two lists are apart.
	tests []TestCase
	// boot is the program boot this unit declares, or nil. It may name a
	// declaration the unit did not retain.
	boot *Symbol
	// testBoots are the boots this unit's `tests` groups declare. Each may
	// name a declaration the unit did not retain.
	testBoots []*Symbol
	// impls are the dispatch entries this unit's retained impl bodies answer.
	impls []Impl
	// displays are this unit's retained `impl Display` bodies, by the
	// declared type whose values they render. See ImplementDisplay.
	displays []DisplayImpl
	// rowDebugs are this unit's hand-written `impl Debug` bodies, by the
	// declared type whose values they render. See ImplementRowDebug.
	rowDebugs []DisplayImpl
	// equates and hashes are this unit's hand-written `impl Equatable` and
	// `impl Hashable` bodies, by the declared type whose values they answer.
	// See ImplementEquatable.
	equates, hashes []DisplayImpl
	// lint is LintModuleAdded's record of what it has already checked. See
	// moduleLintState.
	lint moduleLintState
}

// moduleLintState is what LintModuleAdded has checked of this module: the
// PREFIX of `funcs` and of `cells` it has linted, and RuleModuleDeclaredOnce's
// seen-sets over those prefixes. RemoveFunc and RemoveCell keep both in step,
// so the prefix stays exactly "the entries already checked".
type moduleLintState struct {
	funcs, cells int
	seenFunc     map[*Func]int
	seenSymbol   map[*Symbol]int
	seenCell     map[*Symbol]int
}

// Impl is one dispatch entry: the retained function Func implementing
// interface method Method for values of declared type Type.
type Impl struct {
	Method, Type, Func *Symbol
}

// NewModule opens a container for one compilation unit.
//
// name is DISPLAY ONLY, exactly as it is on Symbol, Type, Decl and Func: the
// container's identity is the pointer. `internal/irbuild` passes the declaring
// file's path, because a Nomi module's name lives in `nomi.toml` and
// `irbuild.Analyze` does not carry it down to a lowering, while the declaring
// path is the identity that lowering already stamps into every `//line`
// directive it emits.
func NewModule(name string) *Module {
	if name == "" {
		panic("ir.NewModule: a module with no name cannot be read back")
	}
	return &Module{name: name}
}

// Name is the module's display name. It is not its identity.
func (m *Module) Name() string { return m.name }

// AddFunc records one function this module declares.
//
// IT DOES NOT REJECT A REPEAT, and the division between what a constructor
// rejects and what Lint reports is deliberate. A constructor rejects what ONE
// CALL can be wrong about — a nil function, a cell with no type. Whether one
// declaration was recorded twice is a fact about the SET, and a producer may
// legitimately ask for a declaration it has already made: `Table.Symbol`
// returns the first entry on a repeat call for exactly that reason. A
// container that panicked instead would force every producer to keep its own
// record of what it had declared, which is the bookkeeping this container
// exists to remove. So a duplicate is kept, stays visible, and
// RuleModuleDeclaredOnce reports it.
func (m *Module) AddFunc(f *Func) {
	if f == nil {
		panic("ir.Module.AddFunc(nil)")
	}
	m.funcs = append(m.funcs, f)
}

// RemoveFunc withdraws a function this module declared. A producer that
// builds a candidate graph and then finds it unusable takes it back; nothing
// else may have linked to it in between.
func (m *Module) RemoveFunc(f *Func) {
	for i, have := range m.funcs {
		if have == f {
			m.funcs = append(m.funcs[:i], m.funcs[i+1:]...)
			if i < m.lint.funcs {
				m.lint.funcs--
				m.lint.forgetFunc(f)
			}
			return
		}
	}
}

// RemoveCell withdraws a cell this module declared, RemoveFunc's counterpart
// for a lazy cell whose initializer turned out to be unusable.
func (m *Module) RemoveCell(c *Cell) {
	for i, have := range m.cells {
		if have == c {
			m.cells = append(m.cells[:i], m.cells[i+1:]...)
			if i < m.lint.cells {
				m.lint.cells--
				m.lint.seenCell[c.Sym()]--
			}
			return
		}
	}
}

// forgetFunc undoes what LintModuleAdded recorded for one linted entry of f.
func (s *moduleLintState) forgetFunc(f *Func) {
	s.seenFunc[f]--
	if s.seenFunc[f] > 0 {
		// A repeat recorded no symbol (lintModuleDecls' `continue`).
		return
	}
	if sym := f.Sym(); sym != nil {
		s.seenSymbol[sym]--
	}
}

// DeclareCell records one piece of module-scoped storage and answers it.
func (m *Module) DeclareCell(sym *Symbol, ty *Type) *Cell {
	if sym == nil {
		panic("ir.Module.DeclareCell: module-scoped storage needs a declaration identity")
	}
	if sym.Name() == "" {
		panic("ir.Module.DeclareCell: a cell with an empty name cannot be read back")
	}
	if ty == nil {
		panic("ir.Module.DeclareCell: " + sym.Name() + " has no type; storage a consumer " +
			"cannot size is not a declaration")
	}
	c := &Cell{owner: m, sym: sym, ty: ty}
	m.cells = append(m.cells, c)
	return c
}

// DeclareLazyCell records storage whose initializer is forced by RefOnce.
// Its body belongs to the cell, just as a closure's body belongs to FuncValue.
func (m *Module) DeclareLazyCell(sym *Symbol, ty *Type, initializer *Func) *Cell {
	if initializer == nil || len(initializer.Params()) != 0 {
		panic("ir.Module.DeclareLazyCell: initializer must be a zero-parameter function")
	}
	c := m.DeclareCell(sym, ty)
	c.initializer = initializer
	return c
}

// SetBoot records sym as the program boot this unit declares. The
// declaration need not be retained: a consumer that finds no function for it
// knows the program cannot start rather than starting it without its app.
func (m *Module) SetBoot(sym *Symbol) {
	if sym == nil {
		panic("ir.Module.SetBoot(nil)")
	}
	m.boot = sym
}

// AddTestBoot records sym as a boot one of this unit's `tests` groups
// declares. The group's cases run in the app that boot publishes, so a
// function they call may read its fields. Like SetBoot, the declaration need
// not be retained.
func (m *Module) AddTestBoot(sym *Symbol) {
	if sym == nil {
		panic("ir.Module.AddTestBoot(nil)")
	}
	m.testBoots = append(m.testBoots, sym)
}

// TestBoots are the boots this unit's `tests` groups declare, in declaration
// order.
func (m *Module) TestBoots() []*Symbol {
	if m == nil {
		return nil
	}
	return m.testBoots
}

// Implement records that fn implements interface method method for values of
// declared type typ. A dispatched call keyed on such a value runs fn.
func (m *Module) Implement(method, typ, fn *Symbol) {
	if method == nil || typ == nil || fn == nil {
		panic("ir.Module.Implement: a dispatch entry needs a method, a type and a body")
	}
	m.impls = append(m.impls, Impl{Method: method, Type: typ, Func: fn})
}

// DisplayImpl names the retained `Display.to_string` body for values of one
// declared type.
type DisplayImpl struct {
	Type, Func *Symbol
}

// ImplementDisplay records that fn is std's `Display.to_string` for values of
// declared type typ. Display is std's interface, so no module's dispatch
// method symbol names it for every other module: an erased Display rendering
// (NewRenderDisplayErased) finds a value's body by its type's runtime name
// among every linked module's entries.
func (m *Module) ImplementDisplay(typ, fn *Symbol) {
	if typ == nil || fn == nil {
		panic("ir.Module.ImplementDisplay: a Display entry needs a type and a body")
	}
	m.displays = append(m.displays, DisplayImpl{Type: typ, Func: fn})
}

// ImplementRowDebug records that fn is a user's hand-written `impl Debug`
// body for values of declared type typ. An assertion report's `values:` rows
// render such a value through it, at any depth, as `dbg` does; a derived or
// universal Debug is not recorded, so every other value reads structurally.
func (m *Module) ImplementRowDebug(typ, fn *Symbol) {
	if typ == nil || fn == nil {
		panic("ir.Module.ImplementRowDebug: a Debug entry needs a type and a body")
	}
	m.rowDebugs = append(m.rowDebugs, DisplayImpl{Type: typ, Func: fn})
}

// ImplementEquatable records that fn is a hand-written `Equatable.equal?`
// body for values of declared type typ, and ImplementHashable the same for
// `Hashable.hash`. A Map key, a Set element and `==` over a container reach
// a value of such a type at any depth, so the machine answers equality and
// hashing there through these bodies (rt.Keys); a derived impl is structural
// and is not recorded.
func (m *Module) ImplementEquatable(typ, fn *Symbol) {
	if typ == nil || fn == nil {
		panic("ir.Module.ImplementEquatable: an Equatable entry needs a type and a body")
	}
	m.equates = append(m.equates, DisplayImpl{Type: typ, Func: fn})
}

// ImplementHashable: see ImplementEquatable.
func (m *Module) ImplementHashable(typ, fn *Symbol) {
	if typ == nil || fn == nil {
		panic("ir.Module.ImplementHashable: a Hashable entry needs a type and a body")
	}
	m.hashes = append(m.hashes, DisplayImpl{Type: typ, Func: fn})
}

// EquatableImpls are this module's Equatable entries, in recording order.
func (m *Module) EquatableImpls() []DisplayImpl {
	if m == nil {
		return nil
	}
	return m.equates
}

// HashableImpls are this module's Hashable entries, in recording order.
func (m *Module) HashableImpls() []DisplayImpl {
	if m == nil {
		return nil
	}
	return m.hashes
}

// RowDebugImpls are this module's row Debug entries, in the order they were
// recorded.
func (m *Module) RowDebugImpls() []DisplayImpl {
	if m == nil {
		return nil
	}
	return m.rowDebugs
}

// DisplayImpls are this module's Display entries, in the order they were
// recorded.
func (m *Module) DisplayImpls() []DisplayImpl {
	if m == nil {
		return nil
	}
	return m.displays
}

// Impls are this module's dispatch entries, in the order they were recorded.
func (m *Module) Impls() []Impl {
	if m == nil {
		return nil
	}
	return m.impls
}

// Boot is the program boot this unit declares, or nil.
func (m *Module) Boot() *Symbol {
	if m == nil {
		return nil
	}
	return m.boot
}

// Funcs are the functions this module declares, in the order they were added.
func (m *Module) Funcs() []*Func { return m.funcs }

// FuncFor is the function this module declares for sym, or nil.
//
// A LINEAR SCAN, for `Cell`'s reason: a map keyed on the Symbol would make a
// duplicate declaration collapse silently into one entry, which is a wrong
// answer where the slice gives a reported one. `RuleModuleDeclaredOnce`
// reports the duplicate and this answers the FIRST, which is `Table.Symbol`'s
// convention for a repeat call.
//
// It skips a `Func` with no Symbol — one built by `NewFunc` rather than
// `NewFuncFor` — because such a function has no identity a caller could name.
func (m *Module) FuncFor(sym *Symbol) *Func {
	if sym == nil {
		return nil
	}
	for _, f := range m.funcs {
		if f.Sym() == sym {
			return f
		}
	}
	return nil
}

// Cells are this module's storage declarations, in the order they were made.
func (m *Module) Cells() []*Cell { return m.cells }

// Cell is the storage declaration for sym, or nil.
//
// A LINEAR SCAN, because a map keyed on the Symbol would make a duplicate
// declaration silently collapse into one entry — a wrong answer where the
// slice gives a reported one. See AddFunc on where that line is drawn.
func (m *Module) Cell(sym *Symbol) *Cell {
	for _, c := range m.cells {
		if c.sym == sym {
			return c
		}
	}
	return nil
}

func (m *Module) String() string { return "module " + m.name }
