package stdmessageformat

import (
	"context"
	"encoding/json"
	"sort"
	"sync"

	"github.com/nomi-language/nomi/internal/localefiles"
	"github.com/nomi-language/nomi/internal/mf2"
	"github.com/nomi-language/nomi/rt"
)

// Runtime is what a running program knows of its messages beyond the
// table a call holds: every message and noun table it carries, which
// Catalog.load checks a catalog against, and the translations std/locale's
// Locale.load installed from a browser build's locale files (its Loader). A
// machine has one (vm.Machine.WithMessages); a machine without one carries
// no tables, so a loaded catalog's every entry is stray, and has nothing to
// load.
type Runtime struct {
	tables func() (messages, nouns []string)
	once   sync.Once
	reg    *registry
	files  *localefiles.Loader
}

// NewRuntime is a program's Runtime over tables, which answers the encoded
// message and noun tables it carries when first asked, and fetch, which
// fetches a locale file (nil for a runner that fetches none).
func NewRuntime(tables func() (messages, nouns []string), fetch func(tag string) ([]byte, error)) *Runtime {
	return &Runtime{tables: tables, files: localefiles.NewLoader(fetch)}
}

type runtimeKey struct{}

// WithRuntime is ctx carrying r and its Loader, which the std adapters
// running on its frames read.
func WithRuntime(ctx context.Context, r *Runtime) context.Context {
	if r == nil {
		return ctx
	}
	return localefiles.WithLoader(context.WithValue(ctx, runtimeKey{}, r), r.files)
}

// runtimeOf is the Runtime fr's machine has, or nil.
func runtimeOf(fr *rt.Frame) *Runtime {
	if fr == nil || fr.Context() == nil {
		return nil
	}
	r, _ := fr.Context().Value(runtimeKey{}).(*Runtime)
	return r
}

// registry is a program's tables, decoded, by qualified name.
type registry struct {
	source   string
	messages map[string]string // name -> table text
	nouns    map[string]string // name -> noun table text
	// nounNames are the nouns' names, sorted: the order agreement reads
	// them in.
	nounNames []string
	// messageNames likewise.
	messageNames []string
}

func (r *Runtime) registry() *registry {
	if r == nil {
		return &registry{messages: map[string]string{}, nouns: map[string]string{}}
	}
	r.once.Do(func() {
		reg := &registry{messages: map[string]string{}, nouns: map[string]string{}}
		var messages, nouns []string
		if r.tables != nil {
			messages, nouns = r.tables()
		}
		for _, text := range messages {
			if t, err := mf2.DecodeTable(text); err == nil {
				reg.messages[t.Name] = text
				reg.messageNames = append(reg.messageNames, t.Name)
				reg.source = t.Source
			}
		}
		for _, text := range nouns {
			if t, err := mf2.DecodeNoun(text); err == nil {
				reg.nouns[t.Name] = text
				reg.nounNames = append(reg.nounNames, t.Name)
				reg.source = t.Source
			}
		}
		sort.Strings(reg.messageNames)
		sort.Strings(reg.nounNames)
		r.reg = reg
	})
	return r.reg
}

// catalogs caches decoded catalogs by their text: Message.format hands the
// same catalogs to every message. It holds the last catalogTexts few, since
// a program that loads a fresh catalog now and then would otherwise keep
// every one it ever loaded.
var catalogs struct {
	sync.Mutex
	byText map[string]*Translations
	order  []string
}

const catalogTexts = 32

// decodeCatalog is the catalog Catalog.load encoded as text, or nil.
func decodeCatalog(text string) *Translations {
	catalogs.Lock()
	defer catalogs.Unlock()
	if t, ok := catalogs.byText[text]; ok {
		return t
	}
	t := &Translations{}
	if err := json.Unmarshal([]byte(text), t); err != nil {
		return nil
	}
	if catalogs.byText == nil {
		catalogs.byText = map[string]*Translations{}
	}
	if len(catalogs.order) == catalogTexts {
		delete(catalogs.byText, catalogs.order[0])
		catalogs.order = catalogs.order[1:]
	}
	catalogs.byText[text] = t
	catalogs.order = append(catalogs.order, text)
	return t
}

// installedIn is the translations Locale.load installed for locale, or nil.
func (r *Runtime) installedIn(locale string) *Translations {
	if r == nil {
		return nil
	}
	return r.files.Installed(locale)
}

// Translations are one locale's texts beyond the tables.
type Translations = localefiles.Translations

// NounForms are a noun's gender and forms in one locale.
type NounForms = localefiles.NounForms
