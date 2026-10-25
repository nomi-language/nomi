package analysis

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// A keyword declaration (ast.KeywordDecl) expands into declarations the
// checker checks as it checks any: a `message` into the function it
// declares. Expansion is the compiler's general mechanism for a declaration
// checked against something outside Nomi, and its parts are general:
//
//   - Inputs. An expansion reads files under the module root (a
//     `message`, its catalogs) through the site's InputReader, and records
//     each one it read with its text. The files are inputs of the declaring
//     file: a program keeps them (CheckedProgram.Inputs), and an edit to one
//     rechecks the files that read it, through the paths an edit to the
//     file's own text takes (DocumentManager.RecheckEdits).
//   - Positions. The declarations an expansion writes keep the user's own
//     nodes where it copies them (a `message`'s parameters) and its name's
//     position, and put everything they add in the synthesized band, at a
//     slot that is a function of the declaration's place in its file, so
//     the same source expands to the same positions every time.
//   - The span map. ExpansionOf answers, for a function an expansion
//     wrote, the declaration it came from and every input span it was made
//     of (each catalog entry), which the language server navigates to, and
//     an error the checker reports in the added code is moved onto the
//     declaration (relocateExpansionErrors).
//   - Diagnostics in inputs. What is wrong in an input is a TypeError whose
//     File is the input's path, reported with the declaring file's: the
//     command line prints it with an excerpt of that file, and the language
//     server publishes it on that file's URI, open or not.

// InputReader answers the text of the file at an absolute path, and false
// when there is none.
type InputReader func(path string) (string, bool)

