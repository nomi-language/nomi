package std

import (
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// TestShared_ModulesAreLoadsModules holds the stdlib built from its
// interfaces (Shared) to the stdlib analyzed from source (Load), module by
// module: as a later process reads it, from an artifact this test makes and
// decodes, every module declares what Load's declares, with the demands the
// facts give it, before any body is checked; and once Bodies has checked a
// module, its whole analysis dumps as Load's does, its bodies' and attached
// tests' facts included. Its diagnostics are Load's.
func TestShared_ModulesAreLoadsModules(t *testing.T) {
	full := Load()
	art := extractStdArtifact()
	if _, err := buildFromArtifact(art, Load()); err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeStdArtifact(encodeStdArtifact(art))
	if err != nil {
		t.Fatalf("the artifact does not decode: %v", err)
	}
	lib, err := buildFromArtifact(decoded, nil)
	if err != nil {
		t.Fatalf("the stdlib does not build from the artifact: %v", err)
	}
	if !lib.FromInterfaces() || full.FromInterfaces() {
		t.Fatal("FromInterfaces answers the wrong way round")
	}
	if len(lib.Files) != len(full.Files) || len(lib.Files) < 50 {
		t.Fatalf("the stdlib from interfaces has %d modules, Load's %d", len(lib.Files), len(full.Files))
	}
	if got, want := len(lib.Errors), len(full.Errors); got != want {
		t.Errorf("the stdlib from interfaces has errors in %d modules, Load's in %d", got, want)
	}
	for _, name := range sortedNames(full.Files) {
		got, want := analysis.DumpDeclarationsForTest(lib.Files[name]), analysis.DumpDeclarationsForTest(full.Files[name])
		if d := firstDifference(got, want); got != want && d != "" {
			t.Errorf("std/%s declares other than Load's before its bodies are checked:\n%s", name, d)
		}
	}
	for _, name := range sortedNames(full.Files) {
		if err := lib.Bodies(name); err != nil {
			t.Fatalf("std/%s: %v", name, err)
		}
		got := analysis.DumpFileForTest(lib.Files[name], lib.Errors[name], "")
		want := analysis.DumpFileForTest(full.Files[name], full.Errors[name], "")
		if d := firstDifference(got, want); got != want && d != "" {
			t.Errorf("std/%s with its bodies checked dumps other than Load's:\n%s", name, d)
		}
	}
}

// TestShared_LazyModulesAreEagerModules holds the stdlib built a module at
// a time (Shared) to the stdlib built from the same artifact at once: from
// the prelude's modules, each module added alone, in a few orders, declares
// what the eager build's declares and, once its bodies are checked, dumps
// as the eager build's does, and the two have the same diagnostics. What a
// program's check reads of a module does not depend on which modules were
// built before it.
func TestShared_LazyModulesAreEagerModules(t *testing.T) {
	art := extractStdArtifact()
	if _, err := buildFromArtifact(art, Load()); err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeStdArtifact(encodeStdArtifact(art))
	if err != nil {
		t.Fatal(err)
	}
	eager, err := buildFromArtifact(decoded, nil)
	if err != nil {
		t.Fatal(err)
	}
	for seed := int64(1); seed <= 3; seed++ {
		c, err := lazyFromArtifact(decoded)
		if err != nil {
			t.Fatal(err)
		}
		base := c.cur.Load()
		if len(base.Files) >= len(eager.Files) {
			t.Fatalf("the base stdlib has %d of %d modules: it is not built a module at a time", len(base.Files), len(eager.Files))
		}
		names := slices.Clone(c.names)
		rand.New(rand.NewSource(seed)).Shuffle(len(names), func(i, j int) { names[i], names[j] = names[j], names[i] })
		for _, name := range names {
			c.cur.Load().Ensure(name)
		}
		lazy := c.cur.Load()
		if got, want := sortedNames(lazy.Files), sortedNames(eager.Files); !slices.Equal(got, want) {
			t.Fatalf("seed %d: the lazy stdlib has modules %v, the eager %v", seed, got, want)
		}
		if got, want := fmt.Sprint(lazy.Errors), fmt.Sprint(eager.Errors); got != want {
			t.Errorf("seed %d: the lazy stdlib's errors are %s, the eager's %s", seed, got, want)
		}
		for _, name := range sortedNames(eager.Files) {
			got, want := analysis.DumpDeclarationsForTest(lazy.Files[name]), analysis.DumpDeclarationsForTest(eager.Files[name])
			if d := firstDifference(got, want); got != want && d != "" {
				t.Errorf("seed %d: std/%s built lazily declares other than built eagerly:\n%s", seed, name, d)
			}
		}
		for _, name := range names {
			if err := lazy.Bodies(name); err != nil {
				t.Fatalf("std/%s: %v", name, err)
			}
			if err := eager.Bodies(name); err != nil {
				t.Fatalf("std/%s: %v", name, err)
			}
			got := analysis.DumpFileForTest(lazy.Files[name], lazy.Errors[name], "")
			want := analysis.DumpFileForTest(eager.Files[name], eager.Errors[name], "")
			if d := firstDifference(got, want); got != want && d != "" {
				t.Errorf("seed %d: std/%s with its bodies checked dumps other than built eagerly:\n%s", seed, name, d)
			}
		}
	}
}

// TestStdArtifact_RefusesAStaleOrCorruptOne holds decodeStdArtifact to
// refusing an artifact this process did not make: truncated, with a byte
// changed, or with another format version or stdlib digest in its head.
// Shared then makes it again, so a refusal costs one slower start.
func TestStdArtifact_RefusesAStaleOrCorruptOne(t *testing.T) {
	art := extractStdArtifact()
	if _, err := buildFromArtifact(art, Load()); err != nil {
		t.Fatal(err)
	}
	data := encodeStdArtifact(art)
	if _, err := decodeStdArtifact(data); err != nil {
		t.Fatalf("the artifact does not decode: %v", err)
	}
	head := len(thisStdArtifactHead())
	cases := map[string][]byte{
		"truncated":    data[:len(data)/2],
		"empty":        nil,
		"a body byte":  flipped(data, head+4+len(data[head+4:])/2),
		"its checksum": flipped(data, head),
		"its version":  flipped(data, len(stdArtifactMagic)),
		"its digest":   flipped(data, head-3),
	}
	for name, bad := range cases {
		if _, err := decodeStdArtifact(bad); err == nil {
			t.Errorf("an artifact with %s changed decodes", name)
		}
	}
}

func flipped(data []byte, at int) []byte {
	out := append([]byte(nil), data...)
	out[at] ^= 0x5a
	return out
}

func sortedNames(files map[string]*analysis.FileAnalysis) []string {
	var names []string
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// firstDifference is the first line got and want differ at, or "" when
// they differ only where a body's import has its type in got and none in
// want (typedLater).
func firstDifference(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(g) || i < len(w); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl && !typedLater(gl, wl) {
			return fmt.Sprintf("line %d:\n  got  %s\n  want %s", i+1, gl, wl)
		}
	}
	return ""
}

// typedLater reports whether the scope lines got and want hold the same
// symbols, but for some that want leaves untyped and got types. An `import`
// in a body (an attached test's) binds a copy of the type it names. Load
// walks the bodies before it types the module's declarations, so its copy
// has no type; Bodies walks them after, so its copy has the declaration's.
func typedLater(got, want string) bool {
	if head, ok := strings.CutSuffix(want, " : <nil>"); ok && strings.HasPrefix(got, head+" : ") && strings.HasPrefix(strings.TrimLeft(got, " "), "ref ") {
		// A reference to another module's declaration in an annotation
		// holds a copy of its symbol, typed when the module it names was
		// typed before the copy was made: in a stdlib built a module at a
		// time, whenever that module was built earlier, as for a program.
		return true
	}
	if !strings.HasPrefix(strings.TrimLeft(got, " "), "scope ") {
		return false
	}
	g, w := strings.Fields(got), strings.Fields(want)
	if len(g) != len(w) {
		return false
	}
	for i := range g {
		if g[i] == w[i] {
			continue
		}
		head, ok := strings.CutSuffix(w[i], ":<nil>")
		if !ok || !strings.HasPrefix(g[i], head+":") {
			return false
		}
	}
	return true
}
