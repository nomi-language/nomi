package cldrdata

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
)

// A browser build splits its data: the image carries the root, every
// listed locale's plural rules and grammatical features, and the number
// and list records of the locales `[i18n].web_embedded` names; each other
// listed locale's records travel in a locale file the program loads
// (std/locale's `Locale.load`). The image's bundle says which (a `remote`
// record) and names the build (a `build` record), so a file from another
// build is refused. Loading decodes the file's bundle over the program's
// data (With) and puts the result in the machine's Store, which every
// frame of the machine reads.

// Remote is the locales whose number and list records are in locale files,
// not in this data: none outside a browser build.
func (d *Data) Remote() []string { return append([]string(nil), d.remote...) }

// Build is the id of the build the data was written by, "" when it names
// none: what a locale file must name to be loaded into it.
func (d *Data) Build() string { return d.build }

// WithBuild is blob with a record naming the build id.
func WithBuild(blob []byte, id string) []byte {
	return append(append([]byte(nil), blob...), "build\t"+id+"\n"...)
}

// With is d with blob decoded over it: a locale file's records added to the
// program's. d is unchanged.
func (d *Data) With(blob []byte) (*Data, error) {
	out := d.clone()
	if err := out.decode(blob); err != nil {
		return nil, err
	}
	return out, nil
}

func (d *Data) clone() *Data {
	out := newData()
	out.Version, out.NonlikelyScriptParent = d.Version, d.NonlikelyScriptParent
	for k, v := range d.parents {
		out.parents[k] = v
	}
	for k, v := range d.pluralParents {
		out.pluralParents[k] = v
	}
	for k, v := range d.cardinal {
		out.cardinal[k] = v
	}
	for k, v := range d.ordinal {
		out.ordinal[k] = v
	}
	out.cardinalSets = append([]setRecord(nil), d.cardinalSets...)
	out.ordinalSets = append([]setRecord(nil), d.ordinalSets...)
	for k, v := range d.digits {
		out.digits[k] = v
	}
	for k, v := range d.currencies {
		out.currencies[k] = v
	}
	for tag, section := range d.locales {
		s := make(map[string]string, len(section))
		for k, v := range section {
			s[k] = v
		}
		out.locales[tag] = s
	}
	out.available, out.hasAvailable = append([]string(nil), d.available...), d.hasAvailable
	out.remote, out.build, out.source = append([]string(nil), d.remote...), d.build, d.source
	if d.pending != nil {
		out.pending = make(map[string]bool, len(d.pending))
		for k, v := range d.pending {
			out.pending[k] = v
		}
	}
	out.collationRoot = d.collationRoot
	for k, v := range d.collationParents {
		out.collationParents[k] = v
	}
	return out
}

// Store is the data a running program reads, which loading a locale file
// replaces: a machine has one, and every frame it runs reads the data in
// it (From).
type Store struct {
	p atomic.Pointer[Data]
}

// NewStore is a Store holding d.
func NewStore(d *Data) *Store {
	s := &Store{}
	s.p.Store(d)
	return s
}

// Data is the data s holds now.
func (s *Store) Data() *Data { return s.p.Load() }

// Replace makes next the data s holds, when it still holds prev, and
// reports whether it did.
func (s *Store) Replace(prev, next *Data) bool { return s.p.CompareAndSwap(prev, next) }

type storeKey struct{}

// WithStore is ctx carrying s, whose data a machine's std adapters read.
func WithStore(ctx context.Context, s *Store) context.Context {
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, storeKey{}, s)
}

// StoreFrom is the Store ctx carries, or nil.
func StoreFrom(ctx context.Context) *Store {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(storeKey{}).(*Store)
	return s
}

// decodeSplit reads the records of a browser build's split: the locales
// in files, and the build's id.
func (d *Data) decodeSplit(fields []string) error {
	switch fields[0] {
	case "remote":
		if len(fields) != 2 {
			return fmt.Errorf("remote takes one field")
		}
		d.remote, d.pending = nil, map[string]bool{}
		if fields[1] != "" {
			d.remote = strings.Split(fields[1], ",")
		}
		for _, tag := range d.remote {
			d.pending[tag] = true
		}
	case "build":
		if len(fields) != 2 {
			return fmt.Errorf("build takes one field")
		}
		d.build = fields[1]
	}
	return nil
}
