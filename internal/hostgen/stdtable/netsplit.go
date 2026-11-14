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
)

// NetworkPackage is the adapter package whose functions need the network:
// net/http, crypto/tls and net's sockets. Every adapter that calls into it
// is built only into a runner with the network (NetworkTag unset), so a
// program that never reaches one runs on a runner that links none of them.
const NetworkPackage = "github.com/nomi-language/nomi/internal/stdhttp"

// NetworkTag is the build tag that leaves the network out: a runner built
// with it answers each network crossing with an error instead of linking
// NetworkPackage. Untagged builds, `nomi` itself among them, have it.
const NetworkTag = "nomi_nonet"

// Files is the generated adapter package's source, by file name.
type Files map[string][]byte

// GenerateFiles is Generate's source split three ways: adapters_gen.go,
// every adapter that does not call NetworkPackage plus NetworkNames;
// adapters_net_gen.go, the ones that do, built without NetworkTag; and
// adapters_nonet_gen.go, an error-answering stub for each of those, built
// with it. Bind names every adapter in both builds, so the two runners
// answer the same crossings and differ only in what the network ones do.
func GenerateFiles() (Files, error) {
	src, err := Generate()
	if err != nil {
		return nil, err
	}
	return splitNetwork(src)
}

func splitNetwork(src []byte) (Files, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "adapters_gen.go", src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("stdtable: parsing the generated source: %w", err)
	}
	netAlias := ""
	for _, imp := range f.Imports {
		if path, _ := strconv.Unquote(imp.Path.Value); path == NetworkPackage {
			netAlias = NetworkPackage[strings.LastIndex(NetworkPackage, "/")+1:]
			if imp.Name != nil {
				netAlias = imp.Name.Name
			}
		}
	}
	if netAlias == "" {
		return nil, fmt.Errorf("stdtable: the generated source does not import %s", NetworkPackage)
	}

	var keep, moved []ast.Decl
	movedFns := map[string]bool{}
	for _, d := range f.Decls {
		if !callsPackage(d, netAlias) {
			keep = append(keep, d)
			continue
		}
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Recv == nil {
			return nil, fmt.Errorf("stdtable: a declaration other than an adapter method calls %s at %s", netAlias, fset.Position(d.Pos()))
		}
		moved = append(moved, d)
		if !isPartsForm(fd) {
			movedFns[fd.Name.Name] = true
		}
	}
	names := networkNames(f, movedFns)
	if len(names) != len(movedFns) {
		return nil, fmt.Errorf("stdtable: %d network adapters, but Bind names %d of them", len(movedFns), len(names))
	}

	header := leadingComment(src)
	main, err := renderFile(fset, f.Comments, header, "", f.Name.Name, f.Imports, keep, netAlias)
	if err != nil {
		return nil, err
	}
	main = append(main, networkNamesFunc(names)...)
	if main, err = format.Source(main); err != nil {
		return nil, fmt.Errorf("stdtable: formatting adapters_gen.go: %w", err)
	}
	net, err := renderFile(fset, f.Comments, header, "!"+NetworkTag, f.Name.Name, f.Imports, moved, "")
	if err != nil {
		return nil, err
	}
	net = append(net, []byte("\n// HasNetwork reports whether this build answers the network crossings\n"+
		"// (NetworkNames) with their adapters: false in a build with the "+NetworkTag+" tag.\n"+
		"const HasNetwork = true\n")...)
	if net, err = format.Source(net); err != nil {
		return nil, fmt.Errorf("stdtable: formatting adapters_net_gen.go: %w", err)
	}
	nonet, err := stubFile(fset, header, f.Name.Name, moved)
	if err != nil {
		return nil, err
	}
	return Files{
		"adapters_gen.go":       main,
		"adapters_net_gen.go":   net,
		"adapters_nonet_gen.go": nonet,
	}, nil
}

// callsPackage reports whether d names anything of the package imported as
// alias.
func callsPackage(d ast.Decl, alias string) bool {
	found := false
	ast.Inspect(d, func(n ast.Node) bool {
		if s, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := s.X.(*ast.Ident); ok && id.Name == alias {
				found = true
			}
		}
		return !found
	})
	return found
}

