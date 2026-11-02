package stdhttp

import (
	"bufio"
	"fmt"
	"go/importer"
	"go/token"
	"go/types"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// A row of inventory.txt.
type inventoryRow struct {
	pkg, name, status, text string
	phase                   int // 0 for a skipped row
	line                    int
	matched                 int
}

func readInventory(t *testing.T) []*inventoryRow {
	t.Helper()
	f, err := os.Open("inventory.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var rows []*inventoryRow
	pkg := ""
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		switch {
		case strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#"):
			continue
		case strings.HasPrefix(line, "@ "):
			pkg = strings.TrimSpace(line[2:])
			continue
		}
		parts := strings.Split(line, " | ")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		if pkg == "" || len(parts) < 3 {
			t.Fatalf("inventory.txt:%d: a row is `name | status | ...` under an `@ package` line: %q", n, line)
		}
		r := &inventoryRow{pkg: pkg, name: parts[0], status: parts[1], line: n}
		switch r.status {
		case "skipped":
			r.text = strings.Join(parts[2:], " | ")
		case "covered", "mapped":
			p, ok := strings.CutPrefix(parts[2], "phase ")
			phase, err := strconv.Atoi(p)
			if !ok || err != nil || len(parts) < 4 {
				t.Fatalf("inventory.txt:%d: a %s row is `name | %s | phase <n> | text`: %q", n, r.status, r.status, line)
			}
			r.phase = phase
			r.text = strings.Join(parts[3:], " | ")
		default:
			t.Fatalf("inventory.txt:%d: status %q is not covered, mapped or skipped", n, r.status)
		}
		if r.text == "" {
			t.Fatalf("inventory.txt:%d: the row says nothing about %s", n, r.name)
		}
		rows = append(rows, r)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}

// goExports lists every exported constant, variable, function, type,
// struct field, interface method and method of pkg, as `Name` or
// `Type.Name`.
func goExports(t *testing.T, imp types.Importer, pkg string) []string {
	t.Helper()
	p, err := imp.Import(pkg)
	if err != nil {
		t.Fatalf("importing %s: %v", pkg, err)
	}
	var names []string
	scope := p.Scope()
	for _, n := range scope.Names() {
		obj := scope.Lookup(n)
		if !obj.Exported() {
			continue
		}
		names = append(names, n)
		tn, ok := obj.(*types.TypeName)
		if !ok {
			continue
		}
		named, ok := tn.Type().(*types.Named)
		if !ok {
			continue
		}
		switch u := named.Underlying().(type) {
		case *types.Struct:
			for i := 0; i < u.NumFields(); i++ {
				if f := u.Field(i); f.Exported() {
					names = append(names, n+"."+f.Name())
				}
			}
		case *types.Interface:
			for i := 0; i < u.NumExplicitMethods(); i++ {
				names = append(names, n+"."+u.ExplicitMethod(i).Name())
			}
		}
		for i := 0; i < named.NumMethods(); i++ {
			if m := named.Method(i); m.Exported() {
				names = append(names, n+"."+m.Name())
			}
		}
	}
	return names
}

// match finds the row that answers name: an exact row, else the longest
// group with as many dots.
func match(rows []*inventoryRow, name string) *inventoryRow {
	var best *inventoryRow
	for _, r := range rows {
		if r.name == name {
			return r
		}
		if !strings.Contains(r.name, "*") || strings.Count(r.name, ".") != strings.Count(name, ".") {
			continue
		}
		if ok, _ := path.Match(r.name, name); ok && (best == nil || len(r.name) > len(best.name)) {
			best = r
		}
	}
	return best
}

// TestGoInventoryIsComplete holds inventory.txt to Go's HTTP packages: every
// exported identifier has a row, every row matches an identifier, and every
// covered row of a landed phase names a declaration std holds.
func TestGoInventoryIsComplete(t *testing.T) {
	rows := readInventory(t)
	byPkg := map[string][]*inventoryRow{}
	var pkgs []string
	for _, r := range rows {
		if _, ok := byPkg[r.pkg]; !ok {
			pkgs = append(pkgs, r.pkg)
		}
		byPkg[r.pkg] = append(byPkg[r.pkg], r)
	}
	imp := importer.ForCompiler(token.NewFileSet(), "source", nil)
	for _, pkg := range pkgs {
		for _, name := range goExports(t, imp, pkg) {
			r := match(byPkg[pkg], name)
			if r == nil {
				t.Errorf("%s: %s has no row in inventory.txt; add one that says what Nomi does with it", pkg, name)
				continue
			}
			r.matched++
		}
	}
	for _, r := range rows {
		if r.matched == 0 {
			t.Errorf("inventory.txt:%d: %s %s matches nothing Go exports; remove or rename the row", r.line, r.pkg, r.name)
		}
	}

	std := indexStd(t, filepath.Join("..", "..", "std"))
	for _, r := range rows {
		if r.status != "covered" || r.phase > LandedPhase {
			continue
		}
		if err := std.resolve(r.text); err != nil {
			t.Errorf("inventory.txt:%d: %s %s is covered by phase %d, which has landed, but %v", r.line, r.pkg, r.name, r.phase, err)
		}
	}
}

// stdIndex is what std declares, by name: each module's top-level names,
// each type's members (fields, variants, impl functions and onces) and
// each `Interface for Type` impl.
type stdIndex struct {
	modules map[string]map[string]bool
	types   map[string]map[string]bool
	impls   map[string]bool
}

func indexStd(t *testing.T, dir string) *stdIndex {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.nomi"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no std sources under %s: %v", dir, err)
	}
	ix := &stdIndex{modules: map[string]map[string]bool{}, types: map[string]map[string]bool{}, impls: map[string]bool{}}
	member := func(typ, name string) {
		if ix.types[typ] == nil {
			ix.types[typ] = map[string]bool{}
		}
		ix.types[typ][name] = true
	}
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		nodes, err := parser.Parse(lexer.Lex(string(src)))
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		mod := strings.TrimSuffix(filepath.Base(file), ".nomi")
		top := map[string]bool{}
		ix.modules[mod] = top
		declareType := func(name string) {
			top[name] = true
			if ix.types[name] == nil {
				ix.types[name] = map[string]bool{}
			}
		}
		for _, n := range nodes {
			switch d := n.(type) {
			case *ast.FuncDef:
				top[d.Name] = true
			case *ast.ExternFunc:
				top[d.Name] = true
			case *ast.OnceBinding:
				top[d.Name] = true
			case *ast.ExternType:
				declareType(d.Name)
			case *ast.TypeDef:
				declareType(d.Name)
			case *ast.InterfaceDef:
				declareType(d.Name)
				for _, m := range d.Methods {
					member(d.Name, m.Name)
				}
			case *ast.StructDef:
				declareType(d.Name)
				for _, f := range d.Fields {
					member(d.Name, f.Name)
				}
			case *ast.EnumDef:
				declareType(d.Name)
				for _, v := range d.Variants {
					member(d.Name, v.Name)
				}
			case *ast.ImplBlock:
				recv := typeName(d.Receiver)
				if d.Interface != nil {
					ix.impls[typeName(d.Interface)+" for "+recv] = true
				}
				for _, item := range d.Items {
					switch f := item.(type) {
					case *ast.FuncDef:
						member(recv, f.Name)
					case *ast.ExternFunc:
						member(recv, f.Name)
					case *ast.OnceBinding:
						member(recv, f.Name)
					}
				}
			}
		}
	}
	return ix
}

