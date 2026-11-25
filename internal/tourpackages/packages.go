package tourpackages

//go:generate go run ./gen

import (
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/virtualproject"
)

// Package is one package the tour links for its blocks.
type Package struct {
	// Name is the package's name, its nomi.toml's, which a block imports
	// (`import sqlite`, `import sqlite::describe`).
	Name string
	// Dir is its directory in this repository, slash-separated.
	Dir string
	// Requires is the names of the tour packages its nomi.toml names, which
	// Source attaches with it (sqlkit's generate tool imports sqlite).
	Requires []string
	// Files is the text of its nomi.toml and Nomi files, its tests left
	// out, by their path in Dir: packages_sources_gen.go's copy of the tree.
	Files map[string]string
	// Embedded is the files its `embed` declarations name, by their path
	// in Dir, each in base64 as its `// FILE: <path> (base64)` section
	// holds it, so a file that is not text reaches the block unchanged.
	Embedded map[string]string
}

// Packages is every package the tour links.
var Packages = []Package{
	// sqlkit's generate tool imports postgres/describe beside
	// sqlite/describe. The driver is Nomi alone over std::net, which has no
	// sockets in the browser, so there every call answers that Postgres
	// cannot be reached.
	{Name: "postgres", Dir: "lib/sql/postgres", Files: packageSources["postgres"], Embedded: packageEmbedded["postgres"]},
	{Name: "sqlite", Dir: "lib/sql/sqlite", Files: packageSources["sqlite"], Embedded: packageEmbedded["sqlite"]},
	// migrate is sqlkit's migration runner. Its nomi.toml also names the
	// postgres driver, for its tools and tests.
	{Name: "migrate", Dir: "lib/sql/migrate", Requires: []string{"postgres", "sqlite"}, Files: packageSources["migrate"], Embedded: packageEmbedded["migrate"]},
	{Name: "sqlkit", Dir: "lib/sql/sqlkit", Requires: []string{"migrate", "postgres", "sqlite"}, Files: packageSources["sqlkit"], Embedded: packageEmbedded["sqlkit"]},
}

// PackagesDir is the directory of a block's project that Source writes the
// packages it imports under, one directory each, named by the package.
const PackagesDir = "tour_packages"

// Source is a tour block's source as the tour loads it: src unchanged,
// unless its Nomi files import one of Packages and it names no packages of
// its own (virtualproject's namesPackages), which would be the block's to
// state. Then it is src's files (a single-file block's as main.nomi), with
// a [dependencies] table that names each package the block imports and each
// tour package those name, by its directory under PackagesDir,
// in the block's own nomi.toml or in one Source writes, and each package's
// files there, its embedded ones as base64 sections.
func Source(src string) string {
	project, used := importedBy(src)
	if len(used) == 0 {
		return src
	}
	var b strings.Builder
	if strings.Contains(src, "// FILE:") {
		b.WriteString(src)
	} else {
		b.WriteString("// FILE: " + project.EntryName + ".nomi\n" + src)
	}
	if !strings.HasSuffix(src, "\n") {
		b.WriteString("\n")
	}
	deps := "[dependencies]\n"
	for _, m := range used {
		deps += m.Name + " = { dir = \"./" + PackagesDir + "/" + m.Name + "\" }\n"

	}
	if project.Manifest != nil {
		// The block's own nomi.toml gains the table.
		text := appendToSection(b.String(), "nomi.toml", "\n"+deps)
		b.Reset()
		b.WriteString(text)
	} else {
		b.WriteString("\n// FILE: nomi.toml\n[package]\nname = \"tour\"\nentry_points = [\"" + project.EntryName + "\"]\n\n" + deps)
	}
	for _, m := range used {
		dir := PackagesDir + "/" + m.Name
		for _, name := range packageFiles(m) {
			text := m.Files[name]
			b.WriteString("\n// FILE: " + dir + "/" + name + "\n")
			b.WriteString(text)
			if len(text) > 0 && text[len(text)-1] != '\n' {
				b.WriteString("\n")
			}
		}
		for _, name := range sortedKeys(m.Embedded) {
			b.WriteString("\n// FILE: " + dir + "/" + name + " (base64)\n" + m.Embedded[name])
		}
	}
	return b.String()
}

// appendToSection is src, a marked source, with text added at the end of
// its section named name.
func appendToSection(src, name, text string) string {
	lines := strings.SplitAfter(src, "\n")
	in := false
	for i, line := range lines {
		names := virtualproject.FileNames(strings.TrimRight(line, "\n"))
		ok := len(names) == 1
		switch {
		case ok && in:
			return strings.Join(lines[:i], "") + text + "\n" + strings.Join(lines[i:], "")
		case ok && names[0] == name:
			in = true
		}
	}
	return src + text
}

// Imported is the names of the packages Source attaches to src.
func Imported(src string) []string {
	_, used := importedBy(src)
	names := []string{}
	for _, m := range used {
		names = append(names, m.Name)
	}
	return names
}

// importedBy is src's project and the packages Source attaches to it.
func importedBy(src string) (virtualproject.Project, []Package) {
	project, err := virtualproject.FromMarkedSource(src)
	if err != nil || project.Files != nil {
		return project, nil
	}
	sources := []string{project.EntrySource}
	for name, text := range project.VirtualFiles {
		if !strings.HasSuffix(name, ".toml") {
			sources = append(sources, text)
		}
	}
	needed := map[string]bool{}
	var need func(name string)
	need = func(name string) {
		if needed[name] {
			return
		}
		needed[name] = true
		for _, dep := range packageNamed(name).Requires {
			need(dep)
		}
	}
	for _, m := range Packages {
		for _, text := range sources {
			if imports(text, m.Name) {
				need(m.Name)
				break
			}
		}
	}
	var used []Package
	for _, m := range Packages {
		if needed[m.Name] {
			used = append(used, m)
		}
	}
	return project, used
}

// packageNamed is the package of Packages named name.
func packageNamed(name string) Package {
	for _, m := range Packages {
		if m.Name == name {
			return m
		}
	}
	panic("tourpackages: no tour package " + name)
}

// packageFiles is the paths of m's files, in order.
func packageFiles(m Package) []string { return sortedKeys(m.Files) }

// sortedKeys is files' paths, in order.
func sortedKeys(files map[string]string) []string {
	out := make([]string, 0, len(files))
	for name := range files {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// imports reports whether the Nomi source text imports a file of the
// package named name: an import whose path starts with that segment
// (`sqlite`, `sqlite/describe`, `sqlite.{memory}`). A file that does not
// parse is read as far as it does, so an example being edited keeps its
// packages.
func imports(text, name string) bool {
	if !strings.Contains(text, name) {
		return false
	}
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(text))
	var stmts []*ast.ImportStmt
	for _, n := range nodes {
		switch x := n.(type) {
		case *ast.ImportStmt:
			stmts = append(stmts, x)
		case *ast.ImportBlock:
			stmts = append(stmts, x.Entries...)
		}
	}
	for _, s := range stmts {
		if len(s.ModulePath) == 0 {
			continue
		}
		if id, ok := s.ModulePath[0].(*ast.Ident); ok && id.Name == name {
			return true
		}
	}
	return false
}
