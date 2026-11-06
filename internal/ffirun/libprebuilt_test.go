package ffirun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// libBindingsMiss lets the prebuilt lib wrapper run a program only when
// every binding is in a lib/ module it links, from the module cache at
// LibVersion, and otherwise names the first binding that is not.
func TestLibBindingsMiss_OnlyLibModulesAtTheReleasesVersion(t *testing.T) {
	const release = "v0.0.0-20261104000000-abcdefabcdef"
	saved := LibVersion
	LibVersion = release
	t.Cleanup(func() { LibVersion = saved })

	root := t.TempDir()
	module := func(dir, path string) string {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(dir))
		if err := os.MkdirAll(filepath.Join(full, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(full, "go.mod"), []byte("module "+path+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return full
	}
	sqlite := compilerModulePath + "/lib/sql/sqlite"
	atRelease := module("cache/github.com/nomi-language/nomi/lib/sql/sqlite@"+release, sqlite)
	atOther := module("cache/github.com/nomi-language/nomi/lib/sql/sqlite@v0.0.0-20261103000000-012345678901", sqlite)
	local := module("checkout/lib/sql/sqlite", sqlite)
	migrate := module("cache/github.com/nomi-language/nomi/lib/sql/migrate@"+release, compilerModulePath+"/lib/sql/migrate")
	app := module("app", "example.com/app")

	bound := func(files ...string) []DiscoveredPackage {
		var pkg DiscoveredPackage
		pkg.ImportPath = "example.com/goside"
		for i, f := range files {
			pkg.Exports = append(pkg.Exports, DiscoveredExport{Key: "k" + string(rune('a'+i)), SourceFile: f, SourceLine: 7})
		}
		return []DiscoveredPackage{pkg}
	}
	if miss := libBindingsMiss(bound(filepath.Join(atRelease, "sqlite.nomi"), filepath.Join(atRelease, "sub", "describe.nomi"))); miss != nil {
		t.Fatalf("sqlite at the release's version is refused: %s", miss)
	}
	for _, c := range []struct {
		name, file, want string
	}{
		{"another version", filepath.Join(atOther, "sqlite.nomi"), sqlite + " is at v0.0.0-20261103000000-012345678901, and this nomi carries the lib/ modules' Go prebuilt at " + release},
		{"a local directory", filepath.Join(local, "sqlite.nomi"), "from a directory on disk"},
		{"a lib module the runners do not link", filepath.Join(migrate, "migrate.nomi"), "a release carries only the first-party lib/ modules' Go prebuilt"},
		{"the project's own", filepath.Join(app, "ffi.nomi"), "binds the Go package example.com/goside"},
	} {
		miss := libBindingsMiss(bound(filepath.Join(atRelease, "sqlite.nomi"), c.file))
		if miss == nil {
			t.Errorf("%s: served by the prebuilt wrapper", c.name)
			continue
		}
		if got := miss.String(); !strings.Contains(got, c.want) {
			t.Errorf("%s: the reason is %q, want it to say %q", c.name, got, c.want)
		}
	}
}

// A file in the module cache reads as its module's path and its path in
// the module, as GenerateLibMains names a declaring file.
func TestDisplayLibPath_NamesAModuleCacheFileByItsModule(t *testing.T) {
	file := filepath.Join(t.TempDir(), "github.com", "nomi-language", "nomi", "lib", "image@v0.0.0-20261104000000-abcdefabcdef", "image.nomi")
	if got, want := displayLibPath(file), "github.com/nomi-language/nomi/lib/image@v0.0.0-20261104000000-abcdefabcdef/image.nomi"; got != want {
		t.Fatalf("displayLibPath = %q, want %q", got, want)
	}
}
