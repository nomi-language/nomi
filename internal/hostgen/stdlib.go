package hostgen

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// FuncRow is one row of a binding table: the name a `host fn` is registered
// under and the Go function implementing it.
type FuncRow struct {
	Name string
	// Fn is the Go function value; Go describes a function read from source
	// instead. Exactly one is set.
	Fn any
	Go *GoFunc
	// PanicsPropagate leaves a panic in the Go function to the adapter's
	// caller. Unset, the adapter turns every panic into its error with the
	// marshaller's text (`<name>: panic: <value>`), which is the FFI
	// contract. Set, a panic unwinds through the adapter untouched: an rt
	// trap (*rt.Error) stays a trap the engine reports as a Nomi fault with
	// rt's text, and a cancellation unwind (rt.TimerSleep on a cancelled
	// frame) keeps unwinding the task. It is a property of the row, so one
	// generated file serves both policies.
	PanicsPropagate bool
}

// TypeRow is one registered `host type`: its qualified Nomi name and the Go
// type behind it, as a typed nil (Prototype) or, for a type read from source,
// as a Type (Go).
type TypeRow struct {
	Name      string
	Prototype any
	Go        Type
}

// Table is everything one generated file is derived from.
type Table struct {
	Package string
	Header  string
	// Source reads a module's Nomi source by module name.
	Source func(module string) ([]byte, bool)
	Types  []TypeRow
	Funcs  []FuncRow
	// ModuleOf names the module that declares the `host fn` a row binds.
	ModuleOf func(row FuncRow) (string, error)
}

// Generate renders one adapter per row of tb.Funcs.
func Generate(tb Table) ([]byte, error) {
	g, err := tb.generator()
	if err != nil {
		return nil, err
	}
	return g.Source(tb.Header)
}

// generator loads tb's modules and adds every row, failing on the first
// refusal.
func (tb Table) generator() (*Generator, error) {
	modules, err := LoadModules(tb.Source)
	if err != nil {
		return nil, err
	}
	handles, err := Handles(modules, tb.Types)
	if err != nil {
		return nil, err
	}
	var bindings []Binding
	for _, row := range tb.Funcs {
		module, err := tb.ModuleOf(row)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", row.Name, err)
		}
		hf, err := FindHostFunc(modules, module, row.Name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", row.Name, err)
		}
		bindings = append(bindings, Binding{Key: row.Name, Fn: row.Fn, Go: row.Go, Decl: hf, PanicsPropagate: row.PanicsPropagate})
	}
	g := New(tb.Package, modules.Resolver(), handles)
	for _, b := range bindings {
		if err := g.Add(b); err != nil {
			return nil, err
		}
	}
	return g, nil
}

// Modules is a set of loaded Nomi modules by name.
type Modules struct {
	source func(module string) ([]byte, bool)
	byName map[string]*Module
}

// NewModules starts an empty module set over source, loading only what is
// asked for. LoadModules is the set an adapter is generated from.
func NewModules(source func(module string) ([]byte, bool)) *Modules {
	return &Modules{source: source, byName: map[string]*Module{}}
}

// LoadModules starts a module set over source, with std's prelude enums
// loaded and their tag order checked.
func LoadModules(source func(module string) ([]byte, bool)) (*Modules, error) {
	ms := NewModules(source)
	for _, prelude := range []string{"maybe", "results"} {
		if _, err := ms.Load(prelude); err != nil {
			return nil, err
		}
	}
	if err := checkPreludeTags(ms.byName["maybe"], "Maybe", "Some", "None"); err != nil {
		return nil, err
	}
	if err := checkPreludeTags(ms.byName["results"], "Result", "Ok", "Err"); err != nil {
		return nil, err
	}
	return ms, nil
}

