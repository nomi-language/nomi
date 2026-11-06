package analysis

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// A std::embed `Files` call whose path is a string literal, and whose tree
// is an `embed` declaration or a `Files.sub` of one with a literal
// directory, is checked against the files the declaration holds (spec §18
// *Embedded files*):
//
//	Files.read_text(site, "static/indx.html")
//
// is an error at the literal when site holds no such file. `read`,
// `read_text` name a file; `list`, `walk` and `sub` a directory; `metadata`
// either. A path built at run time, and `exists` and `paths`, are not
// checked. A literal that the run time refuses for its form (a leading
// `/`, or `..` out of the tree) is refused here with the reason the run
// time's `InvalidPath` gives.
//
// The tree is read from the declaration's value, the ast.EmbeddedFiles its
// expansion holds, which a file's interface keeps (interface.go keeps an
// expanded `once`'s value), so a check against interfaces sees the same
// files as a check from source.
//
// A pattern whose base did not exist when the program was compiled leaves
// its base an empty directory of the tree (its value's second argument),
// and a literal path at or under such a base is not checked: the
// files are not there to check it against, and the pattern's own warning
// says so. `nomi build` refuses the pattern.

// embedPathWant is what a checked call's path must name.
type embedPathWant int

const (
	embedWantFile embedPathWant = iota
	embedWantDir
	embedWantEither
)

// embedPathFuncs are the `Files` functions whose literal path is checked,
// with their path parameter's name.
var embedPathFuncs = map[string]struct {
	want  embedPathWant
	param string
}{
	"read":      {embedWantFile, "p"},
	"read_text": {embedWantFile, "p"},
	"metadata":  {embedWantEither, "p"},
	"list":      {embedWantDir, "dir"},
	"walk":      {embedWantDir, "root"},
	"sub":       {embedWantDir, "dir"},
}

// embedTree is the files of an `embed` declaration, or of a `Files.sub`
// of one, by their paths from the tree's root.
type embedTree struct {
	// name is how a message names the tree: `site`, or `static` of `site`.
	name  string
	files map[string]bool
	dirs  map[string]bool
	// missing are the tree's directories from patterns whose base did not
	// exist: a path at or under one is not checked.
	missing []string
}

func newEmbedTree(name string, paths, missing []string) *embedTree {
	t := &embedTree{name: name, files: map[string]bool{}, dirs: map[string]bool{".": true}, missing: missing}
	for _, p := range paths {
		t.files[p] = true
		for d := path.Dir(p); d != "."; d = path.Dir(d) {
			t.dirs[d] = true
		}
	}
	for _, m := range missing {
		for d := m; d != "."; d = path.Dir(d) {
			t.dirs[d] = true
		}
	}
	return t
}

// unchecked reports whether p is at or under a directory of t whose
// pattern's base did not exist. A directory above one is in t, as it is
// in the tree the program reads.
func (t *embedTree) unchecked(p string) bool {
	for _, m := range t.missing {
		if m == "." || p == m || strings.HasPrefix(p, m+"/") {
			return true
		}
	}
	return false
}

// sub is the tree under dir, a directory of t.
func (t *embedTree) sub(dir string) *embedTree {
	if dir == "." {
		return t
	}
	var paths, missing []string
	for p := range t.files {
		if rest, ok := strings.CutPrefix(p, dir+"/"); ok {
			paths = append(paths, rest)
		}
	}
	for _, m := range t.missing {
		if rest, ok := strings.CutPrefix(m, dir+"/"); ok {
			missing = append(missing, rest)
		} else if m == dir || strings.HasPrefix(dir, m+"/") {
			missing = append(missing, ".")
		}
	}
	return newEmbedTree(fmt.Sprintf("`%s` of %s", dir, t.name), paths, missing)
}

