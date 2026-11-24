package stdtable

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"sort"
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/stdlibbindings"
	"github.com/nomi-language/nomi/internal/stdlibbindings/families"
)

// Family is one of the optional families (internal/stdlibbindings/families),
// with what generating its files needs.
type Family struct{ families.Family }

// Families are the optional families, in their table's order.
var Families = func() []Family {
	out := make([]Family, len(families.All))
	for i, f := range families.All {
		out[i] = Family{f}
	}
	return out
}()

// constraint is the build constraint of the builds that have f: neither
// its tag nor a GOOS it is absent on.
func (f Family) constraint() string {
	parts := []string{"!" + f.Tag}
	for _, goos := range f.Absent {
		parts = append(parts, "!"+goos)
	}
	return strings.Join(parts, " && ")
}

// absentConstraint is the build constraint of the builds without f: its
// tag, or a GOOS it is absent on.
func (f Family) absentConstraint() string {
	parts := []string{f.Tag}
	parts = append(parts, f.Absent...)
	return strings.Join(parts, " || ")
}

// message is the error every stub of f answers.
func (f Family) message() string {
	msg := "this nomi-vm was built without " + f.What + "; build the program with `nomi build`, which picks a runner with all it uses"
	for _, goos := range f.Absent {
		msg += "; no runner for " + goos + " has " + f.What
	}
	return msg
}

// linkedConst is the constant that says whether a build has f.
func (f Family) linkedConst() string { return "has" + strings.ToUpper(f.Key[:1]) + f.Key[1:] }

// errName is the error every stub of f answers.
func (f Family) errName() string { return "errNo" + strings.ToUpper(f.Key[:1]) + f.Key[1:] }

// Files is the generated adapter package's source, by file name.
type Files map[string][]byte

// GenerateFiles is Generate's source split by family: adapters_gen.go,
// every adapter that calls no family's package, plus Families, Pure,
// Unbounded, Seeded and Held (the rows internal/stdlibbindings marks so) and Known
// (whether a row answers a crossing); and for each family
// adapters_<key>_gen.go, the adapters that call its package, built where
// the family is, and adapters_no<key>_gen.go, an error-answering stub for
// each of those, built where it is not. Bind names every adapter in every
// build, so the runners answer the same crossings and differ only in what
// a family's crossings do. The same step writes internal/effects's list of
// std names (StdNamesFile, stdNamesSource), by its path from
// internal/stdlibadapters.
func GenerateFiles() (Files, error) {
	src, err := Generate()
	if err != nil {
		return nil, err
	}
	files, err := splitFamilies(src)
	if err != nil {
		return nil, err
	}
	if files[StdNamesFile], err = stdNamesSource(); err != nil {
		return nil, err
	}
	return files, nil
}

