package stdmessageformat

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/catalog"
	"github.com/nomi-language/nomi/internal/catalogcheck"
	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/mf2"
	"github.com/nomi-language/nomi/internal/stdlocale"
	"github.com/nomi-language/nomi/internal/stdplural"
	"github.com/nomi-language/nomi/rt"
)

// A catalog loaded while a program runs (std/message's `Catalog.load`):
// text in a catalog's TOML, each table naming a message or noun by its
// file, `[cart.cart_items]`, checked against the program's compiled tables
// with the compiler's own checks (internal/catalogcheck):
//
//   - the locale is one `[i18n]` lists, and not the source locale;
//   - each table names a message or noun the program carries;
//   - each entry holds what the compiler holds a catalog's entry to: its
//     keys, a `source` that is the declaration's current text (a stale
//     entry is rejected, in every locale), and text that is valid MF2
//     reading only the message's parameters with the types, plural arms and
//     enum arms its signature and the locale's rules need;
//   - its nouns agree with the program's messages, and its messages with
//     the program's nouns, in that locale: a noun has every form a message
//     asks for, and a selector on gender an arm for every gender a noun has.
//
// An entry that fails any check is rejected, with the first problem the
// compiler would report about it, and is never used: the message it names
// keeps its compiled text. Completeness is not asked of a loaded catalog.

// Rejection is one entry Catalog.load refused, by its name as the catalog
// writes it ("" for a problem outside any entry), and why.
type Rejection struct {
	Key, Reason string
}

// FFILoad is `message_format.load_raw`: the accepted entries, encoded, then
// each rejection's key and reason.
func FFILoad(fr *rt.Frame, text, locale string) []string {
	encoded, rejected := LoadCatalog(stdlocale.Data(fr), runtimeOf(fr), text, locale)
	out := []string{encoded}
	for _, r := range rejected {
		out = append(out, r.Key, r.Reason)
	}
	return out
}

// runtimeLocale is tag as a loaded catalog's texts are checked in, from
// the program's CLDR data d.
func runtimeLocale(d *cldrdata.Data, tag string) mf2.Locale {
	genders, cases := d.Grammar(stdlocale.Fallbacks(d, tag))
	return mf2.Locale{
		Tag:      tag,
		Language: catalogcheck.LanguageName(tag),
		Cardinal: stdplural.Rules(d, tag, false),
		Ordinal:  stdplural.Rules(d, tag, true),
		Genders:  genders,
		Cases:    cases,
	}
}

// reason is a diagnostic's text as a rejection gives it: without the
// locale the compiler starts it with, which the rejection holds beside it.
func reason(tag, msg string) string {
	return strings.TrimPrefix(msg, tag+": ")
}

// LoadCatalog checks text, a catalog for locale, against r's tables over
// the program's CLDR data d, and answers the accepted entries encoded as
// Format reads a catalog, and the rejected ones.
func LoadCatalog(d *cldrdata.Data, r *Runtime, text, locale string) (string, []Rejection) {
	reg := r.registry()
	file, diags := catalog.ParseQualified(text)
	out := &Translations{Locale: locale, Messages: map[string]string{}, Nouns: map[string]NounForms{}}
	var rejected []Rejection
	rejectedKeys := map[string]bool{}
	reject := func(key, why string) {
		if key != "" && rejectedKeys[key] {
			return
		}
		rejectedKeys[key] = true
		rejected = append(rejected, Rejection{Key: key, Reason: why})
	}
	// A problem the TOML reader found is the entry's it is in, else the
	// catalog's.
	for _, dg := range diags {
		key := ""
		for i, e := range file.Entries {
			end := len(text)
			if i+1 < len(file.Entries) {
				end = file.Entries[i+1].Span.Start
			}
			if dg.Span.Start >= e.Span.Start && dg.Span.Start < end {
				key = e.Name
			}
		}
		if key == "" {
			rejected = append(rejected, Rejection{Reason: dg.Message})
			continue
		}
		reject(key, dg.Message)
	}
	whole := ""
	switch avail, listed := d.Available(); {
	case locale == reg.source || reg.source == "" && locale == "en":
		whole = fmt.Sprintf("%s is the source locale, whose text is in the `message` declarations", locale)
	case listed && !slices.Contains(avail, locale):
		whole = fmt.Sprintf("%s is not a locale `[i18n]` lists; a catalog loaded while the program runs translates only the locales it was built with", locale)
	}
	loc := runtimeLocale(d, locale)
	for _, entry := range file.Entries {
		if rejectedKeys[entry.Name] {
			continue
		}
		if whole != "" {
			reject(entry.Name, whole)
			continue
		}
		if !strings.Contains(entry.Name, ".") {
			reject(entry.Name, bareKey(reg, entry.Name))
			continue
		}
		if table, ok := reg.messages[entry.Name]; ok {
			t, _ := mf2.DecodeTable(table)
			decl := catalogcheck.Declaration{Name: mf2.DeclName(t.Name), Source: t.Texts[t.Source], SourceLocale: t.Source}
			if ds := catalogcheck.MessageEntry(entry, decl, t.Signature(), loc, false); len(ds) > 0 {
				reject(entry.Name, reason(locale, ds[0].Message))
				continue
			}
			m, _ := mf2.Parse(entry.Text.Text)
			out.Messages[entry.Name] = mf2.Print(m)
			continue
		}
		if table, ok := reg.nouns[entry.Name]; ok {
			t, _ := mf2.DecodeNoun(table)
			decl := catalogcheck.Declaration{Name: mf2.DeclName(t.Name), Source: t.Forms[t.Source][mf2.Nominative], SourceLocale: t.Source}
			if ds := catalogcheck.NounEntry(entry, decl, loc, false); len(ds) > 0 {
				reject(entry.Name, reason(locale, ds[0].Message))
				continue
			}
			n := NounForms{Forms: map[string]string{}}
			for _, f := range entry.Fields {
				if f.Name == catalogcheck.GenderKey {
					n.Gender = f.Value.Text
					continue
				}
				m, _ := mf2.Parse(f.Value.Text)
				n.Forms[f.Name] = mf2.Print(m)
			}
			out.Nouns[entry.Name] = n
			continue
		}
		reject(entry.Name, fmt.Sprintf("`%s` is not a message or noun the program uses", entry.Name))
	}
	for _, a := range agreement(reg, r, d, loc, out) {
		reject(a.Key, a.Reason)
	}
	return out.Encode(), rejected
}