// embedFilesCall is the `Files` function a call's callee names, its
// files argument and its path argument, or false when the callee is not
// one of embedPathFuncs.
func (c *checker) embedFilesCall(fn ast.Node, args []ast.Node) (string, ast.Node, ast.Node, bool) {
	fa, ok := fn.(*ast.FieldAccess)
	if !ok || fa.Field == nil {
		return "", nil, nil, false
	}
	spec, ok := embedPathFuncs[fa.Field.Name]
	if !ok || len(args) != 2 || !c.isStdEmbedFilesFunc(fa) {
		return "", nil, nil, false
	}
	files, p := args[0], args[1]
	for _, a := range args {
		if na, ok := a.(*ast.NamedArg); ok {
			switch na.Name {
			case "files":
				files = na.Value
			case spec.param:
				p = na.Value
			default:
				return "", nil, nil, false
			}
		}
	}
	return fa.Field.Name, files, p, true
}

// isStdEmbedFilesFunc reports whether fa names a function of std::embed's
// `Files`.
func (c *checker) isStdEmbedFilesFunc(fa *ast.FieldAccess) bool {
	sym := resolvedSymbol(c.fa.References[Pos{Line: fa.Field.Line, Col: fa.Field.Col}])
	if sym == nil || sym.OwningType != "Files" {
		return false
	}
	fd, ok := sym.Node.(*ast.FuncDef)
	if !ok || len(fd.Params) == 0 {
		return false
	}
	ft, ok := sym.Type.(*FuncType)
	if !ok || len(ft.Params) == 0 {
		return false
	}
	st, ok := ft.Params[0].(*StructType)
	return ok && st.Name == "Files" && st.Origin == "std/embed"
}

// checkEmbeddedPathCall checks the literal path of a call to a `Files`
// function, fn applied to args (a pipe's left operand first), against the
// tree its files argument names.
func (c *checker) checkEmbeddedPathCall(fn ast.Node, args []ast.Node) {
	name, filesArg, pathArg, ok := c.embedFilesCall(fn, args)
	if !ok {
		return
	}
	lit, ok := ungroupExpr(pathArg).(*ast.StringLit)
	if !ok {
		return
	}
	tree := c.embedTreeOf(filesArg)
	if tree == nil {
		return
	}
	if e, bad := embedPathError(lit, tree, embedPathFuncs[name].want); bad {
		c.report(e)
	}
}

// embedPathError is the error for lit, a path a call wants to be of the
// kind want in tree, or false when it is.
func embedPathError(lit *ast.StringLit, tree *embedTree, want embedPathWant) (TypeError, bool) {
	p := path.Clean(lit.Value)
	if msg := embedPathFormProblem(p); msg != "" {
		return errAt(lit, fmt.Sprintf("`%s` is not an embedded path: %s", lit.Value, msg)), true
	}
	if tree.unchecked(p) {
		return TypeError{}, false
	}
	isFile, isDir := tree.files[p], tree.dirs[p]
	var candidates []string
	switch want {
	case embedWantFile:
		if isFile {
			return TypeError{}, false
		}
		if isDir {
			return errAt(lit, fmt.Sprintf("`%s` is a directory in %s, not a file", lit.Value, tree.name)), true
		}
		candidates = keysOf(tree.files)
	case embedWantDir:
		if isDir {
			return TypeError{}, false
		}
		if isFile {
			return errAt(lit, fmt.Sprintf("`%s` is a file in %s, not a directory", lit.Value, tree.name)), true
		}
		candidates = keysOf(tree.dirs)
	default:
		if isFile || isDir {
			return TypeError{}, false
		}
		candidates = append(keysOf(tree.files), keysOf(tree.dirs)...)
	}
	msg := fmt.Sprintf("`%s` is not embedded in %s", lit.Value, tree.name)
	if want == embedWantDir {
		msg = fmt.Sprintf("`%s` is not an embedded directory of %s", lit.Value, tree.name)
	}
	e := errAt(lit, msg)
	if best := closestPath(p, candidates); best != "" {
		e = e.WithHint(fmt.Sprintf("did you mean `%s`?", best))
	}
	return e, true
}