// Load parses the module name and, transitively, the modules it imports that
// the source answers.
func (ms *Modules) Load(name string) (*Module, error) {
	if m, ok := ms.byName[name]; ok {
		return m, nil
	}
	src, ok := ms.source(name)
	if !ok {
		return nil, fmt.Errorf("module %s: no source", name)
	}
	nodes, err := parser.Parse(lexer.Lex(string(src)))
	if err != nil {
		return nil, fmt.Errorf("module %s: %w", name, err)
	}
	m := &Module{Name: name, Nodes: nodes}
	ms.byName[name] = m
	for _, imported := range importedModules(nodes) {
		if _, ok := ms.source(imported); !ok {
			continue
		}
		if _, err := ms.Load(imported); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// Resolver resolves types over every loaded module.
func (ms *Modules) Resolver() *Resolver {
	mods := make([]*Module, 0, len(ms.byName))
	for _, m := range ms.byName {
		mods = append(mods, m)
	}
	return NewResolver(mods...)
}

// FindHostFunc is the one `host fn` in module that a binding table names by
// key.
func FindHostFunc(ms *Modules, module, key string) (HostFunc, error) {
	m, err := ms.Load(module)
	if err != nil {
		return HostFunc{}, err
	}
	var found []HostFunc
	for _, hf := range HostFuncs(m) {
		for _, k := range hf.Keys {
			if k == key {
				found = append(found, hf)
				break
			}
		}
	}
	if len(found) > 1 {
		// An inherent declaration and an interface impl's may both answer a
		// fallback spelling (`decimal.Decimal.divide` is the inherent
		// `divide` and, less specifically, `impl Divide<Decimal, Decimal>`'s).
		// The declaration whose most specific key is this one wins, which is
		// the front end's rule: inherent beats interface impl.
		var exact []HostFunc
		for _, hf := range found {
			if hf.Keys[0] == key {
				exact = append(exact, hf)
			}
		}
		if len(exact) == 1 {
			found = exact
		}
	}
	switch len(found) {
	case 0:
		return HostFunc{}, fmt.Errorf("no `host fn` in module %s is registered under this name", module)
	case 1:
		return found[0], nil
	}
	return HostFunc{}, fmt.Errorf("%d `host fn` declarations in module %s answer to this name", len(found), module)
}

// Handles maps every registered host type's Go type, T and *T, to its
// qualified Nomi name, by TypeKey. Each type's module is loaded.
func Handles(ms *Modules, types []TypeRow) (map[string]string, error) {
	handles := map[string]string{}
	for _, tr := range types {
		t := tr.Go
		if t == nil {
			rt := reflect.TypeOf(tr.Prototype)
			if rt == nil {
				return nil, fmt.Errorf("host type %s: nil prototype", tr.Name)
			}
			t = ReflectType(rt)
		}
		handles[TypeKey(t)] = tr.Name
		if t.Kind() == reflect.Pointer {
			handles[TypeKey(t.Elem())] = tr.Name
		} else {
			handles["*"+TypeKey(t)] = tr.Name
		}
		module, _, found := strings.Cut(tr.Name, ".")
		if !found {
			return nil, fmt.Errorf("host type %s: a registered host type name is qualified by its module", tr.Name)
		}
		if _, err := ms.Load(module); err != nil {
			return nil, fmt.Errorf("host type %s: %w", tr.Name, err)
		}
	}
	return handles, nil
}

// DirSource reads a module's Nomi source from dir/<module>.nomi, which is how
// a caller that cannot import nomi/std (internal/ffirun, which nomi/analysis
// imports) reads the standard library: from the compiler's source tree.
func DirSource(dir string) func(module string) ([]byte, bool) {
	return func(module string) ([]byte, bool) {
		data, err := os.ReadFile(filepath.Join(dir, module+".nomi"))
		return data, err == nil
	}
}

// checkPreludeTags holds the tag order generated code assumes: the enum's
// variants in declaration order.
func checkPreludeTags(m *Module, enum string, variants ...string) error {
	d, ok := typeDecl(m.Nodes, enum).(*ast.EnumDef)
	if !ok {
		return fmt.Errorf("std/%s declares no enum %s", m.Name, enum)
	}
	if len(d.Variants) != len(variants) {
		return fmt.Errorf("std/%s: %s has %d variants; the generator assumes %v", m.Name, enum, len(d.Variants), variants)
	}
	for i, v := range variants {
		if d.Variants[i].Name != v {
			return fmt.Errorf("std/%s: %s variant %d is %s; the generator assumes %v", m.Name, enum, i, d.Variants[i].Name, variants)
		}
	}
	return nil
}
