package irbuild

import (
	"sort"
	"strings"
	"testing"
)

// A monomorphic std body the cache declines because it reaches a generic
// template, directly or through other bodies declined for that reason, is
// built per program (stdUnretainedMonoBody). `String.strip_prefix` calls
// the generic `String.starts_with`; without a per-program route a program
// that reaches it is BLOCKED.
//
// Every Nomi-bodied monomorphic std function the cache did not retain must
// be one a program builds, and must build, unless every call of it lowers
// to an operation of the machine (StdLowersToMachine: `Iter.from` is
// ir.IterFrom), so no program runs its body.
func TestStdBodiesTheCacheDeclinesAreBuiltPerProgram(t *testing.T) {
	idx := stdlibLowering()
	s := newStdInstances(idx, newDeclineCensus(DeclineHooks{}))
	var names []string
	whys := map[string]string{}
	for _, m := range idx.unlowered {
		for name, why := range m {
			names = append(names, name)
			_, whys[name], _ = strings.Cut(why, "|")
		}
	}
	sort.Strings(names)
	checked := map[string]bool{}
	for _, name := range names {
		f := idx.byKey[name]
		if f == nil || f.why != "" || f.rtCall != "" || f.decl == nil || f.decl.Body == nil ||
			len(f.decl.TypeParams) > 0 || len(f.decl.Decorators) > 0 || StdLowersToMachine(f.id) {
			continue
		}
		checked[name] = true
		if !s.stdUnretainedMonoBody(f) {
			t.Errorf("std %s is not retained (%s) and no program builds it", name, whys[name])
			continue
		}
		if inst, why := s.instantiate(f, nil, nil, nil); inst == nil {
			t.Errorf("std %s is built per program, and does not build: %s", name, why)
		}
	}
	for _, want := range []string{"strings.String.strip_prefix"} {
		if !checked[want] {
			t.Errorf("std %s is no longer a body the cache declines and a program builds, so this "+
				"test no longer covers it; point it at another one", want)
		}
	}
}
