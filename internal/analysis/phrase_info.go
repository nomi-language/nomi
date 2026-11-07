package analysis

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/catalog"
	"github.com/nomi-language/nomi/internal/cldrfull"
	"github.com/nomi-language/nomi/internal/mf2"
	"github.com/nomi-language/nomi/internal/stdlocale"
)

// What the language server reads of a `phrase` and its catalogs: the
// text each locale shows, the entries that are stale or missing, and the
// signature and locale rules a catalog's text is checked with, for hover,
// completion in catalogs and the quick fixes.

// PhraseState is what a locale's catalog holds of a phrase.
type PhraseState int

const (
	// PhraseCurrent is an entry whose recorded source is the current text.
	PhraseCurrent PhraseState = iota
	// PhraseStale is an entry translated from an older source text.
	PhraseStale
	// PhraseUnrecorded is an entry that does not record its source.
	PhraseUnrecorded
	// PhraseNoText is an entry with no `text`.
	PhraseNoText
	// PhraseMissing is no entry.
	PhraseMissing
)

// PhraseInfo is a `phrase` declaration with what each locale's catalog
// holds of it.
type PhraseInfo struct {
	Decl *ast.KeywordDecl
	// Name is the qualified name, `cart.cart_items`.
	Name string
	// Root is the module root; empty for a file in no module.
	Root string
	// SourceLocale is the locale of the declaration's own text, Source.
	SourceLocale string
	Source       string
	// Locales are the listed locales besides the source one, then the
	// partial ones, in the manifest's order.
	Locales []PhraseLocale
}

// PhraseLocale is one locale's catalog entry of a phrase.
type PhraseLocale struct {
	Locale string
	// Partial reports a locale of `partial_locales`, which may leave the
	// phrase out.
	Partial bool
	// Path is the catalog's path, Found whether it exists, and Text its
	// text when it does.
	Path  string
	Found bool
	Text  string
	// Entry is the phrase's entry in the catalog, nil when it has none.
	Entry *catalog.Entry
	State PhraseState
	// Shows is the locale whose text `Phrase.format` writes in this
	// locale: the locale itself for a current entry, else the nearest on
	// its CLDR fallback chain with a current entry, else the source locale.
	Shows string
	// Required reports that a missing entry is an error: the locale is
	// listed and nothing on its fallback chain has the text.
	Required bool
}

// PhraseOf is the phrase fn expands, with its catalogs as the expansion
// read them, or nil when fn is not a phrase's function.
func PhraseOf(fn *ast.FuncDef) *PhraseInfo {
	e := phraseExpansionOf(fn)
	if e == nil {
		return nil
	}
	info := &PhraseInfo{Decl: e.decl, Name: e.name, Root: e.root, SourceLocale: e.source}
	if e.decl.Body != nil {
		info.Source = e.decl.Body.Value
	}
	current := map[string]bool{}
	for _, c := range e.catalogs {
		l := PhraseLocale{Locale: c.locale, Partial: e.partialLocale(c.locale), Path: c.path, Found: c.found, Text: c.text}
		l.Entry = c.file.Entry(e.decl.Name)
		switch {
		case l.Entry == nil:
			l.State = PhraseMissing
		case !l.Entry.Text.Present:
			l.State = PhraseNoText
		case !l.Entry.Source.Present:
			l.State = PhraseUnrecorded
		case !mf2.Same(l.Entry.Source.Text, info.Source):
			l.State = PhraseStale
		default:
			l.State = PhraseCurrent
			current[c.locale] = true
		}
		info.Locales = append(info.Locales, l)
	}
	for i := range info.Locales {
		l := &info.Locales[i]
		l.Shows = shownLocale(l.Locale, e.source, current)
		l.Required = l.State == PhraseMissing && !l.Partial && !e.fallsBack(l.Locale, info.Source)
	}
	return info
}

// shownLocale is the locale whose text a phrase with current texts in
// current writes in locale, its source text being in source's.
func shownLocale(locale, source string, current map[string]bool) string {
	for _, l := range stdlocale.Fallbacks(cldrfull.Full(), locale) {
		if l == source || current[l] {
			return l
		}
	}
	return source
}

// TextValue is the translation the locale's entry holds, "" when it has none.
func (l PhraseLocale) TextValue() string {
	if l.Entry == nil || !l.Entry.Text.Present {
		return ""
	}
	return l.Entry.Text.Text
}

// Locale answers the catalog entry of locale, or false.
func (m *PhraseInfo) Locale(locale string) (PhraseLocale, bool) {
	for _, l := range m.Locales {
		if l.Locale == locale {
			return l, true
		}
	}
	return PhraseLocale{}, false
}

// PhraseSignature is the signature fn's phrase is checked with: its
// parameters with their resolved types, from fa, the analysis of the file
// that declares it.
func PhraseSignature(fa *FileAnalysis, fn *ast.FuncDef) mf2.Signature {
	sig := mf2.Signature{Name: fn.Name}
	var ft *FuncType
	if fa != nil {
		if sym := fa.Definitions[Pos{Line: fn.Line, Col: fn.Col}]; sym != nil {
			ft, _ = sym.Type.(*FuncType)
		}
	}
	for i, p := range fn.Params {
		var t Type
		if ft != nil && i < len(ft.Params) {
			t = ft.Params[i]
		}
		sig.Params = append(sig.Params, phraseParamOf(fa, p.Name, t))
	}
	return sig
}

// PhraseRules is the locale a text in tag is checked in: its English
// name and CLDR plural rules. source marks the source locale's text, which
// must use every parameter.
func PhraseRules(tag string, source bool) mf2.Locale { return pluralLocale(tag, source) }

// LanguageName is the English name of tag's language: `pl` is Polish.
func LanguageName(tag string) string { return languageName(tag) }

// CatalogPlace is where a catalog file sits: its module's root and
// manifest, the locale whose directory holds it, and the Nomi file whose
// translations it holds. ok is false for a TOML file that is no catalog:
// one outside every module's catalog directory, or in no locale `[i18n]`
// lists.
type CatalogPlace struct {
	Root     string
	Manifest *Manifest
	Locale   string
	// NomiPath is the declaring file's path, and Module its module key.
	NomiPath string
	Module   string
}

// CatalogAt is the place of the catalog at path, an absolute path.
func CatalogAt(path string) (CatalogPlace, bool) {
	if filepath.Ext(path) != ".toml" {
		return CatalogPlace{}, false
	}
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "nomi.toml")); err == nil {
			m, err := LoadManifest(dir)
			if err != nil || m == nil || m.Locales == nil {
				return CatalogPlace{}, false
			}
			rel, err := filepath.Rel(filepath.Join(dir, filepath.FromSlash(m.CatalogDir())), path)
			if err != nil || strings.HasPrefix(rel, "..") {
				return CatalogPlace{}, false
			}
			locale, key, ok := strings.Cut(filepath.ToSlash(rel), "/")
			if !ok || !slices.Contains(m.Locales, locale) && !slices.Contains(m.PartialLocales, locale) || locale == m.Source() {
				return CatalogPlace{}, false
			}
			key = strings.TrimSuffix(key, ".toml")
			return CatalogPlace{Root: dir, Manifest: m, Locale: locale, Module: key, NomiPath: filepath.Join(dir, filepath.FromSlash(key)+".nomi")}, true
		}
		if filepath.Dir(dir) == dir {
			return CatalogPlace{}, false
		}
	}
}

// InputText is the text of the input at path as the analysis reads it: an
// editor's buffer when one is open, else the disk's.
func (dm *DocumentManager) InputText(path string) (string, bool) {
	return dm.readInput(path)
}