// embedPathFormProblem is why the run time refuses the cleaned path p
// for its form, as std::embed's `key` says it, or "".
func embedPathFormProblem(p string) string {
	switch {
	case strings.HasPrefix(p, "/"):
		return "an embedded path is relative to the root of the embedded tree"
	case p == ".." || strings.HasPrefix(p, "../"):
		return "the path leaves the embedded tree"
	}
	return ""
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// closestPath is the one candidate close to p by closestName's rule (an
// edit distance of at most a third of p's length), or "".
func closestPath(p string, candidates []string) string {
	limit := max(1, len([]rune(p))/3)
	best, bestDist, tied := "", limit+1, false
	for _, cand := range candidates {
		if cand == "." || cand == p {
			continue
		}
		switch d := editDistance(p, cand); {
		case d < bestDist:
			best, bestDist, tied = cand, d, false
		case d == bestDist:
			tied = true
		}
	}
	if tied {
		return ""
	}
	return best
}

// embedTreeOf is the tree e names: an `embed` declaration, read by its
// name (`site`, `web.site`), or `Files.sub` of a tree with a literal
// directory, unwrapped with `!` or `?`; nil for anything else.
func (c *checker) embedTreeOf(e ast.Node) *embedTree {
	switch e := ungroupExpr(e).(type) {
	case *ast.Ident:
		return embedDeclTree(c.fa.References[Pos{Line: e.Line, Col: e.Col}])
	case *ast.FieldAccess:
		if e.Field == nil {
			return nil
		}
		return embedDeclTree(c.fa.References[Pos{Line: e.Field.Line, Col: e.Field.Col}])
	case *ast.Unwrap:
		return c.embedSubTree(e.Expr)
	case *ast.TryOp:
		if e.Expr == nil {
			return nil
		}
		return c.embedSubTree(e.Expr)
	case *ast.Binary:
		// `site |> Files.sub("static")!`: the stage is the Unwrap.
		if e.Op != "|>" {
			return nil
		}
		switch r := e.Right.(type) {
		case *ast.Unwrap:
			if call, ok := r.Expr.(*ast.Call); ok {
				return c.embedSubOf(call.Func, append([]ast.Node{e.Left}, call.Args...))
			}
		}
	}
	return nil
}

// embedSubTree is the tree a `Files.sub` call e answers, or nil.
func (c *checker) embedSubTree(e ast.Node) *embedTree {
	call, ok := ungroupExpr(e).(*ast.Call)
	if !ok {
		return nil
	}
	return c.embedSubOf(call.Func, call.Args)
}

func (c *checker) embedSubOf(fn ast.Node, args []ast.Node) *embedTree {
	name, filesArg, pathArg, ok := c.embedFilesCall(fn, args)
	if !ok || name != "sub" {
		return nil
	}
	lit, ok := ungroupExpr(pathArg).(*ast.StringLit)
	if !ok {
		return nil
	}
	base := c.embedTreeOf(filesArg)
	if base == nil {
		return nil
	}
	dir := path.Clean(lit.Value)
	if !base.dirs[dir] {
		return nil
	}
	return base.sub(dir)
}

// embedDeclTree is the tree of the `embed` declaration sym names, or nil.
func embedDeclTree(sym *Symbol) *embedTree {
	sym = resolvedSymbol(sym)
	if sym == nil {
		return nil
	}
	o, ok := sym.Node.(*ast.OnceBinding)
	if !ok || o.Expansion == nil || o.Expansion.Keyword != keywordEmbed {
		return nil
	}
	call, ok := o.Value.(*ast.Call)
	if !ok || len(call.Args) != 2 {
		return nil
	}
	ef, ok := call.Args[0].(*ast.EmbeddedFiles)
	if !ok {
		return nil
	}
	empty, ok := call.Args[1].(*ast.ListLit)
	if !ok {
		return nil
	}
	paths := make([]string, len(ef.Files))
	for i, f := range ef.Files {
		paths[i] = f.Path
	}
	var missing []string
	for _, item := range empty.Items {
		if lit, ok := item.(*ast.StringLit); ok {
			missing = append(missing, lit.Value)
		}
	}
	return newEmbedTree("`"+o.Name+"`", paths, missing)
}