func splitFamilies(src []byte) (Files, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "adapters_gen.go", src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("stdtable: parsing the generated source: %w", err)
	}
	aliases := map[string]int{} // a family package's import alias, to its index
	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		for i, fam := range Families {
			if path != fam.Package {
				continue
			}
			alias := path[strings.LastIndex(path, "/")+1:]
			if imp.Name != nil {
				alias = imp.Name.Name
			}
			aliases[alias] = i
		}
	}
	moved := make([][]ast.Decl, len(Families))
	movedFns := make([]map[string]bool, len(Families))
	for i := range Families {
		movedFns[i] = map[string]bool{}
	}
	var keep []ast.Decl
	for _, d := range f.Decls {
		fam, calls := familyOf(d, aliases)
		if !calls {
			keep = append(keep, d)
			continue
		}
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Recv == nil {
			return nil, fmt.Errorf("stdtable: a declaration other than an adapter method calls %s at %s", Families[fam].Package, fset.Position(d.Pos()))
		}
		moved[fam] = append(moved[fam], d)
		movedFns[fam][fd.Name.Name] = true
	}

	header := leadingComment(src)
	drop := map[string]bool{}
	for alias := range aliases {
		drop[alias] = true
	}
	main, err := renderFile(fset, f.Comments, header, "", f.Name.Name, f.Imports, keep, drop)
	if err != nil {
		return nil, err
	}
	files := Files{}
	var table bytes.Buffer
	for i, fam := range Families {
		if len(moved[i]) == 0 {
			return nil, fmt.Errorf("stdtable: no adapter calls %s, the package of the family %s", fam.Package, fam.Key)
		}
		names := crossingNames(f, movedFns[i])
		if len(names) != len(movedFns[i]) {
			return nil, fmt.Errorf("stdtable: %d adapters of the family %s, but Bind and its rows name %d of them", len(movedFns[i]), fam.Key, len(names))
		}
		fmt.Fprintf(&table, "\t\t{Family: families.All[%d], Linked: %s, Names: []string{\n", i, fam.linkedConst())
		for _, n := range names {
			fmt.Fprintf(&table, "\t\t\t%q,\n", n)
		}
		table.WriteString("\t\t}},\n")

		others := map[string]bool{}
		for alias, j := range aliases {
			if j != i {
				others[alias] = true
			}
		}
		with, err := renderFile(fset, f.Comments, header, fam.constraint(), f.Name.Name, f.Imports, moved[i], others)
		if err != nil {
			return nil, err
		}
		with = append(with, fmt.Sprintf("\n// %s reports whether this build answers %s's crossings with\n"+
			"// their adapters.\nconst %s = true\n", fam.linkedConst(), fam.Key, fam.linkedConst())...)
		if with, err = format.Source(with); err != nil {
			return nil, fmt.Errorf("stdtable: formatting adapters_%s_gen.go: %w", fam.Key, err)
		}
		without, err := stubFile(fset, header, f.Name.Name, fam, moved[i])
		if err != nil {
			return nil, err
		}
		files["adapters_"+fam.Key+"_gen.go"] = with
		files["adapters_no"+fam.Key+"_gen.go"] = without
	}
	main = bytes.Replace(main, []byte("\nimport (\n"), []byte("\nimport (\n\t\""+familiesPackage+"\"\n"), 1)
	main = append(main, familiesFunc(table.String())...)
	main = append(main, pureFunc(stdlibbindings.Pure())...)
	main = append(main, unboundedFunc(stdlibbindings.Unbounded())...)
	main = append(main, seededFunc(stdlibbindings.Seeded())...)
	main = append(main, heldFunc(stdlibbindings.Held())...)
	main = append(main, knownFunc()...)
	if main, err = format.Source(main); err != nil {
		return nil, fmt.Errorf("stdtable: formatting adapters_gen.go: %w", err)
	}
	files["adapters_gen.go"] = main
	return files, nil
}

// familyOf is the index of the family whose package d names, and whether
// it names one.
func familyOf(d ast.Decl, aliases map[string]int) (int, bool) {
	fam, found := 0, false
	ast.Inspect(d, func(n ast.Node) bool {
		if s, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := s.X.(*ast.Ident); ok {
				if i, ok := aliases[id.Name]; ok {
					fam, found = i, true
				}
			}
		}
		return !found
	})
	return fam, found
}

// crossingNames is the crossing names that land on a method in fns,
// sorted: a boxed adapter Bind puts in its table (`funcs["name"] =
// b.fnN`), or a register form a row lists (`{Key: "name", Fn:
// (*bound).fnNRegs, ...}`).
func crossingNames(f *ast.File, fns map[string]bool) []string {
	var out []string
	add := func(key ast.Expr, method *ast.SelectorExpr) {
		lit, isLit := key.(*ast.BasicLit)
		if !isLit || method == nil || !fns[method.Sel.Name] {
			return
		}
		if name, err := strconv.Unquote(lit.Value); err == nil {
			out = append(out, name)
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			if ix, ok := n.Lhs[0].(*ast.IndexExpr); ok && len(n.Rhs) == 1 {
				sel, _ := n.Rhs[0].(*ast.SelectorExpr)
				add(ix.Index, sel)
			}
		case *ast.CompositeLit:
			var key ast.Expr
			var fn *ast.SelectorExpr
			for _, e := range n.Elts {
				kv, ok := e.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				switch id, _ := kv.Key.(*ast.Ident); {
				case id == nil:
				case id.Name == "Key":
					key = kv.Value
				case id.Name == "Fn":
					fn, _ = kv.Value.(*ast.SelectorExpr)
				}
			}
			if key != nil {
				add(key, fn)
			}
		}
		return true
	})
	sort.Strings(out)
	return out
}

// familiesPackage is the table of optional families the generated
// Families reads.
const familiesPackage = "github.com/nomi-language/nomi/internal/stdlibbindings/families"

