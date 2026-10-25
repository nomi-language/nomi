// Package localefiles holds the translations a running program has beyond
// its compiled tables, and loads a browser build's locale files.
//
// A browser build (`nomi build --web`) compiles only the locales
// `[i18n].web_embedded` names into app.wasm, and writes each other listed
// locale to a locale file, `locales/<tag>.nomil`, beside it: that locale's
// CLDR numbers and lists, and its messages' and nouns' texts, with those
// of each ancestor on its chain that is not embedded either, so one file
// serves the locale (vmhost's BuildWebImage). std/locale's `Locale.load`
// fetches the files a locale needs, refuses one another build wrote, and
// installs the rest: the CLDR data into the machine's cldrdata.Store and
// the texts into its Loader, where std/message's formatter finds them
// after the program's own tables (internal/stdmessageformat). The build
// checked every file's texts, so loading checks only the build id.
//
// The same Translations hold a catalog loaded at run time, which
// std/message's `Catalog.load` checked.
package localefiles

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/mf2"
)

// Translations are one locale's message and noun texts that are not in the
// program's tables, canonical, by qualified name.
type Translations struct {
	Locale   string               `json:"locale"`
	Messages map[string]string    `json:"messages,omitempty"`
	Nouns    map[string]NounForms `json:"nouns,omitempty"`

	mu     sync.Mutex
	parsed map[string]*mf2.Message
}

// NounForms are a noun's gender and forms in one locale.
type NounForms struct {
	Gender string            `json:"gender,omitempty"`
	Forms  map[string]string `json:"forms"`
}

// New is empty translations for locale.
func New(locale string) *Translations {
	return &Translations{Locale: locale, Messages: map[string]string{}, Nouns: map[string]NounForms{}}
}

// Parse is text, one of t's, parsed once.
func (t *Translations) Parse(text string) (*mf2.Message, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if m, ok := t.parsed[text]; ok {
		return m, m != nil
	}
	if t.parsed == nil {
		t.parsed = map[string]*mf2.Message{}
	}
	m, errs := mf2.Parse(text)
	if len(errs) > 0 {
		m = nil
	}
	t.parsed[text] = m
	return m, m != nil
}

// Message is the text of the message name, parsed, if t has it.
func (t *Translations) Message(name string) (*mf2.Message, bool) {
	if t == nil {
		return nil, false
	}
	text, ok := t.Messages[name]
	if !ok {
		return nil, false
	}
	return t.Parse(text)
}

// Noun is the noun name's gender and forms, if t has it.
func (t *Translations) Noun(name string) (NounForms, bool) {
	if t == nil {
		return NounForms{}, false
	}
	n, ok := t.Nouns[name]
	return n, ok
}