// networkNames is the crossing names Bind maps to a method in fns, sorted.
func networkNames(f *ast.File, fns map[string]bool) []string {
	var out []string
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "Bind" || fd.Recv != nil {
			continue
		}
		ast.Inspect(fd, func(n ast.Node) bool {
			kv, ok := n.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			sel, ok := kv.Value.(*ast.SelectorExpr)
			lit, isLit := kv.Key.(*ast.BasicLit)
			if ok && isLit && fns[sel.Sel.Name] {
				if name, err := strconv.Unquote(lit.Value); err == nil {
					out = append(out, name)
				}
			}
			return true
		})
	}
	sort.Strings(out)
	return out
}

func networkNamesFunc(names []string) []byte {
	var b bytes.Buffer
	b.WriteString("\n// NetworkNames is every crossing whose adapter calls " + NetworkPackage + ",\n" +
		"// sorted: what a runner built with the " + NetworkTag + " tag answers with an error.\n" +
		"// `nomi build` picks the runner without the network for a program that\n" +
		"// reaches none of them.\nfunc NetworkNames() []string {\n\treturn []string{\n")
	for _, n := range names {
		fmt.Fprintf(&b, "\t\t%q,\n", n)
	}
	b.WriteString("\t}\n}\n")
	return b.Bytes()
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
// drop names an import alias never kept.
func renderFile(fset *token.FileSet, comments []*ast.CommentGroup, header, constraint, pkg string, imports []*ast.ImportSpec, decls []ast.Decl, drop string) ([]byte, error) {
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
		if alias == drop || !used[alias] {
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

// stubFile is the NetworkTag build's answer for each moved adapter: the
// same method, answering errNoNetwork.
func stubFile(fset *token.FileSet, header, pkg string, moved []ast.Decl) ([]byte, error) {
	var out bytes.Buffer
	out.WriteString("//go:build " + NetworkTag + "\n\n")
	out.WriteString(header)
	out.WriteString("\npackage " + pkg + "\n\nimport (\n\t\"errors\"\n\n\t\"github.com/nomi-language/nomi/rt\"\n)\n\n")
	out.WriteString("// errNoNetwork is every network crossing's answer in a runner built\n" +
		"// with the " + NetworkTag + " tag. `nomi build` gives a program that reaches one\n" +
		"// the runner with the network, so a program meets this only on a\n" +
		"// runner chosen by hand.\n" +
		"var errNoNetwork = errors.New(\"this nomi-vm was built without the network; build the program with `nomi build`, which picks the runner that has it\")\n\n" +
		"// HasNetwork reports whether this build answers the network crossings\n" +
		"// (NetworkNames) with their adapters: false in a build with the " + NetworkTag + " tag.\n" +
		"const HasNetwork = false\n")
	for _, d := range moved {
		fd := d.(*ast.FuncDecl)
		var sig bytes.Buffer
		if err := printer.Fprint(&sig, fset, &ast.FuncDecl{Recv: fd.Recv, Name: fd.Name, Type: fd.Type}); err != nil {
			return nil, err
		}
		fail := "nil"
		if isPartsForm(fd) {
			fail = "nil, false"
		}
		fmt.Fprintf(&out, "\n%s {\n\treturn %s, errNoNetwork\n}\n", sig.String(), fail)
	}
	formatted, err := format.Source(out.Bytes())
	if err != nil {
		return nil, fmt.Errorf("stdtable: formatting adapters_nonet_gen.go: %w", err)
	}
	return formatted, nil
}

// isPartsForm reports whether fd is an adapter's parts form
// (hostadapt.PartsFunc), which answers three results where the adapter
// answers two. Bind hands it to Env.AddParts rather than naming it in its
// table.
func isPartsForm(fd *ast.FuncDecl) bool {
	return fd.Type.Results != nil && fd.Type.Results.NumFields() == 3
}