// familiesFunc is Families, the families with their crossings as rows
// lists them.
func familiesFunc(rows string) []byte {
	return []byte("\n// Family is an optional family of crossings (internal/stdlibbindings/families),\n" +
		"// with what this build does with it.\ntype Family struct {\n\tfamilies.Family\n" +
		"\t// Linked reports whether this build answers the family's crossings\n" +
		"\t// with their adapters.\n\tLinked bool\n" +
		"\t// Names is every crossing whose adapter calls the family's package,\n" +
		"\t// sorted.\n\tNames []string\n}\n\n" +
		"// Families are the optional families. The runners come in two tiers,\n" +
		"// never a combination: the lean one, built with every family's tag\n" +
		"// (families.LeanTags), has none, and the full one has every family, so\n" +
		"// a new family joins the full tier and adds no runner. `nomi build`\n" +
		"// appends a program that reaches none of their crossings to the lean\n" +
		"// runner, and a runner refuses before it runs a program that reaches a\n" +
		"// family it does not link.\n" +
		"func Families() []Family {\n\treturn []Family{\n" + rows + "\t}\n}\n")
}

// pureFunc is Pure, which answers the rows marked Pure from a table built
// into the binary rather than from internal/stdlibbindings, which the
// runners do not link.
func pureFunc(pure map[string]bool) []byte {
	return markFunc(pure, "pure", "Pure",
		"// Pure reports whether the crossing name lands on an adapter whose row is\n"+
			"// marked Pure: one that computes its answer from its operands alone and\n"+
			"// acts on nothing outside the machine (stdlibbindings.Binding.Pure).\n")
}

// unboundedFunc is Unbounded, which answers the rows marked Unbounded as
// pureFunc answers Pure.
func unboundedFunc(unbounded map[string]bool) []byte {
	return markFunc(unbounded, "unbounded", "Unbounded",
		"// Unbounded reports whether the crossing name lands on an adapter whose\n"+
			"// row is marked Unbounded: a pure function whose one call's cost grows\n"+
			"// with an operand's value (stdlibbindings.Binding.Unbounded).\n")
}

// seededFunc is Seeded, which answers the rows marked Seeded as pureFunc
// answers Pure.
func seededFunc(seeded map[string]bool) []byte {
	return markFunc(seeded, "seeded", "Seeded",
		"// Seeded reports whether the crossing name lands on an adapter whose\n"+
			"// row is marked Seeded: a pure function whose answer is a hash keyed by\n"+
			"// the run's seed (stdlibbindings.Binding.Seeded).\n")
}

// heldFunc is Held, which answers the rows marked Held as pureFunc answers
// Pure.
func heldFunc(held map[string]bool) []byte {
	return markFunc(held, "held", "Held",
		"// Held reports whether the crossing name lands on an adapter whose row\n"+
			"// is marked Held: one that acts only through a value its operand already\n"+
			"// holds, which an effects list names where the value was made\n"+
			"// (stdlibbindings.Binding.Held).\n")
}

// markFunc is the function fn, documented by doc, answering whether a
// crossing's row carries a mark, from the table named table of the rows
// in marked.
func markFunc(marked map[string]bool, table, fn, doc string) []byte {
	names := make([]string, 0, len(marked))
	for n := range marked {
		names = append(names, n)
	}
	sort.Strings(names)
	var b bytes.Buffer
	fmt.Fprintf(&b, "\n// %s is every crossing whose row in internal/stdlibbindings is marked\n"+
		"// %s, sorted.\nvar %s = map[string]bool{\n", table, fn, table)
	for _, n := range names {
		fmt.Fprintf(&b, "\t%q: true,\n", n)
	}
	fmt.Fprintf(&b, "}\n\n%sfunc %s(name string) bool { return %s[name] }\n", doc, fn, table)
	return b.Bytes()
}

// knownFunc is Known, which answers whether a row adapts a crossing from
// the list Names already holds, as a set built once.
func knownFunc() []byte {
	return []byte("\n// known is every crossing Names lists, as a set.\n" +
		"var known = func() map[string]bool {\n" +
		"\tm := map[string]bool{}\n" +
		"\tfor _, n := range Names() {\n\t\tm[n] = true\n\t}\n" +
		"\treturn m\n}()\n\n" +
		"// Known reports whether the crossing name lands on an adapter: whether a\n" +
		"// row in internal/stdlibbindings answers it.\n" +
		"func Known(name string) bool { return known[name] }\n")
}

// leadingComment is the generated file's comment block before `package`.
func leadingComment(src []byte) string {
	i := bytes.Index(src, []byte("\npackage "))
	if i < 0 {
		return ""
	}
	return string(src[:i+1])
}

