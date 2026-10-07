package frontend_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// Every front-end check in a process reads the one stdlib analysis
// (std.Shared), and the language server runs several checks at once. A check
// must write nothing into it.

// Each program imports the same std names from a different file path, in the
// import forms the builder stamps a source file for: a file import, a
// selective import, a drill-through import with `self`, and a sibling user
// file that imports std itself. The entry also declares a dotted name under a
// prelude type. Under -race, a write to a shared std symbol fails this test.
func TestSharedStd_ConcurrentChecksWriteNothingShared(t *testing.T) {
	const entry = `import std/io
import std/calendar.{Days}
import std/json.Json.{self, Null}
import helper.{greet}

pub enum Maybe.Extra {
  A
}

fn main() {
  m: Maybe<Int> = Some(1)
  d: Days = Days(3)
  v: Json = Null
  io.print(greet("x"))
  io.print(Debug.inspect(m))
  io.print(Debug.inspect(v))
  io.print(Debug.inspect(d))
  io.print(Debug.inspect(Maybe.Extra.A))
}
`
	const helper = `import std/calendar.{Days}
import std/io

pub fn greet(name: String): String {
  m: Maybe<String> = Some(name)
  io.print(Debug.inspect(Days(1)))
  io.print(name)
  Maybe.with_default(m, "")
}
`
	const workers = 6
	dirs := make([]string, workers)
	for i := range dirs {
		dir := t.TempDir()
		for name, src := range map[string]string{"main.nomi": entry, "helper.nomi": helper} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		dirs[i] = dir
	}
	// Admitted alone first, so a failure below is the concurrency and not
	// the program.
	if _, err := frontend.New(frontend.Config{}).CheckFile(filepath.Join(dirs[0], "main.nomi"), frontend.Mode{}); err != nil {
		t.Fatalf("the front end rejects the program: %v", err)
	}

	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i, dir := range dirs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 3 {
				if _, err := frontend.New(frontend.Config{}).CheckFile(filepath.Join(dir, "main.nomi"), frontend.Mode{}); err != nil {
					errs[i] = err
					return
				}
			}
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("check %d: %v", i, err)
		}
	}
}

