package vm

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/stdlibbindings"
)

func stdlibbindingsWorlds() map[string]bool { return stdlibbindings.Worlds() }

// Every intrinsic decides whether a call to it is an outside call: a pure
// one is inside by being pure, and every other one says inside or outside.
// There is no default, so a new intrinsic that says neither fails here. An
// outside one names the std module that declares it.
func TestEveryIntrinsicDecidesItsWorld(t *testing.T) {
	m := NewProgram(nil, nil, io.Discard)
	if len(m.hosts) == 0 {
		t.Fatal("a machine binds no intrinsics")
	}
	for name, in := range m.hosts {
		switch {
		case in.pure && in.world != 0:
			t.Errorf("%s is pure and sets world; a pure intrinsic is inside by being pure", name)
		case !in.pure && in.world != inside && in.world != outside:
			t.Errorf("%s is not pure and says neither inside nor outside", name)
		case in.world == outside && in.std == "":
			t.Errorf("%s is outside and names no std module", name)
		case in.world != outside && in.std != "":
			t.Errorf("%s is not outside and names std module %q", name, in.std)
		}
	}
}

// The std module an outside crossing is named by when code outside std
// makes it is the module that declares it: its file declares a `host fn` of
// the crossing's name. `dbg` is a form of the language, not a function any
// module declares; it writes to standard error, so it is named io, as
// io.eprint is.
func TestOutsideCrossingsNameTheirDeclaringModule(t *testing.T) {
	m := NewProgram(nil, nil, io.Discard)
	keys := []string{}
	for name, in := range m.hosts {
		if in.world == outside && name != dbgKey {
			keys = append(keys, name)
		}
	}
	for name, out := range stdlibbindingsWorlds() {
		if out {
			keys = append(keys, name)
		}
	}
	for _, key := range keys {
		isOutside, std := m.crossingWorld(key, nil)
		if !isOutside || std == "" {
			t.Errorf("%s: outside %v, std %q", key, isOutside, std)
			continue
		}
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
	if _, std := m.crossingWorld(dbgKey, nil); std != "io" {
		t.Errorf("dbg is named std::%s, want std::io", std)
	}
}
