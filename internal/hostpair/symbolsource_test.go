package hostpair

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	gruntime "runtime"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/stdlibbindings"
)

// WHERE THE GO SYMBOL COULD COME FROM, measured.
//
// No std facade carries a `go alias.Symbol` selector, so the question is
// whether any OTHER source determines the symbol. The candidates, all closed:
//
//	.nomi source                TestNoStdlibHostDeclarationNamesAGoSymbol: 0 bound
//	a mangle of the Nomi key    TestTheBoundGoSymbolIsNotAMangleOfItsNomiKey
//	adapter signature matching  TestSignaturePairingCannotDetermineTheGoSymbol: 52%

// boundGoSymbol is the Go function name a binding row's function value
// reports, as runtime.FuncForPC spells it.
func boundGoSymbol(fn any) string {
	rv := reflect.ValueOf(fn)
	if rv.Kind() != reflect.Func {
		return ""
	}
	f := gruntime.FuncForPC(rv.Pointer())
	if f == nil {
		return ""
	}
	return f.Name()
}

// TestTheBoundGoSymbolIsNotAMangleOfItsNomiKey closes the last route that
// needs no second table at all: computing the Go name from the Nomi name.
//
// `FFI` + PascalCase is the rule the table mostly follows, and "mostly" is the
// finding: 44 of 66 rows match and 22 do not. The exceptions are not typos —
// `random.os_state` is `FFIFromOS`, `Regex.compile` drops its receiver, and
// every `_raw` or `_state` row drops its suffix. A generator would have to
// carry them as overrides, which is the table again.
func TestTheBoundGoSymbolIsNotAMangleOfItsNomiKey(t *testing.T) {
	pascal := func(s string) string {
		var b strings.Builder
		for _, part := range strings.Split(strings.TrimSuffix(s, "?"), "_") {
			if part == "" {
				continue
			}
			b.WriteString(strings.ToUpper(part[:1]))
			b.WriteString(part[1:])
		}
		return b.String()
	}
	modules := map[string]bool{"calendar": true, "random": true, "regex": true}

	hit, miss := 0, []string{}
	for _, b := range stdlibbindings.Funcs() {
		sym := boundGoSymbol(b.Fn)
		if i := strings.LastIndex(sym, "."); i >= 0 {
			sym = sym[i+1:]
		}
		owner, name, ok := strings.Cut(b.Name, ".")
		if !ok {
			t.Fatalf("key %q has no dot; every row is `<module>.<fn>` or `<Receiver>.<fn>`", b.Name)
		}
		want := "FFI" + owner + pascal(name)
		if modules[owner] {
			want = "FFI" + pascal(name)
		}
		if sym == want {
			hit++
		} else {
			miss = append(miss, b.Name+": rule "+want+", table "+sym)
		}
	}
	if hit == 0 {
		t.Fatal("the mangling rule matches nothing at all; this probe is broken rather than the rule refuted")
	}
	if len(miss) == 0 {
		t.Errorf("every one of %d rows now follows `FFI + PascalCase`. If that is deliberate and "+
			"enforced, the symbol half IS derivable and cmd/nomi-stdlibbindings can come back.", hit)
	}
	sort.Strings(miss)
	t.Logf("`FFI + PascalCase` accounts for %d of %d rows; %d exceptions:", hit, hit+len(miss), len(miss))
	for _, m := range miss {
		t.Logf("  %s", m)
	}
}

// TestSignaturePairingCannotDetermineTheGoSymbol is the third measurement: if
// every FFI-shaped exported function in an adapter package had a DISTINCT
// signature, a generator could pair a Nomi declaration with its Go symbol by
// shape and need no table at all.
//
// It cannot. Half the surface sits in a signature group with more than one
// member, and the worst group is the nine `func(d DateTime, n int64) DateTime`
// adders — nine functions no signature can tell apart.
//
// WHAT IT WOULD SHOW IF THE WALK WERE BROKEN: zero functions found, hence zero
// collisions, hence "signatures are unique". So the population and the FFI
// prefix are both asserted before the collision count is read.
func TestSignaturePairingCannotDetermineTheGoSymbol(t *testing.T) {
	root := moduleRoot(t)
	fset := token.NewFileSet()

	type fn struct{ pkg, name, sig string }
	var all []fn
	for _, pkg := range []string{"stdcalendar", "stdrandom", "stdregex"} {
		dir := filepath.Join(root, "internal", pkg)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("reading %s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
			if err != nil {
				t.Fatalf("parsing %s: %v", e.Name(), err)
			}
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Recv != nil || !fd.Name.IsExported() {
					continue
				}
				var b strings.Builder
				if err := printer.Fprint(&b, fset, fd.Type); err != nil {
					t.Fatal(err)
				}
				all = append(all, fn{pkg, fd.Name.Name, b.String()})
			}
		}
	}
	if len(all) == 0 {
		t.Fatal("no exported adapter functions found; the walk is broken, not the packages empty")
	}

	// FFI-shaped half only: the surface internal/stdlibbindings draws from.
	byShape := map[string][]string{}
	ffiCount := 0
	for _, f := range all {
		if !strings.HasPrefix(f.name, "FFI") {
			continue
		}
		ffiCount++
		k := f.pkg + " " + f.sig
		byShape[k] = append(byShape[k], f.name)
	}
	if ffiCount == 0 {
		t.Fatal("no FFI-prefixed exported function found; the prefix convention changed and this probe is measuring nothing")
	}

	collisions, worst, inCollision := 0, 0, 0
	var shapes []string
	for k, names := range byShape {
		if len(names) > 1 {
			collisions++
			inCollision += len(names)
			if len(names) > worst {
				worst = len(names)
			}
			sort.Strings(names)
			shapes = append(shapes, k+"  <- "+strings.Join(names, ", "))
		}
	}
	sort.Strings(shapes)
	t.Logf("adapter packages export %d functions, %d of them FFI-shaped, in %d distinct signatures",
		len(all), ffiCount, len(byShape))
	t.Logf("%d signature(s) are shared by more than one function; %d of %d FFI functions (%.0f%%) sit in one; worst group has %d members",
		collisions, inCollision, ffiCount, 100*float64(inCollision)/float64(ffiCount), worst)
	if collisions == 0 {
		t.Errorf("every one of the %d FFI-shaped adapter functions now has a unique signature. "+
			"If that is deliberate and enforced, pairing by shape is a derivation and the "+
			"symbol half of internal/stdlibbindings can be generated after all.", ffiCount)
	}
	for _, s := range shapes {
		t.Logf("  AMBIGUOUS: %s", s)
	}
}
