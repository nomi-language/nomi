package ir

import (
	"strings"
	"testing"
)

// addedViolations is LintModuleAdded's violations, or nil.
func addedViolations(t *testing.T, m *Module) []Violation {
	t.Helper()
	err := LintModuleAdded(m)
	if err == nil {
		return nil
	}
	le, ok := err.(*LintError)
	if !ok {
		t.Fatalf("LintModuleAdded returned %T, not *LintError", err)
	}
	return le.Violations
}

// symFunc is a one-block function claiming sym.
func symFunc(sym *Symbol, line int) *Func {
	at := At(lintFile(), line, 1)
	f := NewFuncFor(at, sym)
	b := f.NewBlock(at, "entry")
	n := NewInt(at, f.NewTemp(), int64(line))
	b.Append(n)
	b.SetTerm(NewReturn(at, n.Dst()))
	return f
}

// TestLintModuleAdded_DeclaredOnceAcrossCalls: the set rule sees an entry
// checked by an EARLIER call, which is the part an incremental lint could lose.
func TestLintModuleAdded_DeclaredOnceAcrossCalls(t *testing.T) {
	t.Run("identity claimed by an earlier call", func(t *testing.T) {
		m := NewModule(lintFile())
		shared := NewSymbol("same")
		m.AddFunc(symFunc(shared, 1))
		if vs := addedViolations(t, m); vs != nil {
			t.Fatalf("the first body is well formed: %v", vs)
		}
		m.AddFunc(symFunc(shared, 2))
		vs := addedViolations(t, m)
		if len(vs) != 1 || vs[0].Rule != RuleModuleDeclaredOnce || !strings.Contains(vs[0].Why, "identity twice") {
			t.Fatalf("the second body claims the first's identity; want one declared-once violation, got %v", vs)
		}
	})
	t.Run("a distinct identity with the same name is clean", func(t *testing.T) {
		m := NewModule(lintFile())
		m.AddFunc(symFunc(NewSymbol("same"), 1))
		_ = addedViolations(t, m)
		m.AddFunc(symFunc(NewSymbol("same"), 2))
		if vs := addedViolations(t, m); vs != nil {
			t.Fatalf("two identities sharing a name are two declarations: %v", vs)
		}
	})
	t.Run("one function recorded again later", func(t *testing.T) {
		m := NewModule(lintFile())
		f := symFunc(NewSymbol("f"), 1)
		m.AddFunc(f)
		_ = addedViolations(t, m)
		m.AddFunc(f)
		vs := addedViolations(t, m)
		if len(vs) != 1 || !strings.Contains(vs[0].Why, "records this function twice") {
			t.Fatalf("want one recorded-twice violation, got %v", vs)
		}
	})
	t.Run("a removed body frees its identity", func(t *testing.T) {
		m := NewModule(lintFile())
		shared := NewSymbol("same")
		first := symFunc(shared, 1)
		m.AddFunc(first)
		_ = addedViolations(t, m)
		m.RemoveFunc(first)
		m.AddFunc(symFunc(shared, 2))
		if vs := addedViolations(t, m); vs != nil {
			t.Fatalf("the first body was withdrawn, so the second is the only claim: %v", vs)
		}
		if err := LintModule(m); err != nil {
			t.Fatalf("the whole-module lint disagrees with the incremental one: %v", err)
		}
	})
	t.Run("storage declared again later, and freed by removal", func(t *testing.T) {
		tbl := NewTable()
		ty, _ := tbl.Concrete("clockDecl", "Clock")
		valued(ty, NewHandleType(NewSymbol("Clock")))
		sym := tbl.Symbol("clockDecl", "appCell_Clock")
		m := NewModule(lintFile())
		c := m.DeclareCell(sym, ty)
		_ = addedViolations(t, m)
		m.DeclareCell(sym, ty)
		if vs := addedViolations(t, m); len(vs) != 1 || !strings.Contains(vs[0].Why, "storage") {
			t.Fatalf("want one duplicate-storage violation, got %v", vs)
		}
		m2 := NewModule(lintFile())
		c2 := m2.DeclareCell(sym, ty)
		_ = addedViolations(t, m2)
		m2.RemoveCell(c2)
		m2.DeclareCell(sym, ty)
		if vs := addedViolations(t, m2); vs != nil {
			t.Fatalf("the first cell was withdrawn: %v", vs)
		}
		_ = c
	})
}

// TestLintModuleAdded_LintsEachFunctionOnce: a body is linted by the call after
// it is recorded and not again, and LintModule still sees everything. That is
// the contract internal/irbuild relies on: per-retention LintModuleAdded, then
// one whole LintModule per finished module (irLintFinished).
func TestLintModuleAdded_LintsEachFunctionOnce(t *testing.T) {
	at := At(lintFile(), 1, 1)
	broken := NewFunc(at, "broken")
	broken.NewBlock(at, "entry") // no terminator
	m := NewModule(lintFile())
	m.AddFunc(broken)
	if vs := addedViolations(t, m); len(vs) == 0 {
		t.Fatal("a block with no terminator must be reported by the call after it is recorded")
	}
	m.AddFunc(wellFormed())
	if vs := addedViolations(t, m); vs != nil {
		t.Fatalf("only the new, well-formed body should be linted this time: %v", vs)
	}
	if err := LintModule(m); err == nil {
		t.Fatal("LintModule must still see the broken body; it is the finishing check")
	}
}
