package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/catalog"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

const phrasesUsage = `usage: nomi phrases accept <locale> <phrase>

accept marks a locale's translation of a phrase current: it records the
phrase's text as it is now as the source the translation was made from.
Run it once the translation says what the current text says. <phrase> is
the name of a ` + "`phrase`" + ` declaration, or path/to/file:name when two
files of the module declare one of that name.`

// runPhrases runs `nomi phrases`.
func runPhrases(args []string) error {
	if len(args) != 3 || args[0] != "accept" {
		return errors.New(phrasesUsage)
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	msg, err := acceptTranslation(wd, args[1], args[2])
	if err != nil {
		return err
	}
	fmt.Println(msg)
	return nil
}

// acceptTranslation records the current text of the phrase named name as
// the source of locale's translation of it, in the module holding dir. It
// answers what it did.
func acceptTranslation(dir, locale, name string) (string, error) {
	root := moduleRootOf(dir)
	if root == "" {
		return "", fmt.Errorf("%s is in no module: there is no nomi.toml above it", dir)
	}
	m, err := analysis.LoadManifest(root)
	if err != nil {
		return "", err
	}
	if !slices.Contains(m.Locales, locale) && !slices.Contains(m.PartialLocales, locale) {
		return "", fmt.Errorf("[i18n] lists no locale %s", locale)
	}
	file, msgName, _ := strings.Cut(name, ":")
	if msgName == "" {
		file, msgName = "", name
	}
	type found struct {
		key  string
		decl *ast.KeywordDecl
	}
	var hits []found
	catalogs := filepath.Join(root, filepath.FromSlash(m.CatalogDir()))
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && (path == catalogs || strings.HasPrefix(d.Name(), ".")) && path != root {
			return filepath.SkipDir
		}
		if d.IsDir() || filepath.Ext(path) != ".nomi" {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		key := strings.TrimSuffix(filepath.ToSlash(rel), ".nomi")
		if file != "" && key != strings.TrimSuffix(filepath.ToSlash(file), ".nomi") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))
		for _, n := range nodes {
			if kd, ok := n.(*ast.KeywordDecl); ok && kd.Keyword == "phrase" && kd.Name == msgName {
				hits = append(hits, found{key, kd})
			}
		}
		return nil
	})
	switch len(hits) {
	case 0:
		return "", fmt.Errorf("no file of the module declares `phrase %s`", msgName)
	case 1:
	default:
		var keys []string
		for _, h := range hits {
			keys = append(keys, h.key+".nomi")
		}
		sort.Strings(keys)
		return "", fmt.Errorf("`phrase %s` is declared in %s; name one as %s:%s", msgName, strings.Join(keys, " and "), hits[0].key, msgName)
	}
	h := hits[0]
	if h.decl.Body == nil {
		return "", fmt.Errorf("`phrase %s` has no text to accept a translation of", msgName)
	}
	path := filepath.Join(catalogs, locale, filepath.FromSlash(h.key)+".toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	text, err := catalog.SetSource(string(data), msgName, h.decl.Body.Value)
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return "", err
	}
	rel, _ := filepath.Rel(root, path)
	return fmt.Sprintf("%s: the %s translation of `%s` is current", filepath.ToSlash(rel), locale, msgName), nil
}

// moduleRootOf is the nearest directory at or above dir holding nomi.toml.
func moduleRootOf(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "nomi.toml")); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}
