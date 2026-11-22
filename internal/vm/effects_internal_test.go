package vm

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every intrinsic is exactly one of pure, held, unlisted, or an effect an
// effect list names, and decides whether a call to it is an effect call: a
// pure one is inside by being pure, a held one is inside, and every other
// one says inside or outside. There is no default, so a new intrinsic that
// says neither fails here. One a list names names the std module that
// declares it, and no other does.
func TestEveryIntrinsicDecidesItsWorld(t *testing.T) {
	m := NewProgram(nil, nil, io.Discard)
	if len(m.hosts) == 0 {
		t.Fatal("a machine binds no intrinsics")
	}
	for name, in := range m.hosts {
		listed := !in.pure && !in.held && !in.unlisted
		switch {
		case in.unlisted && (in.pure || in.held):
			t.Errorf("%s is unlisted and pure or held; an unlisted intrinsic is an effect no list names", name)
		case in.pure && in.held:
			t.Errorf("%s is pure and held; a held intrinsic acts through a value it is given, which is an effect", name)
		case in.pure && in.world != 0:
			t.Errorf("%s is pure and sets world; a pure intrinsic is inside by being pure", name)
		case in.held && in.world != inside:
			t.Errorf("%s is held and not inside; using a value the code holds is inside", name)
		case !in.pure && in.world != inside && in.world != outside:
			t.Errorf("%s is not pure and says neither inside nor outside", name)
		case listed && in.std == "":
			t.Errorf("%s is neither pure nor held and names no std module", name)
		case !listed && in.std != "":
			t.Errorf("%s is pure or held and names std module %q", name, in.std)
		}
	}
}

// The std module a crossing a list names is named by when code outside std
// makes it is the module that declares it: its file declares a `host fn` of
// the crossing's name. `dbg` is a form of the language, not a function any
// module declares, and no list names it.
func TestEffectCrossingsNameTheirDeclaringModule(t *testing.T) {
	crossings := EffectCrossings()
	if len(crossings) == 0 {
		t.Fatal("no crossing is one a list names")
	}
	for key, std := range crossings {
		file := filepath.Join("..", "..", "std", filepath.FromSlash(strings.ReplaceAll(std, "::", "/"))+".nomi")
		src, err := os.ReadFile(file)
		if err != nil {
			t.Errorf("%s is named std::%s, which has no file: %v", key, std, err)
			continue
		}
		fn := key[strings.LastIndex(key, ".")+1:]
		if !regexp.MustCompile(`host fn ` + regexp.QuoteMeta(fn) + `\b`).Match(src) {
			t.Errorf("%s is named std::%s, whose file declares no host fn %s", key, std, fn)
		}
	}
	if std, named := crossings[dbgKey]; named {
		t.Errorf("dbg is named std::%s; no list names it", std)
	}
}
