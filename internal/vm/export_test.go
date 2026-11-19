package vm

import "github.com/nomi-language/nomi/internal/ir"

// EntryCounts is how many times m, or any view of it, has entered each
// function it compiled with a counter (WithEntryCounts): every body the
// machine ran, a lambda's and a test case's included, which no module's
// Funcs lists.
func EntryCounts(m *Machine) map[*ir.Func]uint64 {
	out := map[*ir.Func]uint64{}
	m.codes.slots.Range(func(_, v any) bool {
		s := v.(*fnSlot)
		if c := s.code.Load(); c != nil {
			if n := c.entries.Load(); n > 0 {
				out[s.f] = n
			}
		}
		return true
	})
	return out
}
