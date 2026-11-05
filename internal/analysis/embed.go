package analysis

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nomi-language/nomi/internal/ast"
)

// An `embed` declaration (ast.EmbedDecl) names files by pattern, and the
// front end reads them when it prepares the declaring file:
//
//	embed site `static templates/*.html`
//
// expands into the `once` it names,
//
//	once site: embed$.Files = embed$.from_entries(<EmbeddedFiles>)
//
// whose value is an ast.EmbeddedFiles holding every file the patterns
// matched, with where its contents are and their size and SHA-256, and
// whose type and call are in the synthesized band, as a `noun`'s are. The
// front end hashes each file a buffer at a time and keeps none of it. The
// IR builder lowers the EmbeddedFiles to a list of each file's path, handle
// (ir.ConstEmbedded) and size; the program reads a file's contents when it
// reads the file, from the image a build wrote them into, or from the
// source a run checks against the sum.
//
// The patterns follow Go's `//go:embed`, except for their base: each is
// a path.Match pattern naming files from the module root, as an import
// path names a file, whatever directory the declaring file is in. It is
// slash-separated, with no
// `.` or `..` name, no empty name and no leading or trailing `/`, so it
// never reaches outside the module. A match that is a directory embeds
// every file under it, except names that start with `.` or `_`, unless
// the pattern starts with `all:`; a file a pattern names itself is
// embedded whatever its name. A directory holding its own nomi.toml is
// another module: a walk skips it, and naming it is an error. A symbolic
// link or other irregular file is an error. A pattern that matches
// nothing, or only directories with nothing to embed, is an error at the
// pattern. Nomi differs from Go in one thing: `**` is an error, since a
// directory's name already embeds everything under it.

const keywordEmbed = "embed"

// embedModuleAlias is the alias of the hidden import an `embed` reaches
// std/embed by (embedImport).
const embedModuleAlias = "embed$"

// EmbedFS is what an `embed` reads: the files under a module root, by
// slash-separated paths from it (`.` is the root). Open is called only
// when the site hashes contents, and Source names where the program reads
// a file's contents when it runs (ast.EmbeddedFile).
type EmbedFS interface {
	ReadDir(dir string) ([]fs.DirEntry, error)
	Open(name string) (io.ReadCloser, error)
	Source(name string) string
}

// DiskEmbedFS reads the files under Root on disk. A directory's entries
// are not followed through symbolic links, so a link is seen as one.
type DiskEmbedFS struct{ Root string }

func (d DiskEmbedFS) ReadDir(dir string) ([]fs.DirEntry, error) {
	return os.ReadDir(filepath.Join(d.Root, filepath.FromSlash(dir)))
}

func (d DiskEmbedFS) Open(name string) (io.ReadCloser, error) {
	return os.Open(d.Source(name))
}

// Source is the file's absolute path.
func (d DiskEmbedFS) Source(name string) string {
	return filepath.Join(d.Root, filepath.FromSlash(name))
}

// MapEmbedFS is an in-memory module's files, by their slash-separated
// paths from its root, as a tour block's data files are held. Their
// directories are made from the paths.
type MapEmbedFS map[string]string

