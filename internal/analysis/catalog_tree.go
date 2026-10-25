package analysis

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/catalog"
)

// CheckCatalogTree reports the catalogs of the module at root, whose
// manifest is m, that nothing reads: a directory for a locale `[i18n]` does
// not list, or for the source locale, whose text is in the declarations; a
// catalog file whose Nomi file does not exist; and, for a file of the
// program that declares no message (files, by module key), every entry of
// its catalogs. A file that declares messages reports its own catalogs'
// stray entries (checkFileCatalogs). Each error is in the catalog file it
// is about. A module whose manifest lists no locales has no catalogs.
func CheckCatalogTree(root string, m *Manifest, files map[string][]ast.Node) []TypeError {
	if root == "" || m == nil || m.Locales == nil {
		return nil
	}
	dir := filepath.Join(root, filepath.FromSlash(m.CatalogDir()))
	locales, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var errs []TypeError
	listed := append(append([]string(nil), m.Locales...), m.PartialLocales...)
	for _, ld := range locales {
		if !ld.IsDir() {
			continue
		}
		loc := ld.Name()
		catalogs := catalogFiles(filepath.Join(dir, loc))
		if len(catalogs) == 0 {
			continue
		}
		switch {
		case loc == m.Source():
			errs = append(errs, TypeError{File: catalogs[0], Line: 1, Col: 1,
				Message: fmt.Sprintf("%s is the source locale, whose text is in the `message` declarations: its catalog directory is read by nothing", loc)})
			continue
		case !slices.Contains(listed, loc):
			errs = append(errs, TypeError{File: catalogs[0], Line: 1, Col: 1,
				Message: fmt.Sprintf("%s is not a locale `[i18n]` lists: its catalog directory is read by nothing", loc)}.
				WithHint(fmt.Sprintf("list %s in `locales` or `partial_locales`, or remove %s", loc, filepath.ToSlash(filepath.Join(m.CatalogDir(), loc)))))
			continue
		}
		for _, path := range catalogs {
			rel, err := filepath.Rel(filepath.Join(dir, loc), path)
			if err != nil {
				continue
			}
			key := strings.TrimSuffix(filepath.ToSlash(rel), ".toml")
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(key)+".nomi")); err != nil {
				errs = append(errs, TypeError{File: path, Line: 1, Col: 1,
					Message: fmt.Sprintf("%s: this catalog is for %s.nomi, which does not exist", loc, key)}.
					WithHint("a catalog holds the translations of the Nomi file at its own path under the catalog directory"))
				continue
			}
			nodes, inProgram := files[key]
			if !inProgram || declaresMessages(nodes) {
				continue
			}
			text, ok := DiskInputs(path)
			if !ok {
				continue
			}
			f, _ := catalog.Parse(text)
			for _, entry := range f.Entries {
				l, c := catalog.LineCol(text, entry.Span.Start)
				el, ec := catalog.LineCol(text, entry.Span.End)
				errs = append(errs, TypeError{File: path, Line: l, Col: c, EndLine: el, EndCol: ec,
					Message: fmt.Sprintf("%s: `%s` is not a message or noun of %s", loc, entry.Name, filepath.Base(key)+".nomi")}.
					WithHint("a catalog holds the translations of its Nomi file's `message` and `noun` declarations; remove the entry, or declare it"))
			}
		}
	}
	return errs
}

// catalogFiles are the .toml files under dir, sorted.
func catalogFiles(dir string) []string {
	var out []string
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Ext(path) == ".toml" {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// declaresMessages reports whether nodes hold a message or a noun,
// expanded or not.
func declaresMessages(nodes []ast.Node) bool {
	for _, n := range nodes {
		switch d := n.(type) {
		case *ast.FuncDef:
			if messageExpansionOf(d) != nil {
				return true
			}
		case *ast.OnceBinding:
			if nounExpansionOf(d) != nil {
				return true
			}
		case *ast.KeywordDecl:
			if isBuiltinKeyword(d) {
				return true
			}
		}
	}
	return false
}