// renderFile writes decls as one file of package pkg under header, with
// constraint as its build line when set, importing only what decls use.
// drop names import aliases never kept.
func renderFile(fset *token.FileSet, comments []*ast.CommentGroup, header, constraint, pkg string, imports []*ast.ImportSpec, decls []ast.Decl, drop map[string]bool) ([]byte, error) {
	var body bytes.Buffer
	cfg := printer.Config{Mode: printer.UseSpaces | printer.TabIndent, Tabwidth: 8}
	for _, d := range decls {
		if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
			continue
		}
		body.WriteString("\n")
		if err := cfg.Fprint(&body, fset, &printer.CommentedNode{Node: d, Comments: comments}); err != nil {
			return nil, fmt.Errorf("stdtable: printing a declaration: %w", err)
		}
		body.WriteString("\n")
	}
	used := usedAliases(decls)
	var out bytes.Buffer
	if constraint != "" {
		out.WriteString("//go:build " + constraint + "\n\n")
	}
	out.WriteString(header)
	out.WriteString("\npackage " + pkg + "\n\nimport (\n")
	for _, imp := range imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		alias := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		if drop[alias] || !used[alias] {
			continue
		}
		if imp.Name != nil {
			out.WriteString("\t" + imp.Name.Name + " " + imp.Path.Value + "\n")
		} else {
			out.WriteString("\t" + imp.Path.Value + "\n")
		}
	}
	out.WriteString(")\n")
	out.Write(body.Bytes())
	formatted, err := format.Source(out.Bytes())
	if err != nil {
		return nil, fmt.Errorf("stdtable: formatting a split file: %w", err)
	}
	return formatted, nil
}

// usedAliases is every package alias a selector in decls names.
func usedAliases(decls []ast.Decl) map[string]bool {
	used := map[string]bool{}
	for _, d := range decls {
		ast.Inspect(d, func(n ast.Node) bool {
			if s, ok := n.(*ast.SelectorExpr); ok {
				if id, ok := s.X.(*ast.Ident); ok && id.Obj == nil {
					used[id.Name] = true
				}
			}
			return true
		})
	}
	return used
}

// stubFile is the answer of a build without fam for each of its adapters:
// the same method, answering fam's error.
func stubFile(fset *token.FileSet, header, pkg string, fam Family, moved []ast.Decl) ([]byte, error) {
	var out bytes.Buffer
	out.WriteString("//go:build " + fam.absentConstraint() + "\n\n")
	out.WriteString(header)
	// A register form's stub names hostadapt.Slots.
	hostadapt := ""
	for _, d := range moved {
		if isRegForm(d.(*ast.FuncDecl)) {
			hostadapt = "\t\"github.com/nomi-language/nomi/hostadapt\"\n"
		}
	}
	out.WriteString("\npackage " + pkg + "\n\nimport (\n\t\"errors\"\n\n" + hostadapt + "\t\"github.com/nomi-language/nomi/rt\"\n)\n\n")
	where := "a runner built with the " + fam.Tag + " tag"
	if len(fam.Absent) > 0 {
		where += " or for " + strings.Join(fam.Absent, " or ")
	}
	fmt.Fprintf(&out, "// %s answers every crossing of the %s family in\n// %s.\n"+
		"// A runner refuses a program that reaches one before it runs\n"+
		"// (vmrunner's familyRefusal), so a program meets this only through a\n"+
		"// crossing a runner makes for it.\n"+
		"var %s = errors.New(%q)\n\n"+
		"// %s reports whether this build answers %s's crossings with\n"+
		"// their adapters.\nconst %s = false\n",
		fam.errName(), fam.Key, where, fam.errName(), fam.message(),
		fam.linkedConst(), fam.Key, fam.linkedConst())
	for _, d := range moved {
		fd := d.(*ast.FuncDecl)
		var sig bytes.Buffer
		if err := printer.Fprint(&sig, fset, &ast.FuncDecl{Recv: fd.Recv, Name: fd.Name, Type: fd.Type}); err != nil {
			return nil, err
		}
		fail := "nil"
		if isRegForm(fd) {
			fail = "false"
		}
		fmt.Fprintf(&out, "\n%s {\n\treturn %s, %s\n}\n", sig.String(), fail, fam.errName())
	}
	formatted, err := format.Source(out.Bytes())
	if err != nil {
		return nil, fmt.Errorf("stdtable: formatting adapters_no%s_gen.go: %w", fam.Key, err)
	}
	return formatted, nil
}

// isRegForm reports whether fd is an adapter's register form
// (hostadapt.RegFunc), which answers a bool where the adapter answers an
// rt.Value. A row lists it rather than Bind's table.
func isRegForm(fd *ast.FuncDecl) bool {
	if fd.Type.Results == nil || len(fd.Type.Results.List) == 0 {
		return false
	}
	id, ok := fd.Type.Results.List[0].Type.(*ast.Ident)
	return ok && id.Name == "bool"
}
