package analysis

import (
	"reflect"
	"sync"

	"github.com/nomi-language/nomi/internal/ast"
)

// A file checked against interfaces reads the other files of its closure
// as their interfaces (interface.go): each declaration as another file's
// check reads it, with no attached test and a typed `once`'s value
// `todo`. A reader of the declaration itself (hover's examples and a
// `once`'s value) wants the declaration as its source has it. A program
// whose files were read so carries the way to their sources
// (declarationSources), and DeclarationSource answers a declaration from
// it.

// declarationSources answers, by module key, the nodes of a file under
// root the program read as its interface, as its source gives them, each
// file parsed once. Requests read it concurrently.
type declarationSources struct {
	root   string
	texts  map[string]string
	mu     sync.Mutex
	parsed map[string][]ast.Node
}

func (d *declarationSources) nodes(key string) []ast.Node {
	d.mu.Lock()
	defer d.mu.Unlock()
	if nodes, ok := d.parsed[key]; ok {
		return nodes
	}
	var nodes []ast.Node
	if text, ok := d.texts[key]; ok {
		nodes = parseFile(text).nodes
	}
	if d.parsed == nil {
		d.parsed = map[string][]ast.Node{}
	}
	d.parsed[key] = nodes
	return nodes
}

// DeclarationSource is the declaration n, of the file at path, as that
// file's source has it: n itself, unless fa's program read the file as
// its interface; then the source's declaration of the same kind at n's
// position, top-level or an impl's item, or n when the source has none.
func (fa *FileAnalysis) DeclarationSource(n ast.Node, path string) ast.Node {
	p := fa.Program()
	if p == nil || p.sources == nil || n == nil || path == "" {
		return n
	}
	return p.sources.declaration(n, relModuleKey(p.sources.root, path))
}

// ImplDeclarationSource is DeclarationSource for an impl's function, whose
// file the program's impl index names.
func (fa *FileAnalysis) ImplDeclarationSource(fd *ast.FuncDef) ast.Node {
	p := fa.Program()
	if p == nil || p.sources == nil || fd == nil || p.ImplIndex == nil {
		return fd
	}
	key, ok := p.ImplIndex.ImplFiles[fd]
	if !ok {
		return fd
	}
	return p.sources.declaration(fd, key)
}

// declaration is the source's declaration in the file with module key
// key of n's kind at n's position, or n.
func (d *declarationSources) declaration(n ast.Node, key string) ast.Node {
	at := topLevelPos(n)
	if at == (Pos{}) {
		return n
	}
	want := reflect.TypeOf(n)
	for _, top := range d.nodes(key) {
		if reflect.TypeOf(top) == want && topLevelPos(top) == at {
			return top
		}
		if impl, ok := top.(*ast.ImplBlock); ok {
			for _, item := range impl.Items {
				if reflect.TypeOf(item) == want && topLevelPos(item) == at {
					return item
				}
			}
		}
	}
	return n
}