func (m MapEmbedFS) ReadDir(dir string) ([]fs.DirEntry, error) {
	dir = path.Clean(dir)
	prefix := dir + "/"
	if dir == "." {
		prefix = ""
	}
	seen := map[string]fs.DirEntry{}
	for name, text := range m {
		name = path.Clean(name)
		rest, ok := strings.CutPrefix(name, prefix)
		if !ok || rest == "" {
			continue
		}
		if first, _, deeper := strings.Cut(rest, "/"); deeper {
			seen[first] = memEntry{name: first, dir: true}
		} else {
			seen[first] = memEntry{name: first, size: int64(len(text))}
		}
	}
	if len(seen) == 0 && dir != "." {
		return nil, fs.ErrNotExist
	}
	out := make([]fs.DirEntry, 0, len(seen))
	for _, e := range seen {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

func (m MapEmbedFS) Open(name string) (io.ReadCloser, error) {
	if text, ok := m[path.Clean(name)]; ok {
		return io.NopCloser(strings.NewReader(text)), nil
	}
	return nil, fs.ErrNotExist
}

// Source is the file's key in the map, which the program reads its
// contents by (vmhost's in-memory programs).
func (m MapEmbedFS) Source(name string) string { return path.Clean(name) }

// memEntry is one of MapEmbedFS's entries, and its own FileInfo.
type memEntry struct {
	name string
	dir  bool
	size int64
}

func (e memEntry) Name() string { return e.name }
func (e memEntry) IsDir() bool  { return e.dir }
func (e memEntry) Type() fs.FileMode {
	if e.dir {
		return fs.ModeDir
	}
	return 0
}
func (e memEntry) Info() (fs.FileInfo, error) { return e, nil }
func (e memEntry) Size() int64                { return e.size }
func (e memEntry) Mode() fs.FileMode {
	if e.dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}
func (e memEntry) ModTime() time.Time { return time.Time{} }
func (e memEntry) Sys() any           { return nil }

// EmbedInfo is what an `embed` matched, for the language server: the
// declaration, each embedded file, and each pattern's matches.
type EmbedInfo struct {
	Decl *ast.EmbedDecl
	// Root is the module root the patterns are read from: an absolute
	// path on disk, or "" for an in-memory module.
	Root  string
	Files []EmbedFileInfo
	// Matches holds, for each pattern in order, the paths it matched from
	// Root, files and directories, sorted.
	Matches [][]string
}

// EmbedFileInfo is one embedded file: its path from the module root, and
// its size.
type EmbedFileInfo struct {
	Path string
	Size int64
}

// TotalSize is the size of every embedded file together.
func (in *EmbedInfo) TotalSize() int64 {
	var n int64
	for _, f := range in.Files {
		n += f.Size
	}
	return n
}

// Bases are the directories and files, by their paths from Root, whose
// contents can change what the patterns match: for each pattern, the
// path its leading names spell before the first name that holds a
// wildcard, or the whole path when none does. "." is the root.
func (in *EmbedInfo) Bases() []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range in.Decl.Patterns {
		text, _ := strings.CutPrefix(p.Text, "all:")
		names := strings.Split(text, "/")
		var fixed []string
		for _, name := range names {
			if strings.ContainsAny(name, `*?[\`) {
				break
			}
			fixed = append(fixed, name)
		}
		base := path.Join(append([]string{"."}, fixed...)...)
		if !seen[base] {
			seen[base] = true
			out = append(out, base)
		}
	}
	sort.Strings(out)
	return out
}

// embedExpansion is what an `embed` keeps on the `once` it expands into.
type embedExpansion struct {
	info *EmbedInfo
	errs []TypeError
}

// embedExpansionOf is o's embed expansion, or nil.
func embedExpansionOf(o *ast.OnceBinding) *embedExpansion {
	if o == nil || o.Expansion == nil {
		return nil
	}
	e, _ := o.Expansion.Detail().(*embedExpansion)
	return e
}

// EmbedOf is what the `embed` that o expands matched, or nil when o is
// no `embed`'s.
func EmbedOf(o *ast.OnceBinding) *EmbedInfo {
	if e := embedExpansionOf(o); e != nil {
		return e.info
	}
	return nil
}

// embedImport is `import std::embed as embed$`, on the hidden imports'
// line, after html's columns.
func embedImport() *ast.ImportStmt {
	l := hiddenImportLine
	return &ast.ImportStmt{
		ModulePath:   []ast.Node{&ast.Ident{Name: "std", Line: l, Col: 18}, &ast.Ident{Name: "embed", Line: l, Col: 19}},
		FileSegments: 2,
		ModuleAlias:  &ast.Ident{Name: embedModuleAlias, Line: l, Col: 20},
		Line:         l,
		Col:          17,
	}
}

// hasEmbedImport reports whether nodes hold embedImport's import.
func hasEmbedImport(nodes []ast.Node) bool {
	for _, n := range nodes {
		if imp, ok := n.(*ast.ImportStmt); ok && imp.Line == hiddenImportLine && imp.ModuleAlias != nil &&
			ast.ImportNodeName(imp.ModuleAlias) == embedModuleAlias {
			return true
		}
	}
	return false
}

// expandEmbed reads the files d's patterns match at site and writes the
// `once` d names, with its added nodes at slot.
func expandEmbed(d *ast.EmbedDecl, site *ExpansionSite, slot int) *ast.OnceBinding {
	r := embedResolver{decl: d, site: site}
	files := r.resolve()
	value := &ast.EmbeddedFiles{Files: files}
	call := &ast.Call{
		Func: fieldAccess(identExpr(embedModuleAlias, 0, 0), "from_entries", 0, 0),
		Args: []ast.Node{value},
	}
	typ := &ast.QualifiedType{Module: embedModuleAlias, Member: &ast.SimpleType{Name: "Files"}}
	carrier := &ast.FuncDef{ReturnTypeExpr: typ, Body: &ast.Block{Stmts: []ast.Node{exprStmt(call, 0, 0)}}}
	uniquifyPositions(carrier, slot)
	once := &ast.OnceBinding{
		Name:           d.Name,
		Public:         d.Public,
		TypeAnnotation: typ,
		Value:          call,
		Doc:            d.Doc,
		Line:           d.Line,
		Col:            d.NameCol,
	}
	once.Span = d.Span
	once.TriviaCarrier = d.TriviaCarrier
	info := &EmbedInfo{Decl: d, Root: r.displayRoot(), Matches: r.matches}
	for _, f := range files {
		info.Files = append(info.Files, EmbedFileInfo{Path: f.Path, Size: f.Size})
	}
	once.Expansion = ast.NewExpansion(keywordEmbed, &embedExpansion{info: info, errs: r.errs})
	return once
}

// embedResolver finds the files of one `embed`, by their paths from the
// site's module root.
type embedResolver struct {
	decl    *ast.EmbedDecl
	site    *ExpansionSite
	errs    []TypeError
	matches [][]string
}

// displayRoot is the module root on disk, or "" for an in-memory module.
func (r *embedResolver) displayRoot() string {
	if r.site == nil {
		return ""
	}
	if d, onDisk := r.site.Embed.(DiskEmbedFS); onDisk {
		return d.Root
	}
	return ""
}

// fail records msg as an error at pattern p.
func (r *embedResolver) fail(p ast.EmbedPattern, msg string, hints ...string) {
	e := TypeError{Line: p.Line, Col: p.Col, EndLine: p.Line, EndCol: p.Col + p.Width, Message: msg}
	for _, h := range hints {
		e = e.WithHint(h)
	}
	r.errs = append(r.errs, e)
}

// resolve answers every file d's patterns match, in path order, recording
// an error at each pattern that is malformed or matches nothing.
func (r *embedResolver) resolve() []ast.EmbeddedFile {
	r.matches = make([][]string, len(r.decl.Patterns))
	if r.site == nil || r.site.Embed == nil {
		d := r.decl
		r.errs = append(r.errs, TypeError{Line: d.Line, Col: d.Col, EndLine: d.Line, EndCol: d.KeywordEndCol(),
			Message: fmt.Sprintf("`embed %s` has no directory to read files from: this file is not on disk or in a module", d.Name)})
		return nil
	}
	found := map[string]ast.EmbeddedFile{}
	for i, p := range r.decl.Patterns {
		for _, f := range r.pattern(i, p) {
			found[f.Path] = f
		}
	}
	paths := make([]string, 0, len(found))
	for p := range found {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	out := make([]ast.EmbeddedFile, 0, len(paths))
	for _, p := range paths {
		f := found[p]
		f.Source = r.site.Embed.Source(p)
		if !r.site.SkipEmbedContents {
			size, sum, err := hashEmbedded(r.site.Embed, p)
			if err != nil {
				r.errs = append(r.errs, TypeError{Line: r.decl.Line, Col: r.decl.Col, EndLine: r.decl.Line, EndCol: r.decl.KeywordEndCol(),
					Message: fmt.Sprintf("`embed %s` cannot read `%s`: %v", r.decl.Name, p, err)})
				continue
			}
			f.Size, f.Sum = size, sum
		}
		out = append(out, f)
	}
	return out
}

// pattern answers the files pattern i, p, embeds.
func (r *embedResolver) pattern(i int, p ast.EmbedPattern) []ast.EmbeddedFile {
	text, all := strings.CutPrefix(p.Text, "all:")
	if msg, hint := embedPatternProblem(text); msg != "" {
		if hint != "" {
			r.fail(p, msg, hint)
		} else {
			r.fail(p, msg)
		}
		return nil
	}
	names := strings.Split(text, "/")
	current := []string{"."}
	var anotherModule string
	for depth, name := range names {
		var next []string
		for _, at := range current {
			entries, err := r.site.Embed.ReadDir(at)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if ok, _ := path.Match(name, e.Name()); !ok {
					continue
				}
				rel := path.Join(at, e.Name())
				if depth < len(names)-1 {
					if !e.IsDir() {
						continue
					}
					if r.otherModule(rel) {
						anotherModule = rel
						continue
					}
				}
				next = append(next, rel)
				if depth == len(names)-1 {
					r.matches[i] = append(r.matches[i], rel)
				}
			}
		}
		current = next
	}
	if len(current) == 0 {
		if anotherModule != "" {
			r.fail(p, fmt.Sprintf("cannot embed files under `%s`: it is another module, with its own nomi.toml", anotherModule))
			return nil
		}
		r.fail(p, fmt.Sprintf("the `embed` pattern `%s` matches no file", p.Text), "a pattern names files from the module root, as an import path does")
		return nil
	}
	sort.Strings(r.matches[i])
	var out []ast.EmbeddedFile
	for _, rel := range current {
		files, ok := r.match(p, rel, all)
		if !ok {
			return nil
		}
		out = append(out, files...)
	}
	return out
}

// match answers the files rel, a pattern's match, embeds: itself when it
// is a file, and the files under it when it is a directory.
func (r *embedResolver) match(p ast.EmbedPattern, rel string, all bool) ([]ast.EmbeddedFile, bool) {
	entry, ok := r.entry(rel)
	if !ok {
		r.fail(p, fmt.Sprintf("cannot embed `%s`: it cannot be read", rel))
		return nil, false
	}
	switch {
	case entry.Type()&fs.ModeSymlink != 0:
		r.fail(p, fmt.Sprintf("cannot embed `%s`: it is a symbolic link", rel), "an `embed` holds regular files only; name the file the link points to")
		return nil, false
	case entry.IsDir():
		if r.otherModule(rel) {
			r.fail(p, fmt.Sprintf("cannot embed `%s`: it is another module, with its own nomi.toml", rel))
			return nil, false
		}
		var out []ast.EmbeddedFile
		skipped := false
		if !r.walk(p, rel, all, &out, &skipped) {
			return nil, false
		}
		if len(out) == 0 {
			if skipped && !all {
				r.fail(p, fmt.Sprintf("cannot embed `%s`: it holds no file to embed", rel),
					fmt.Sprintf("files whose names start with `.` or `_` are left out of a directory; write `all:%s` to embed them too", rel))
			} else {
				r.fail(p, fmt.Sprintf("cannot embed `%s`: it holds no file to embed", rel))
			}
			return nil, false
		}
		return out, true
	case !entry.Type().IsRegular():
		r.fail(p, fmt.Sprintf("cannot embed `%s`: it is not a regular file", rel))
		return nil, false
	}
	return []ast.EmbeddedFile{{Path: rel, Size: entrySize(entry)}}, true
}

// walk adds the files under dir to out, leaving out hidden names unless
// all, and the directories of other modules.
func (r *embedResolver) walk(p ast.EmbedPattern, dir string, all bool, out *[]ast.EmbeddedFile, skipped *bool) bool {
	entries, err := r.site.Embed.ReadDir(dir)
	if err != nil {
		r.fail(p, fmt.Sprintf("cannot embed `%s`: %v", dir, err))
		return false
	}
	for _, e := range entries {
		rel := path.Join(dir, e.Name())
		if !all && (strings.HasPrefix(e.Name(), ".") || strings.HasPrefix(e.Name(), "_")) {
			*skipped = true
			continue
		}
		switch {
		case e.Type()&fs.ModeSymlink != 0:
			r.fail(p, fmt.Sprintf("cannot embed `%s`: it is a symbolic link", rel), "an `embed` holds regular files only; name the file the link points to")
			return false
		case e.IsDir():
			if r.otherModule(rel) {
				continue
			}
			if !r.walk(p, rel, all, out, skipped) {
				return false
			}
		case !e.Type().IsRegular():
			r.fail(p, fmt.Sprintf("cannot embed `%s`: it is not a regular file", rel))
			return false
		default:
			*out = append(*out, ast.EmbeddedFile{Path: rel, Size: entrySize(e)})
		}
	}
	return true
}

// entry is the directory entry of rel, from its parent's listing.
func (r *embedResolver) entry(rel string) (fs.DirEntry, bool) {
	entries, err := r.site.Embed.ReadDir(path.Dir(rel))
	if err != nil {
		return nil, false
	}
	for _, e := range entries {
		if e.Name() == path.Base(rel) {
			return e, true
		}
	}
	return nil, false
}

// otherModule reports whether the directory rel holds a nomi.toml of its
// own, which makes it another module's.
func (r *embedResolver) otherModule(rel string) bool {
	entries, err := r.site.Embed.ReadDir(rel)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Name() == "nomi.toml" && !e.IsDir() {
			return true
		}
	}
	return false
}

// hashEmbedded reads the file name in fsys through SHA-256 a buffer at a
// time, and answers its size and sum: the stamp the program checks the
// contents against when it reads them. None of it is kept.
func hashEmbedded(fsys EmbedFS, name string) (int64, string, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

func entrySize(e fs.DirEntry) int64 {
	if info, err := e.Info(); err == nil {
		return info.Size()
	}
	return 0
}

// embedPatternProblem is what is wrong with a pattern's text, and a hint,
// or "" when it is well formed.
func embedPatternProblem(text string) (msg, hint string) {
	switch {
	case text == "":
		return "an `embed` pattern after `all:` is empty", ""
	case strings.HasPrefix(text, "/"):
		return fmt.Sprintf("the `embed` pattern `%s` starts with `/`", text), "a pattern names files from the module root; remove the `/`"
	case strings.HasSuffix(text, "/"):
		return fmt.Sprintf("the `embed` pattern `%s` ends with `/`", text), "a directory's name embeds every file under it; remove the `/`"
	case strings.Contains(text, `\`):
		return fmt.Sprintf("the `embed` pattern `%s` holds `\\`", text), "names in a pattern are separated by `/` on every system"
	}
	for _, name := range strings.Split(text, "/") {
		switch {
		case name == "":
			return fmt.Sprintf("the `embed` pattern `%s` has an empty name between two `/`", text), ""
		case name == ".":
			return fmt.Sprintf("the `embed` pattern `%s` names `.`", text), "a pattern names files from the module root; leave out `./`"
		case name == "..":
			return fmt.Sprintf("the `embed` pattern `%s` names `..`", text), "an `embed` reads only files in its own module, named from the module root"
		case strings.Contains(name, "**"):
			return fmt.Sprintf("the `embed` pattern `%s` holds `**`", text), "a directory's name embeds every file under it: write the directory"
		}
	}
	if _, err := path.Match(text, ""); err != nil {
		return fmt.Sprintf("the `embed` pattern `%s` is not a valid pattern: a `[` is not closed, or a `\\` ends it", text), ""
	}
	return "", ""
}

// checkEmbed reports what is wrong with the `embed` o expands, and
// reports whether there was anything, in which case its value is not
// checked.
func (c *checker) checkEmbed(o *ast.OnceBinding, e *embedExpansion) bool {
	c.errors = append(c.errors, e.errs...)
	return len(e.errs) > 0
}
