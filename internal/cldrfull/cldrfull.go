// Package cldrfull compiles every locale's CLDR data into a binary:
// root.cldr, locales.cldr and collation-cjk.cldr, which internal/cldrgen
// writes (`go generate ./internal/cldrfull`; cldrgen's
// TestGeneratedDataIsCurrent fails while any is stale).
//
// `nomi`, the language server and the tour link it, through vmhost: a
// program run in process gets its locales' data from here, cut to what its
// nomi.toml lists (Bundle), the same bundle `nomi build` writes into an
// image. A runner (nomi-vm, the wasm runners) must not link it: it reads the
// data its image carries, and a test in vmrunner holds it to that.
//
// Importing the package makes Full the data a machine given none reads
// (cldrdata.SetDefault).
package cldrfull

import (
	_ "embed"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/stdlocale"
)

// Resolver is the date-time pattern generator a bundle with skeletons
// resolves each one by (stdformat.ResolveSkeleton, which vmhost installs:
// stdformat's tests read this package, so it cannot import stdformat).
var Resolver func(d *cldrdata.Data, tag, skeleton string) string

//go:generate go run ../cldrgen/gen

//go:embed root.cldr
var rootBlob []byte

//go:embed locales.cldr
var localesBlob []byte

//go:embed collation-cjk.cldr
var cjkBlob []byte

func init() { cldrdata.SetDefault(Full) }

// Full is every locale's data, decoded once: what a program that lists no
// locales runs with. It leaves out the large Chinese and Korean collation
// tailorings, which a program carries only when it lists those locales, so
// such a program sorts Chinese and Korean in the root order.
var Full = sync.OnceValue(func() *cldrdata.Data {
	d, err := cldrdata.Decode(rootBlob, localesBlob)
	if err != nil {
		panic("cldrfull: the compiled-in CLDR data does not decode: " + err.Error())
	}
	return d
})

// fullWithCJK is Full with the large collation tailorings, which bundles
// for listed locales are cut from.
var fullWithCJK = sync.OnceValue(func() *cldrdata.Data {
	d, err := cldrdata.Decode(rootBlob, localesBlob, cjkBlob)
	if err != nil {
		panic("cldrfull: the compiled-in CLDR data does not decode: " + err.Error())
	}
	return d
})

// Sizes are the compiled-in blobs' sizes in bytes: the root, the locales
// and the large collation tailorings.
func Sizes() (root, locales, cjk int) { return len(rootBlob), len(localesBlob), len(cjkBlob) }

// Bundle is the data a program carries: locales (canonical tags; nil for
// every locale) cut to parts, as cldrdata.Data.Select writes it. skeletons,
// when not nil, are every skeleton the program formats dates with, whose
// patterns the bundle holds resolved for each locale in place of its
// available formats (cldrdata.Selection.Skeletons).
func Bundle(locales []string, parts cldrdata.Parts, skeletons []string) []byte {
	return selection(cldrdata.Selection{Locales: locales, Parts: parts, Skeletons: skeletons})
}

// WebBundle is the data a browser build's image carries: Bundle's, with
// the number and list records of the embedded locales alone, the rest
// named as in locale files.
func WebBundle(locales, embedded []string, parts cldrdata.Parts, skeletons []string) []byte {
	return selection(cldrdata.Selection{Locales: locales, Parts: parts, Embedded: embedded, Skeletons: skeletons})
}

// FileBundle is the data a browser build's locale file carries for
// locales: their number and list records, cut to parts.
func FileBundle(locales []string, parts cldrdata.Parts, skeletons []string) []byte {
	return selection(cldrdata.Selection{Locales: locales, Parts: parts, File: true, Skeletons: skeletons})
}

// Select is the bundle sel describes, over every locale's data: Bundle,
// WebBundle or FileBundle with the rest of sel's cuts (its Currencies).
func Select(sel cldrdata.Selection) []byte { return selection(sel) }

func selection(sel cldrdata.Selection) []byte {
	d := Full()
	if sel.Locales != nil {
		d = fullWithCJK()
	}
	sel.Chain = func(tag string) []string { return stdlocale.Fallbacks(d, tag) }
	sel.PluralChain = func(tag string) []string { return stdlocale.PluralFallbacks(d, tag) }
	sel.CollationChain = func(tag string) []string { return stdlocale.CollationFallbacks(d, tag) }
	sel.Resolve = Resolver
	if sel.Skeletons != nil && Resolver == nil {
		panic("cldrfull: a bundle with skeletons needs a Resolver")
	}
	return d.Select(sel)
}

var programData sync.Map // string -> func() *cldrdata.Data

// ForProgram is the data a program that lists locales (nil: none listed)
// runs with in process: every part of its bundle, decoded, shared by every
// program that lists the same locales with the same source locale. source
// is the program's `[i18n].source_locale`, which a locale it does not carry
// formats as (cldrdata.Selection.Source); "" names none.
func ForProgram(locales []string, source string) *cldrdata.Data {
	if locales == nil {
		return Full()
	}
	key := source + "|" + strings.Join(locales, ",")
	load, _ := programData.LoadOrStore(key, sync.OnceValue(func() *cldrdata.Data {
		d, err := cldrdata.Decode(selection(cldrdata.Selection{Locales: locales, Parts: cldrdata.AllParts, Source: source}))
		if err != nil {
			panic("cldrfull: a program's CLDR bundle does not decode: " + err.Error())
		}
		return d
	}))
	return load.(func() *cldrdata.Data)()
}
