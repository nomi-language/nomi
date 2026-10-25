package analysis

import (
	"fmt"
	"slices"
	"sort"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/catalog"
	"github.com/nomi-language/nomi/internal/catalogcheck"
	"github.com/nomi-language/nomi/internal/cldrfull"
	"github.com/nomi-language/nomi/internal/mf2"
	"github.com/nomi-language/nomi/internal/stdlocale"
)

// Agreement is checked over the whole program, because a message may be
// passed any noun the program declares and the set of nouns is closed:
// every noun is a declaration in a file the program reaches. In each locale
// a program's messages are checked in (each message's listed and partial
// locales):
//
//   - every noun has every case form that some message's text in that
//     locale asks for with `:nomi:noun case=...`, the error being the
//     noun's, at its entry;
//   - every selector on a noun's gender (`:nomi:gender`) has an arm for
//     each gender some noun has in that locale, the error being the
//     message's, at the selector, naming the noun that has the gender.
//
// A noun is read in a locale as its own current entry there, else the
// nearest listed ancestor's on its CLDR chain; one with neither (in a
// partial locale, or with its missing entry already reported) asks for
// nothing.

// agreementNoun is one noun of the program as the agreement check reads
// it.
type agreementNoun struct {
	key string
	e   *nounExpansion
}

// nounEntryIn is the entry n is read from in locale, and the catalog it is
// in: its own current one, else that of the nearest listed ancestor whose
// entry is current, else nil.
func (n agreementNoun) entryIn(locale string) (*messageCatalog, *catalog.Entry) {
	source := ""
	if n.e.decl.Body != nil {
		source = n.e.decl.Body.Value
	}
	for i, tag := range stdlocale.Fallbacks(cldrfull.Full(), locale) {
		if tag == n.e.source {
			return nil, nil
		}
		if i > 0 && !slices.Contains(n.e.locales, tag) {
			continue
		}
		for _, cat := range n.e.catalogs {
			if cat.locale != tag {
				continue
			}
			if entry := cat.file.Entry(n.e.decl.Name); nounEntryCurrent(entry, source) {
				return cat, entry
			}
		}
	}
	return nil, nil
}

// CheckAgreement holds every noun of a program to every message, as the
// comment above says, over files, every file of the program by module key
// with its expanded nodes. The errors are by the key of the file they are
// the declaration's of: a missing form the noun's, a missing arm the
// message's.
func CheckAgreement(files map[string][]ast.Node) map[string][]TypeError {
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var nouns []agreementNoun
	type agreementMessage struct {
		key string
		e   *messageExpansion
	}
	var messages []agreementMessage
	for _, key := range keys {
		for _, n := range files[key] {
			switch d := n.(type) {
			case *ast.OnceBinding:
				if e := nounExpansionOf(d); e != nil && e.decl.Body != nil {
					nouns = append(nouns, agreementNoun{key, e})
				}
			case *ast.FuncDef:
				if e := messageExpansionOf(d); e != nil && e.decl.Body != nil {
					messages = append(messages, agreementMessage{key, e})
				}
			}
		}
	}
	if len(nouns) == 0 || len(messages) == 0 {
		return nil
	}
	var out map[string][]TypeError
	seen := map[string]bool{}
	add := func(key string, te TypeError) {
		id := fmt.Sprintf("%s\x00%d\x00%d\x00%s", te.File, te.Line, te.Col, te.Message)
		if seen[id] {
			return
		}
		seen[id] = true
		if out == nil {
			out = map[string][]TypeError{}
		}
		out[key] = append(out[key], te)
	}
	for _, m := range messages {
		sig := mf2.Signature{Name: m.e.decl.Name}
		for _, p := range m.e.decl.Params {
			kind := mf2.KindOther
			if m.e.wrappers[p.Name] == "noun" {
				kind = mf2.KindNoun
			}
			sig.Params = append(sig.Params, mf2.Param{Name: p.Name, Kind: kind})
		}
		source := m.e.decl.Body.Value
		for _, cat := range m.e.catalogs {
			entry := cat.file.Entry(m.e.decl.Name)
			if entry == nil || !entry.Text.Present || !entry.Source.Present || !mf2.Same(entry.Source.Text, source) {
				continue
			}
			text, errs := mf2.Parse(entry.Text.Text)
			if len(errs) > 0 {
				continue
			}
			loc := pluralLocale(cat.locale, false)
			// The nouns as this locale reads them: their entries here, or
			// their nearest listed ancestor's.
			var read []catalogcheck.Noun
			var readFrom []agreementNoun
			var readEntry []*catalog.Entry
			var readCat []*messageCatalog
			for _, n := range nouns {
				ncat, nentry := n.entryIn(cat.locale)
				if nentry == nil {
					continue
				}
				g := nentry.Field(catalogcheck.GenderKey)
				read = append(read, catalogcheck.Noun{Name: n.e.decl.Name, Qualified: n.e.name, Gender: g.Text,
					Has: func(kase string) bool { return nentry.Field(kase).Present }})
				readFrom, readEntry, readCat = append(readFrom, n), append(readEntry, nentry), append(readCat, ncat)
			}
			forms, arms := catalogcheck.Agreement(text, sig, loc, read)
			for _, f := range forms {
				nentry := readEntry[f.Noun]
				add(readFrom[f.Noun].key, catalogError(readCat[f.Noun], nentry.Span.Start, nentry.Span.End, f.Message, f.Hint))
			}
			for _, a := range arms {
				start, end := entry.Text.Offset(a.Selector.Span.Start), entry.Text.Offset(a.Selector.Span.End)
				if !entry.Text.Exact {
					start, end = entry.Text.Span.Start, entry.Text.Span.End
				}
				add(m.key, catalogError(cat, start, end, a.Message, a.Hint))
			}
		}
	}
	return out
}

// CheckProgramDeclarations is what the program checks over every file's
// keyword declarations once every file is checked: each macro's
// whole-module check (CheckMacroPrograms) and the agreement of nouns
// and messages (CheckAgreement), by the key of the file each error is
// reported with.
func CheckProgramDeclarations(files map[string][]ast.Node) map[string][]TypeError {
	out := CheckMacroPrograms(files)
	for key, errs := range CheckAgreement(files) {
		if out == nil {
			out = map[string][]TypeError{}
		}
		out[key] = append(out[key], errs...)
	}
	return out
}