// Checking the corpus, every stdlib file as a subject, and single-file builds
// that declare under or impl a stdlib type leaves everything reachable from
// std.Shared as it was. This catches a write no concurrent run happens to
// race on.
func TestSharedStd_ChecksLeaveItUnchanged(t *testing.T) {
	if testing.Short() {
		t.Skip("checks the whole corpus")
	}
	lib := std.Shared()
	before := sharedStdState(lib)

	var corpus []string
	err := filepath.WalkDir("../../tests", func(path string, d os.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(path, ".nomi") {
			corpus = append(corpus, path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range corpus {
		hasTests, _ := frontend.FileDeclaresTests(path)
		_, _ = frontend.New(frontend.Config{}).CheckFile(path, frontend.Mode{Tests: hasTests})
	}
	stdFiles, err := filepath.Glob("../../std/*.nomi")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range stdFiles {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		_, _, _ = frontend.New(frontend.Config{}).CheckStdlibSource(filepath.Base(path), string(src), frontend.Mode{Tests: true})
	}
	// A single-file build defines a file's imports before its later
	// declarations, so these reach the stdlib's own symbols.
	for _, src := range []string{
		"import std/json.{Json}\n\nimpl Json {\n  pub fn extra(): Int {\n    1\n  }\n}\n",
		"import std/json.{Json}\n\npub enum Json.Extra {\n  A\n}\n",
		"pub enum Maybe.Extra {\n  A\n}\n",
	} {
		nodes, err := parser.Parse(lexer.Lex(src))
		if err != nil {
			t.Fatal(err)
		}
		analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	}

	after := sharedStdState(lib)
	var changed []string
	for key, was := range before {
		if now, ok := after[key]; !ok {
			changed = append(changed, key+": gone")
		} else if now != was {
			changed = append(changed, fmt.Sprintf("%s: %q, was %q", key, now, was))
		}
	}
	for key, now := range after {
		// A key on an object the shared state did not reach before is
		// a new object; the key that first points at it is reported.
		if _, ok := before[key]; !ok && reachedBefore(before, key) {
			changed = append(changed, fmt.Sprintf("%s: %q, new", key, now))
		}
	}
	sort.Strings(changed)
	if len(changed) > 20 {
		changed = append(changed[:20], fmt.Sprintf("and %d more", len(changed)-20))
	}
	for _, c := range changed {
		t.Error(c)
	}
}

// sharedStdState is every value reachable from lib, keyed by the address of
// the pointer or map that holds it and the field path inside that object.
// Addresses are stable within a process, so two states compare key by key.
func sharedStdState(lib *std.StdLib) map[string]string {
	w := stateWalker{seen: map[uintptr]bool{}, out: map[string]string{}}
	w.walk("lib", reflect.ValueOf(lib))
	return w.out
}

// reachedBefore reports whether key's object was in the earlier state.
func reachedBefore(before map[string]string, key string) bool {
	obj := key
	if i := strings.IndexAny(key, ".[#"); i >= 0 {
		obj = key[:i]
	}
	_, ok := before[obj+"#obj"]
	return ok
}

type stateWalker struct {
	seen map[uintptr]bool
	out  map[string]string
}

func (w *stateWalker) walk(path string, v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer, reflect.Map:
		if v.IsNil() {
			w.out[path] = "nil"
			return
		}
		obj := fmt.Sprintf("%x", v.Pointer())
		w.out[path] = "->" + obj
		if w.seen[v.Pointer()] {
			return
		}
		w.seen[v.Pointer()] = true
		w.out[obj+"#obj"] = v.Type().String()
		if v.Kind() == reflect.Pointer {
			w.walk(obj, v.Elem())
			return
		}
		w.out[obj+"#len"] = fmt.Sprint(v.Len())
		iter := v.MapRange()
		for iter.Next() {
			w.walk(obj+"["+mapKey(iter.Key())+"]", iter.Value())
		}
	case reflect.Interface:
		if v.IsNil() {
			w.out[path] = "nil"
			return
		}
		w.walk(path, v.Elem())
	case reflect.Struct:
		if strings.HasPrefix(v.Type().PkgPath(), "sync") {
			return
		}
		for i := 0; i < v.NumField(); i++ {
			w.walk(path+"."+v.Type().Field(i).Name, v.Field(i))
		}
	case reflect.Slice, reflect.Array:
		w.out[path+"#len"] = fmt.Sprint(v.Len())
		for i := 0; i < v.Len(); i++ {
			w.walk(fmt.Sprintf("%s[%d]", path, i), v.Index(i))
		}
	case reflect.String:
		w.out[path] = v.String()
	case reflect.Bool:
		w.out[path] = fmt.Sprint(v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		w.out[path] = fmt.Sprint(v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		w.out[path] = fmt.Sprint(v.Uint())
	case reflect.Float32, reflect.Float64:
		w.out[path] = fmt.Sprint(v.Float())
	}
}

// mapKey spells a map key: a pointer by its address, a struct by its fields.
func mapKey(k reflect.Value) string {
	switch k.Kind() {
	case reflect.Interface:
		if k.IsNil() {
			return "nil"
		}
		return k.Elem().Type().String() + ":" + mapKey(k.Elem())
	case reflect.Pointer:
		return fmt.Sprintf("%s@%x", k.Type(), k.Pointer())
	case reflect.Struct:
		parts := make([]string, k.NumField())
		for i := range parts {
			parts[i] = mapKey(k.Field(i))
		}
		return "{" + strings.Join(parts, ",") + "}"
	case reflect.String:
		return k.String()
	case reflect.Bool:
		return fmt.Sprint(k.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprint(k.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return fmt.Sprint(k.Uint())
	}
	return k.Type().String()
}