// bareKey is why a table named name, with no file, names nothing.
func bareKey(reg *registry, name string) string {
	var named []string
	for _, n := range append(append([]string(nil), reg.messageNames...), reg.nounNames...) {
		if mf2.DeclName(n) == name {
			named = append(named, "`["+n+"]`")
		}
	}
	why := fmt.Sprintf("`%s` names no file: a catalog loaded while the program runs names each message and noun by its file, `[file.%s]`", name, name)
	if len(named) > 0 {
		why += "; the program has " + catalogcheck.JoinOr(named)
	}
	return why
}

// agreement holds the catalog's accepted entries, out, to the rest of the
// program in loc, and removes from out each entry it rejects: a loaded noun
// that lacks a form some message there asks for, or whose gender a message
// there has no arm for; a loaded message asking a noun for a form it lacks,
// or with no arm for a gender a noun there has. Each removal can expose
// another, since a removed entry's compiled text takes its place, so it
// checks again until nothing changes.
func agreement(reg *registry, r *Runtime, d *cldrdata.Data, loc mf2.Locale, out *Translations) []Rejection {
	if len(reg.nounNames) == 0 {
		return nil
	}
	var rejected []Rejection
	compiled := sources{rtm: r}
	chain := stdlocale.Fallbacks(d, loc.Tag)
	for {
		type readNoun struct {
			name   string
			loaded bool
		}
		var nouns []catalogcheck.Noun
		var from []readNoun
		for _, name := range reg.nounNames {
			text := reg.nouns[name]
			t, _ := mf2.DecodeNoun(text)
			var n nounIn
			found, loaded := false, false
			if forms, ok := out.Nouns[name]; ok {
				n, found, loaded = nounIn{gender: forms.Gender, form: func(kase string) (*mf2.Message, bool) {
					_, ok := forms.Forms[kase]
					return nil, ok
				}}, true, true
			} else {
				for _, tag := range chain {
					if tag == t.Source {
						break
					}
					if n, found = compiled.noun(text, t, tag); found {
						break
					}
				}
			}
			if !found {
				continue
			}
			nouns = append(nouns, catalogcheck.Noun{Name: mf2.DeclName(name), Qualified: name, Gender: n.gender,
				Has: func(kase string) bool { _, ok := n.form(kase); return ok }})
			from = append(from, readNoun{name, loaded})
		}
		var drop []Rejection
		for _, name := range reg.messageNames {
			table := reg.messages[name]
			t, _ := mf2.DecodeTable(table)
			text, loaded := out.Message(name)
			if !loaded {
				var ok bool
				if text, ok = compiled.message(table, t, loc.Tag); !ok {
					continue
				}
			}
			forms, arms := catalogcheck.Agreement(text, t.Signature(), loc, nouns)
			for _, f := range forms {
				key := name
				if n := from[f.Noun]; n.loaded {
					key = n.name
				}
				if key == name && !loaded {
					continue
				}
				drop = append(drop, Rejection{Key: key, Reason: reason(loc.Tag, f.Message)})
			}
			for _, a := range arms {
				if loaded {
					drop = append(drop, Rejection{Key: name, Reason: reason(loc.Tag, a.Message)})
					continue
				}
				// A compiled message has an arm for every gender a
				// compiled noun has here, so the gender is a loaded
				// noun's.
				for i, n := range nouns {
					if n.Gender == a.Gender && from[i].loaded {
						drop = append(drop, Rejection{Key: from[i].name, Reason: reason(loc.Tag, a.Message)})
					}
				}
			}
		}
		removed := 0
		for _, x := range drop {
			if _, ok := out.Messages[x.Key]; ok {
				delete(out.Messages, x.Key)
			} else if _, ok := out.Nouns[x.Key]; ok {
				delete(out.Nouns, x.Key)
			} else {
				continue
			}
			rejected = append(rejected, x)
			removed++
		}
		if removed == 0 {
			return rejected
		}
	}
}