// Encode writes t as JSON.
func (t *Translations) Encode() string {
	b, err := json.Marshal(t)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// FileFormat is the version of the locale file format File is.
const FileFormat = 2

// File is a locale file's content, written as JSON.
type File struct {
	// Nomil is FileFormat; it comes first, so a file starts with Magic.
	Nomil int `json:"nomil"`
	// Build is the id of the build that wrote it (cldrdata.Data.Build).
	Build string `json:"build"`
	// Locales are the locales it installs, the one it is named for first.
	Locales []string `json:"locales"`
	// CLDR is their records, a cldrdata bundle: bytes, which JSON writes in
	// base64, since a collation table is not text.
	CLDR []byte `json:"cldr,omitempty"`
	// Texts are each locale's message and noun texts.
	Texts []*Translations `json:"texts"`
}

// Magic opens every locale file.
var Magic = []byte(fmt.Sprintf(`{"nomil":%d,`, FileFormat))

// Encode writes f.
func (f *File) Encode() []byte {
	f.Nomil = FileFormat
	b, err := json.Marshal(f)
	if err != nil {
		panic(err)
	}
	return b
}

// Decode reads a locale file.
func Decode(b []byte) (*File, error) {
	if !bytes.HasPrefix(b, Magic) {
		return nil, fmt.Errorf("it is not a locale file `nomi build --web` wrote")
	}
	f := &File{}
	if err := json.Unmarshal(b, f); err != nil {
		return nil, fmt.Errorf("it does not decode: %v", err)
	}
	return f, nil
}

// Loader is what a machine has loaded from locale files, and how it
// fetches one.
type Loader struct {
	// fetch answers a locale file's bytes by its locale, or is nil for a
	// runner that fetches none.
	fetch func(tag string) ([]byte, error)
	// load serializes Load, so each file is fetched once.
	load sync.Mutex
	mu   sync.Mutex
	// installed are the texts installed, by locale; loaded the locales
	// whose files are.
	installed map[string]*Translations
	loaded    map[string]bool
}

// NewLoader is a Loader that fetches with fetch, which may be nil.
func NewLoader(fetch func(tag string) ([]byte, error)) *Loader {
	return &Loader{fetch: fetch, installed: map[string]*Translations{}, loaded: map[string]bool{}}
}

type loaderKey struct{}

// WithLoader is ctx carrying l.
func WithLoader(ctx context.Context, l *Loader) context.Context {
	if l == nil {
		return ctx
	}
	return context.WithValue(ctx, loaderKey{}, l)
}

// From is the Loader ctx carries, or nil.
func From(ctx context.Context) *Loader {
	if ctx == nil {
		return nil
	}
	l, _ := ctx.Value(loaderKey{}).(*Loader)
	return l
}

// Installed is the texts installed for locale, or nil.
func (l *Loader) Installed(locale string) *Translations {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.installed[locale]
}

// IsLoaded reports whether locale's file is installed.
func (l *Loader) IsLoaded(locale string) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.loaded[locale]
}

// Load installs, from their files, the locales of chain (a locale's
// fallbacks, nearest first) that the data in store leaves in files and l
// has not installed yet. It answers "" when they are installed, else why
// not: "unreachable" or "unreadable", the locale whose file it is and the
// reason, tab-separated.
func Load(store *cldrdata.Store, l *Loader, chain []string) string {
	if store == nil {
		return ""
	}
	if l == nil {
		l = NewLoader(nil)
	}
	l.load.Lock()
	defer l.load.Unlock()
	failed := func(kind, tag, why string) string {
		return kind + "\t" + tag + "\t" + strings.ReplaceAll(why, "\t", " ")
	}
	d := store.Data()
	remote := d.Remote()
	for _, tag := range chain {
		if !slices.Contains(remote, tag) || l.IsLoaded(tag) {
			continue
		}
		if l.fetch == nil {
			return failed("unreachable", tag, "this runner loads no locale files")
		}
		raw, err := l.fetch(tag)
		if err != nil {
			return failed("unreachable", tag, err.Error())
		}
		f, err := Decode(raw)
		if err == nil && f.Build != d.Build() {
			err = fmt.Errorf("it was written by another build of the program")
		}
		if err == nil && (len(f.Locales) == 0 || f.Locales[0] != tag) {
			err = fmt.Errorf("it is not the file of %s", tag)
		}
		if err != nil {
			return failed("unreadable", tag, err.Error())
		}
		if len(f.CLDR) > 0 {
			for {
				cur := store.Data()
				next, err := cur.With(f.CLDR)
				if err != nil {
					return failed("unreadable", tag, err.Error())
				}
				if store.Replace(cur, next) {
					break
				}
			}
		}
		l.mu.Lock()
		for _, t := range f.Texts {
			l.installed[t.Locale] = t
		}
		for _, t := range f.Locales {
			l.loaded[t] = true
		}
		l.mu.Unlock()
	}
	return ""
}

// Loaded is the locales of available whose data the program has now: those
// the data in store does not leave in files, and those l installed.
func Loaded(store *cldrdata.Store, l *Loader, available []string) []string {
	if store == nil {
		return available
	}
	remote := store.Data().Remote()
	out := []string{}
	for _, tag := range available {
		if !slices.Contains(remote, tag) || l.IsLoaded(tag) {
			out = append(out, tag)
		}
	}
	return out
}
