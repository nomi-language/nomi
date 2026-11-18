package irbuild

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
)

// substVerify holds, in a test binary, each checker type an instance's
// frame carries to the kind beside it (verifySubst). NOMI_SUBST_VERIFY=1
// holds them in any binary, such as the `nomi` a test runs.
var substVerify = testing.Testing() || os.Getenv("NOMI_SUBST_VERIFY") != ""

// verifySubst panics when a checker type in f projects to a kind other
// than the one the frame binds its parameter to, and counts each parameter
// f binds with no checker type in the census when one is open.
//
// A std instance's gen holds the kinds of the program gen that asked for
// it (stdProgCaller), whose types it cannot name itself, so the type is
// projected there.
func (g *gen) verifySubst(entry substEntry, owner any, f substFrame) {
	if entry == substProbe {
		return
	}
	in := g
	if g.stdProgCaller != nil {
		in = g.stdProgCaller
	}
	for name, k := range f.kinds {
		t, ok := f.checked[name]
		if !ok {
			substCensusNote(g, entry, owner, f, name)
			continue
		}
		if pk := in.project(t); !in.substKindsAgree(pk, k) {
			panic(fmt.Sprintf("irbuild: the %s instance %s binds %s to the kind %s, and the checker's type for it, %s, is the kind %s",
				entry, substOwnerName(owner, f), name, k.nomi(), t, pk.nomi()))
		}
	}
}

// substKindsAgree reports whether projected, the kind the checker's type
// for a parameter projects to, describes what bound, the kind the frame
// binds it to, does: the same kind, or one std interface held as a value
// through its two defs (sameStdIfaceHeld). project gives a subtype its
// base's kind, so a subtype needs no case here.
func (g *gen) substKindsAgree(projected, bound kind) bool {
	return projected == bound || g.sameStdIfaceHeld(projected, bound)
}

// sameStdIfaceHeld reports whether a and b are existentials over one std
// interface, one through the process-wide def a spec row describes
// (stdIfaceOf) and the other through a program's own def of its
// declaration (stdOrigin, stdprogiface.go), matched as stdIfaceOfDecl
// matches them. A file that names the interface through the prelude's
// anchors holds the first, and importKind hands it to another file
// unchanged; project in a gen that builds program defs answers the second
// (`build_each<Display, Url>` in std/url's prompts, instantiated in
// std::literals::testing).
func (g *gen) sameStdIfaceHeld(a, b kind) bool {
	if a.tag != tagIface || b.tag != tagIface {
		return false
	}
	held := func(shared, prog *ifaceDef) bool {
		_, isShared := stdIfaceOf(shared)
		return isShared && prog != nil && prog.stdOrigin != "" && prog.decl != nil &&
			g.stdIfaceOfDecl(prog.stdOrigin, prog.decl) == shared
	}
	return held(a.iface, b.iface) || held(b.iface, a.iface)
}

// substOwnerName names an instance in a verifySubst failure.
func substOwnerName(owner any, f substFrame) string {
	name := fmt.Sprintf("%T", owner)
	switch o := owner.(type) {
	case *monoTemplate:
		name = o.decl.Name
	case *genericTemplate:
		name = o.nomi
	case *implDef:
		name = "impl"
		if o.iface != nil {
			name += " " + o.iface.nomi
		}
		name += " for " + o.recv.nomi()
	case *stdFunc:
		name = o.key
	}
	return name + "<" + substFrameKey(f) + ">"
}

// substFrameKey spells f's kinds, its names sorted.
func substFrameKey(f substFrame) string {
	names := make([]string, 0, len(f.kinds))
	for n := range f.kinds {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for i, n := range names {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(n + "=" + f.kinds[n].nomi())
	}
	return b.String()
}

// substCensus counts, while a test has it open, the type parameters
// instances are entered with and no checker type, by entry, each
// parameter of each instance of one program's lowering once. Only a
// program's lowering is counted (a gen with a std instance table): the
// shared std lowering runs once a process, or not at all when its bundle
// is read back.
var substCensus struct {
	sync.Mutex
	open    bool
	lowered *stdInstances
	seen    map[string]bool
	missing [substEntries]int
}

func substCensusNote(g *gen, entry substEntry, owner any, f substFrame, name string) {
	if g.stdInsts == nil {
		return
	}
	c := &substCensus
	c.Lock()
	defer c.Unlock()
	if !c.open {
		return
	}
	if c.lowered != g.stdInsts {
		// A new lowering. The table is held until the next one, so no
		// later allocation reuses an owner's address under it.
		c.lowered, c.seen = g.stdInsts, map[string]bool{}
	}
	key := fmt.Sprintf("%d\x00%p\x00%s\x00%s", entry, owner, substFrameKey(f), name)
	if c.seen[key] {
		return
	}
	c.seen[key] = true
	c.missing[entry]++
}

// openSubstCensus starts counting and answers a function that stops and
// returns the counts by entry.
func openSubstCensus() func() map[substEntry]int {
	c := &substCensus
	c.Lock()
	c.open, c.lowered, c.seen, c.missing = true, nil, nil, [substEntries]int{}
	c.Unlock()
	return func() map[substEntry]int {
		c.Lock()
		defer c.Unlock()
		out := map[substEntry]int{}
		for e, n := range c.missing {
			if n > 0 {
				out[substEntry(e)] = n
			}
		}
		c.open, c.lowered, c.seen = false, nil, nil
		return out
	}
}
