package analysis_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/pkgcache"
	"github.com/nomi-language/nomi/internal/pkgfetch/pkgfetchtest"
	"github.com/nomi-language/nomi/std"
)

// vaultFiles is module example.com/vault: one public, one internal and one
// private function.
var vaultFiles = map[string]string{
	"nomi.toml": "[package]\nname = \"vault\"\n",
	"vault.nomi": `pub fn open() -> Int
    secret() + hidden()
end

fn secret() -> Int
    1
end

private fn hidden() -> Int
    2
end
`,
}

// vaultUseErrors builds a program app whose main names fn of vault
// qualified, with vault at dir when dir is set and from the package cache
// otherwise, and answers the entry's diagnostics.
func vaultUseErrors(t *testing.T, fn, dir string) []analysis.TypeError {
	t.Helper()
	root := filepath.Join(t.TempDir(), "app")
	dep := "vault = { repo = \"example.com/vault\", version = \"v0.1.0\" }\n"
	if dir != "" {
		dep = "vault = { dir = \"" + filepath.ToSlash(dir) + "\" }\n"
	}
	writeTestFile(t, filepath.Join(root, "nomi.toml"), "[package]\nname = \"app\"\nentry_points = [\"main\"]\n\n[dependencies]\n"+dep)
	src := "import vault\n\nfn main()\n    _ = vault." + fn + "()\nend\n"
	writeTestFile(t, filepath.Join(root, "main.nomi"), src)
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	lib := loadStd()
	fa := analysis.BuildProject(nodes, lib.Primitives, lib.Modules, lib.Files, root, std.MakeLoader())
	if fa == nil {
		t.Fatal("no analysis")
	}
	return append(entryErrors(fa), analysis.CheckTypes(fa, nodes)...)
}

// Naming another module's internal or private declaration is refused
// either way. The help that says to edit the declaration is shown for a
// dependency named by `dir`, whose files are the reader's, and left off for
// one fetched into the package cache, whose files are not.
func TestVisibilityHelp_OnlyForAPackageTheReaderCanEdit(t *testing.T) {
	pkgfetchtest.CacheRepo(t, pkgfetchtest.Cache(t), "example.com/vault", "v0.1.0", vaultFiles)
	dir := filepath.Join(t.TempDir(), "vault")
	for name, text := range vaultFiles {
		writeTestFile(t, filepath.Join(dir, name), text)
	}
	if _, err := os.Stat(pkgcache.RepoDir(os.Getenv(pkgcache.CacheEnv), "example.com/vault", "v0.1.0")); err != nil {
		t.Fatalf("vault is not in the module cache: %v", err)
	}
	for _, c := range []struct {
		fn, code, message, hint string
	}{
		{"secret", analysis.InternalDeclarationCode, "`secret` is internal to package `vault`",
			"write `pub` on its declaration to name it from another package"},
		{"hidden", analysis.PrivateDeclarationCode, "`hidden` is private to `vault.nomi`",
			"drop `private` to name it from the other files of its package, or write `pub` to name it from any package"},
	} {
		for _, where := range []struct {
			name, dir string
			help      bool
		}{{"dir", dir, true}, {"fetched", "", false}} {
			t.Run(c.fn+" of a "+where.name+" dependency", func(t *testing.T) {
				errs := vaultUseErrors(t, c.fn, where.dir)
				var found *analysis.TypeError
				for i := range errs {
					if errs[i].Code == c.code {
						found = &errs[i]
						break
					}
				}
				if found == nil {
					t.Fatalf("the front end admits vault.%s() or reports no %s, got:\n%s", c.fn, c.code, levelErrors(errs))
				}
				if found.Message != c.message {
					t.Errorf("message %q, want %q", found.Message, c.message)
				}
				if found.Fix != nil {
					t.Errorf("a fix edits the declaration: %+v", found.Fix)
				}
				switch {
				case where.help && (len(found.Hints) != 1 || found.Hints[0] != c.hint):
					t.Errorf("hints %q, want [%q]", found.Hints, c.hint)
				case !where.help && len(found.Hints) != 0:
					t.Errorf("a fetched module's declaration has help %q", found.Hints)
				}
			})
		}
	}
}