// DiskInputs reads inputs from disk.
func DiskInputs(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// ExpansionSite is where a file's keyword declarations expand: the module
// root its inputs are under, its path there, the module's manifest, and how
// to read an input.
type ExpansionSite struct {
	// Root is the module root, the directory holding nomi.toml; empty for a
	// file with no project, which has no inputs.
	Root string
	// ModulePath is the file's path under Root, slash-separated, without
	// `.nomi` ("shop/cart").
	ModulePath string
	// Manifest is the module's nomi.toml, nil for none.
	Manifest *Manifest
	// Read reads an input; nil reads the disk.
	Read InputReader
	// Macros runs the macros the file's declarations are written
	// with (macro_expand.go); nil runs none, and each such declaration
	// is an error.
	Macros MacroHost
}

func (s *ExpansionSite) read(path string) (string, bool) {
	if s.Read != nil {
		return s.Read(path)
	}
	return DiskInputs(path)
}

// SiteFor is the expansion site of the file at absPath under root, whose
// manifest is m: its module path is its path from root.
func SiteFor(root, absPath string, m *Manifest, read InputReader) *ExpansionSite {
	s := &ExpansionSite{Root: root, Manifest: m, Read: read}
	if root != "" && absPath != "" {
		if rel, err := filepath.Rel(root, absPath); err == nil && !strings.HasPrefix(rel, "..") {
			s.ModulePath = strings.TrimSuffix(filepath.ToSlash(rel), ".nomi")
		}
	}
	if s.ModulePath == "" {
		s.ModulePath = strings.TrimSuffix(filepath.Base(absPath), ".nomi")
	}
	return s
}

// ExpansionInput is one input an expansion read: its path, and its text
// when it exists.
type ExpansionInput struct {
	Path  string
	Text  string
	Found bool
}

// ExpansionSpan is one span of an input an expansion was made of: a
// catalog's entry for a message, labelled by its locale.
type ExpansionSpan struct {
	Path  string
	Label string
	Line  int
	Col   int
	// EndLine and EndCol end the span, one past its last byte.
	EndLine, EndCol int
}

// ExpansionInfo is what the span map of a function an expansion wrote says:
// the declaration it came from and the input spans it was made of.
type ExpansionInfo struct {
	Keyword string
	Decl    *ast.KeywordDecl
	Inputs  []ExpansionSpan
}

// expansionDetail is what each keyword keeps on the functions it writes.
type expansionDetail interface {
	info() *ExpansionInfo
	inputs() []ExpansionInput
}

// ExpansionOf is the span map of fn when an expansion wrote it, else nil.
func ExpansionOf(fn *ast.FuncDef) *ExpansionInfo {
	if fn == nil || fn.Expansion == nil {
		return nil
	}
	if d, ok := fn.Expansion.Detail().(expansionDetail); ok {
		return d.info()
	}
	return nil
}

// ExpansionOfNode is the span map of the declaration n when an expansion
// wrote it: a function, or the `once` a `noun` names; else nil.
func ExpansionOfNode(n ast.Node) *ExpansionInfo {
	switch d := n.(type) {
	case *ast.FuncDef:
		return ExpansionOf(d)
	case *ast.OnceBinding:
		if d == nil || d.Expansion == nil {
			return nil
		}
		if x, ok := d.Expansion.Detail().(expansionDetail); ok {
			return x.info()
		}
	}
	return nil
}

// ExpansionInputs are the inputs the expansions among nodes read, each
// once, in the order they were read.
func ExpansionInputs(nodes []ast.Node) []ExpansionInput {
	var out []ExpansionInput
	seen := map[string]bool{}
	for _, n := range nodes {
		var x *ast.Expansion
		switch d := n.(type) {
		case *ast.FuncDef:
			x = d.Expansion
		case *ast.OnceBinding:
			x = d.Expansion
		case *ast.KeywordDecl:
			x = d.Expansion
		}
		d, ok := x.Detail().(expansionDetail)
		if !ok {
			continue
		}
		for _, in := range d.inputs() {
			if !seen[in.Path] {
				seen[in.Path] = true
				out = append(out, in)
			}
		}
	}
	return out
}

// checkExpansionBody checks the body an expansion wrote, and moves an error
// it finds in the code the expansion added onto the declaration: that is a
// defect of the expansion, which the span map has no user span for.
func (c *checker) checkExpansionBody(fn *ast.FuncDef) {
	before := len(c.errors)
	c.checkFunc(fn)
	info := ExpansionOf(fn)
	if info == nil || info.Decl == nil {
		return
	}
	d := info.Decl
	for i := before; i < len(c.errors); i++ {
		e := &c.errors[i]
		if e.File != "" || !IsSynthesizedLine(e.Line) {
			continue
		}
		e.Line, e.Col, e.EndLine, e.EndCol = d.Line, d.Col, d.Line, d.KeywordEndCol()
		e.Message = fmt.Sprintf("the function `%s %s` expands to is ill-formed, a defect in Nomi: %s", d.Keyword, d.Name, e.Message)
	}
}

// ExpandDeclarations answers nodes with each keyword declaration expanded
// at site: a `message` becomes the function it declares and a `noun` the
// `once` it names, in its place, and the file gains the hidden imports its
// expansions name std/message by; a
// declaration written with an imported macro's keyword is followed by
// the declarations the macro expanded it to (expandMacro). A
// declaration of any other keyword stays, for the checker to refuse.
// Expanding again is a no-op: an expanded file holds no `message`
// declaration, and an expanded macro declaration carries its
// expansion. A nil site expands messages with no inputs, as a file with no
// project, and no macro.
//
// Each expansion is positioned at the band slot of its declaration's
// index among the expanded file's source nodes (sourceIndices), the index
// derive synthesis gives what it synthesizes for a node, so the two never
// share a slot.
func ExpandDeclarations(nodes []ast.Node, site *ExpansionSite) []ast.Node {
	hasMessage, hasMacro := false, false
	for _, n := range nodes {
		if kd, ok := n.(*ast.KeywordDecl); ok && kd.Expansion == nil {
			if isBuiltinKeyword(kd) {
				hasMessage = true
			} else {
				hasMacro = true
			}
		}
	}
	if !hasMessage && (!hasMacro || site == nil) {
		return nodes
	}
	if site == nil {
		site = &ExpansionSite{}
	}
	cats := &catalogSet{site: site, files: map[string]*messageCatalog{}}
	out := make([]ast.Node, 0, len(nodes)+1)
	if hasMessage && !hasMessageImport(nodes) {
		out = append(out, messageImports()...)
	}
	index := 0
	add := func(ns ...ast.Node) {
		for _, n := range ns {
			out = append(out, n)
			if imp, ok := n.(*ast.ImportStmt); !ok || !isPreludeInjectLine(imp.Line) {
				index++
			}
		}
	}
	for _, n := range nodes {
		kd, ok := n.(*ast.KeywordDecl)
		switch {
		case !ok || kd.Expansion != nil:
			add(n)
		case kd.Module == "" && kd.Keyword == keywordMessage:
			add(expandMessage(kd, site, cats, synthSlot(synthOriginDecl, index, synthKindEquatable)))
		case kd.Module == "" && kd.Keyword == keywordNoun:
			add(expandNoun(kd, site, cats, synthSlot(synthOriginDecl, index, synthKindEquatable)))
		default:
			add(expandMacro(kd, nodes, site, synthSlot(synthOriginDecl, index, synthKindExpansion))...)
		}
	}
	if hasMacro {
		out = checkExpansionCollisions(out)
	}
	return out
}
