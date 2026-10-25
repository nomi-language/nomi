package analysis

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/catalog"
	"github.com/nomi-language/nomi/internal/mf2"
)

// What the language server reads of a `noun` and its catalogs: each
// locale's gender and forms, and the entries that are stale or missing.

// NounInfo is a `noun` declaration with what each locale's catalog holds of
// it.
type NounInfo struct {
	Decl *ast.KeywordDecl
	// Name is the qualified name, `shop.book`.
	Name string
	// Root is the module root; empty for a file in no module.
	Root string
	// SourceLocale is the locale of the declaration's own text, Source,
	// which is the noun's nominative there.
	SourceLocale string
	Source       string
	// Locales are the listed locales besides the source one, then the
	// partial ones, in the manifest's order.
	Locales []NounLocale
}

// NounLocale is one locale's catalog entry of a noun.
type NounLocale struct {
	Locale  string
	Partial bool
	// Path is the catalog's path, Found whether it exists.
	Path  string
	Found bool
	// Entry is the noun's entry, nil when it has none; State says whether
	// it is current (MessageNoText never applies to a noun).
	Entry *catalog.Entry
	State MessageState
	// Gender is the entry's gender, "" when it gives none.
	Gender string
	// Forms are the entry's forms by case, in the order they are written.
	Forms []catalog.Field
	// Shows is the locale whose forms a message in this locale writes.
	Shows string
	// Required reports that a missing entry is an error.
	Required bool
}

// NounOf is the noun o expands, with its catalogs as the expansion read
// them, or nil when o is not a noun's value.
func NounOf(o *ast.OnceBinding) *NounInfo {
	e := nounExpansionOf(o)
	if e == nil {
		return nil
	}
	info := &NounInfo{Decl: e.decl, Name: e.name, Root: e.root, SourceLocale: e.source}
	if e.decl.Body != nil {
		info.Source = e.decl.Body.Value
	}
	current := map[string]bool{}
	for _, c := range e.catalogs {
		l := NounLocale{Locale: c.locale, Partial: e.partialLocale(c.locale), Path: c.path, Found: c.found}
		l.Entry = c.file.Entry(e.decl.Name)
		switch {
		case l.Entry == nil:
			l.State = MessageMissing
		case !l.Entry.Source.Present:
			l.State = MessageUnrecorded
		case !mf2.Same(l.Entry.Source.Text, info.Source):
			l.State = MessageStale
		default:
			l.State = MessageCurrent
			current[c.locale] = true
		}
		if l.Entry != nil {
			for _, f := range l.Entry.Fields {
				if f.Name == nounGenderKey {
					l.Gender = f.Value.Text
				} else {
					l.Forms = append(l.Forms, f)
				}
			}
		}
		info.Locales = append(info.Locales, l)
	}
	for i := range info.Locales {
		l := &info.Locales[i]
		l.Shows = shownLocale(l.Locale, e.source, current)
		l.Required = l.State == MessageMissing && !l.Partial && !e.fallsBack(l.Locale, info.Source)
	}
	return info
}

// Locale answers the catalog entry of locale, or false.
func (n *NounInfo) Locale(locale string) (NounLocale, bool) {
	for _, l := range n.Locales {
		if l.Locale == locale {
			return l, true
		}
	}
	return NounLocale{}, false
}

// CatalogGenders are the genders the nouns of a module's catalogs in one
// locale's directory give, each once, in the order the catalogs and their
// entries are written: what a selector on a noun's gender needs an arm
// for, as far as one module's catalogs tell. read reads a catalog (an
// editor's buffer or the disk); nil reads the disk.
func CatalogGenders(place CatalogPlace, read func(string) (string, bool)) []string {
	if read == nil {
		read = DiskInputs
	}
	dir := filepath.Join(place.Root, filepath.FromSlash(place.Manifest.CatalogDir()), place.Locale)
	loc := pluralLocale(place.Locale, false)
	var out []string
	seen := map[string]bool{}
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".toml") {
			return nil
		}
		text, ok := read(path)
		if !ok {
			return nil
		}
		f, _ := catalog.Parse(text)
		for _, e := range f.Entries {
			g := e.Field(nounGenderKey)
			if g.Present && !seen[g.Text] && containsString(loc.Genders, g.Text) {
				seen[g.Text] = true
				out = append(out, g.Text)
			}
		}
		return nil
	})
	return out
}

// CatalogCases are the cases the messages of a module's catalogs in one
// locale's directory ask a noun for with `:nomi:noun`, the nominative
// first, each once: the forms each noun's entry there needs, as far as one
// module's catalogs tell. read is as CatalogGenders's.
func CatalogCases(place CatalogPlace, read func(string) (string, bool)) []string {
	if read == nil {
		read = DiskInputs
	}
	dir := filepath.Join(place.Root, filepath.FromSlash(place.Manifest.CatalogDir()), place.Locale)
	loc := pluralLocale(place.Locale, false)
	out := []string{mf2.Nominative}
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".toml") {
			return nil
		}
		text, ok := read(path)
		if !ok {
			return nil
		}
		f, _ := catalog.Parse(text)
		for _, e := range f.Entries {
			if !e.Text.Present {
				continue
			}
			m, errs := mf2.Parse(e.Text.Text)
			if len(errs) > 0 {
				continue
			}
			cases, _ := mf2.NounUses(m, mf2.Signature{})
			for _, u := range cases {
				if !containsString(out, u.Case) && containsString(loc.CaseNames(), u.Case) {
					out = append(out, u.Case)
				}
			}
		}
		return nil
	})
	return out
}

func containsString(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