func typeName(e ast.TypeExpr) string {
	switch t := e.(type) {
	case *ast.SimpleType:
		return t.Name
	case *ast.GenericType:
		return t.Name
	case *ast.QualifiedType:
		return typeName(t.Member)
	}
	return fmt.Sprint(e)
}

var (
	firstCode   = regexp.MustCompile("`([^`]+)`")
	implForm    = regexp.MustCompile(`^(\w+) for ([\w.]+)`)
	literalForm = regexp.MustCompile(`^#([A-Z]\w*)\{\.\.\., (\w+):`)
	dottedName  = regexp.MustCompile(`^[A-Za-z_]\w*(\.[A-Za-z_]\w*)*`)
)

// resolve checks the Nomi name in text's first backticks: `Type`,
// `Type.member`, `module.name`, `module.Type.member`, `Iface for Type` or
// `Type{..., field: ...}`.
func (ix *stdIndex) resolve(text string) error {
	m := firstCode.FindStringSubmatch(text)
	if m == nil {
		return fmt.Errorf("its text names nothing in backticks")
	}
	code := m[1]
	if f := implForm.FindStringSubmatch(code); f != nil {
		parts := strings.Split(f[2], ".")
		if !ix.impls[f[1]+" for "+parts[len(parts)-1]] {
			return fmt.Errorf("std has no `impl %s for %s`", f[1], f[2])
		}
		return nil
	}
	if f := literalForm.FindStringSubmatch(code); f != nil {
		return ix.member(f[1], f[2])
	}
	name := dottedName.FindString(strings.TrimPrefix(code, "#"))
	parts := strings.Split(name, ".")
	switch {
	case name == "":
		return fmt.Errorf("`%s` is not a name std can be checked for", code)
	case len(parts) == 1:
		if ix.types[parts[0]] == nil {
			return fmt.Errorf("std declares no type %s", parts[0])
		}
		return nil
	case len(parts) == 2 && ix.modules[parts[0]] != nil && unicode.IsLower(rune(parts[0][0])):
		if !ix.modules[parts[0]][parts[1]] {
			return fmt.Errorf("std/%s.nomi declares no %s", parts[0], parts[1])
		}
		return nil
	case len(parts) == 2:
		return ix.member(parts[0], parts[1])
	case len(parts) == 3 && ix.modules[parts[0]] != nil:
		if !ix.modules[parts[0]][parts[1]] {
			return fmt.Errorf("std/%s.nomi declares no %s", parts[0], parts[1])
		}
		return ix.member(parts[1], parts[2])
	}
	return fmt.Errorf("`%s` is not a name std can be checked for", code)
}

func (ix *stdIndex) member(typ, name string) error {
	members, ok := ix.types[typ]
	if !ok {
		return fmt.Errorf("std declares no type %s", typ)
	}
	if !members[name] {
		var have []string
		for m := range members {
			have = append(have, m)
		}
		sort.Strings(have)
		return fmt.Errorf("std's %s has no %s (it has %s)", typ, name, strings.Join(have, ", "))
	}
	return nil
}

// The check behind "covered" finds what std declares and refuses what it
// does not, so a covered row cannot name a function std lacks.
func TestInventoryNamesResolveAgainstStd(t *testing.T) {
	std := indexStd(t, filepath.Join("..", "..", "std"))
	for _, ok := range []string{"`String.trim(s)`", "`Display for Int`", "`io.Error.NotFound`", "`url.escape_query(text)`", "`Duration`", "`Command{..., dir: Some(d)}`"} {
		if err := std.resolve(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, missing := range []string{"`String.no_such_function(s)`", "`Display for NoSuchType`", "`io.Error.NoSuchVariant`", "`url.no_such_function`", "`NoSuchType`", "`with_server(..., tls: True)`"} {
		if err := std.resolve(missing); err == nil {
			t.Errorf("%s resolved against std, which does not declare it", missing)
		}
	}
}
