package vmhost

import (
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/compilerhosts"
	"github.com/nomi-language/nomi/internal/format"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/internal/webtypes"
	"github.com/nomi-language/nomi/std"
)

// BuildImage is what `nomi build` appends to a runner: the program's linked
// IR (its own units, the stdlib modules and generic instances it links),
// pruned to the functions the program can reach (vm.PruneImage), with the
// entry, the Go-bound host keys, whether it has a `fn main`, and the
// project root std/compiler's hosts resolve against.
//
// A program the VM would block is refused here with the *Blocked `nomi run`
// would report, before anything is written: a built binary that could only
// print BLOCKED lines is not a program. usesCompiler reports whether the
// program crosses into std/compiler's hosts, which a runner links only when
// asked, because they link the whole front end.
//
// A file with no `fn main` is refused too, the way `go build` writes nothing
// for a package that is not main: `nomi run` would run nothing. A program
// whose exported functions a host calls is built by BuildExportsImage.
//
// So is a program with a `todo` or a `dbg` in any of its files, reached or
// not, with a *LeftoversRemain listing each one: a built binary is a release,
// a `todo` is code nobody has written yet, and a `dbg` is development-only
// instrumentation. The stdlib has neither.
func (p *Program) BuildImage() (image []byte, usesCompiler bool, err error) {
	return p.buildImage(false)
}

// BuildExportsImage is BuildImage for a host that calls the program's
// exported functions: `nomi build --web`, and the wasip1 reactor `nomi build
// --target wasip1/wasm` writes for a program with an `export fn`. The
// program may have no `fn main` when it has exported functions, the image
// records each one, and each is a root the build must be able to run.
func (p *Program) BuildExportsImage() (image []byte, usesCompiler bool, err error) {
	return p.buildImage(true)
}

// WebDeclarations is the nomi.d.ts `nomi build --web` writes beside
// nomi.js: the loader's API, with a typed `call` for each exported function
// (internal/webtypes).
func (p *Program) WebDeclarations() string {
	nodes := make([][]ast.Node, len(p.prog.Modules))
	for i, m := range p.prog.Modules {
		nodes[i] = m.Nodes
	}
	return webtypes.Declarations(filepath.Base(p.prog.Entry().Path), p.res.Exports, nodes)
}

func (p *Program) buildImage(withExports bool) (image []byte, usesCompiler bool, err error) {
	if !withExports || !p.HasExports() {
		if err := p.RequireMain("build"); err != nil {
			return nil, false, err
		}
	}
	if sites := p.leftovers(); len(sites) > 0 {
		return nil, false, &LeftoversRemain{Sites: sites}
	}
	var roots []*ir.Func
	if p.HasMain() {
		mainFn := p.entryFunc("main")
		if mainFn == nil {
			return nil, false, p.blockedName("main")
		}
		roots = vm.MainRoots(p.entry, mainFn)
	}
	var exports []ir.Export
	if withExports {
		for _, e := range p.res.Exports {
			if e.Func == nil {
				return nil, false, p.blockedName(e.Name)
			}
			if !e.Lowered() {
				return nil, false, &Blocked{Reasons: []string{fmt.Sprintf(
					"[%s] the JSON conversion of its arguments or result is not supported yet", e.Name)}}
			}
			x := ir.Export{Name: e.Name, Func: e.Func, Encode: e.Encode}
			roots = append(roots, e.Func)
			for _, prm := range e.Params {
				x.Params = append(x.Params, ir.ExportParam{Name: prm.Name, Type: prm.Type.String(), Decode: prm.Decode})
				roots = append(roots, prm.Decode)
			}
			if e.Encode != nil {
				x.Result = e.Result.String()
				roots = append(roots, e.Encode)
			}
			exports = append(exports, x)
		}
	}
	var boots []*ir.Symbol
	if p.entry != nil {
		if boot := p.entry.Boot(); boot != nil {
			boots = append(boots, boot)
		}
	}
	if found := p.machine(io.Discard).Unretained(roots, boots); len(found) > 0 {
		return nil, false, p.blocked(found)
	}
	// The image carries only what the program can run: the functions the
	// VM's own walk reaches from main, the boot and the exports. A lowering
	// links the whole stdlib, most of which a program never calls.
	entryMod, mods := vm.PruneImage(p.entry, p.res.IRModules(), roots, boots)
	// p.hosts[0] is std/compiler's table (newProgram puts it first). A
	// crossing only it answers is what makes the runner link it.
	bare := vm.NewProgram(entryMod, mods, io.Discard).WithHosts(p.hosts[1:]...)
	for _, u := range bare.Unretained(roots, boots) {
		if u.Kind == vm.NoBinding && slices.Contains(compilerhosts.Names(), u.Name) {
			usesCompiler = true
		}
	}
	entry := -1
	for i, m := range mods {
		if m == entryMod && entryMod != nil {
			entry = i
		}
	}
	// The project root is what std/compiler's hosts resolve imports
	// against; a program that never reaches them does not need the build
	// machine's directory in its image.
	root := ""
	if usesCompiler {
		root = p.prog.Root
	}
	image, err = ir.EncodeImage(ir.Image{
		Modules:  mods,
		Entry:    entry,
		HostKeys: p.res.HostKeys(),
		HasMain:  p.HasMain(),
		Root:     root,
		Exports:  exports,
	}, ir.WithFileNames(p.imageName))
	return image, usesCompiler, err
}

// imageName is how a built image names a source file, as `go build
// -trimpath` does: a stdlib module as `std/<file>.nomi`, a file under the
// project root by its slash-separated path from the root (`app.nomi`,
// `lib/util.nomi`), and any other file by its base name. A refusal a built
// program prints names the entry as the project knows it, and a
// distributed image carries none of the build machine's directories.
func (p *Program) imageName(path string) string {
	if name, ok := std.ImageName(path); ok {
		return name
	}
	if !filepath.IsAbs(path) {
		return filepath.ToSlash(path)
	}
	if p.prog.Root != "" {
		if rel, err := filepath.Rel(p.prog.Root, path); err == nil && rel != ".." &&
			!strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.Base(path)
}

// LeftoverKind says which development-only form a Leftover is.
type LeftoverKind int

const (
	LeftoverTodo LeftoverKind = iota // `todo`: code not written yet
	LeftoverDbg                      // `dbg`: debug instrumentation
)

// Leftover is one `todo` or `dbg` in a program's source: its file, its
// position and its text. A todo's text is its reason, or "" for a bare
// `todo`. A dbg's is its operand on one line, cut to dbgTextLimit runes, or
// "" for the pipe stage `|> dbg`.
type Leftover struct {
	Kind LeftoverKind
	Path string
	Line int
	Col  int
	Text string
}

// LeftoversRemain is the error BuildImage answers for a program with any
// `todo` or `dbg` in its files, in file and source order.
type LeftoversRemain struct {
	Sites []Leftover
}

func (l *LeftoversRemain) Error() string {
	todos, dbgs := 0, 0
	for _, s := range l.Sites {
		if s.Kind == LeftoverTodo {
			todos++
		} else {
			dbgs++
		}
	}
	var counts []string
	if todos > 0 {
		counts = append(counts, countOf(todos, "todo"))
	}
	if dbgs > 0 {
		counts = append(counts, countOf(dbgs, "dbg"))
	}
	verb := "remain"
	if todos+dbgs == 1 {
		verb = "remains"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s:", strings.Join(counts, " and "), verb)
	for _, s := range l.Sites {
		fmt.Fprintf(&b, "\n  %s:%d:%d ", DisplayPath(s.Path), s.Line, s.Col)
		switch {
		case s.Kind == LeftoverTodo && s.Text == "":
			b.WriteString("todo")
		case s.Kind == LeftoverTodo:
			fmt.Fprintf(&b, "todo %q", s.Text)
		case s.Text == "":
			b.WriteString("|> dbg")
		default:
			b.WriteString("dbg " + s.Text)
		}
	}
	return b.String()
}

func countOf(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// leftovers is every `todo` and `dbg` in the program's own files (every user
// file in the entry's import graph), in file order and source order within a
// file.
func (p *Program) leftovers() []Leftover {
	var out []Leftover
	for _, m := range p.prog.Modules {
		var sites []Leftover
		todos, dbgs := analysis.TodosAndDbgs(m.Nodes)
		for _, t := range todos {
			sites = append(sites, Leftover{Kind: LeftoverTodo, Path: m.Path, Line: t.Line, Col: t.Col, Text: t.ReasonText()})
		}
		for _, d := range dbgs {
			text := ""
			if d.Expr != nil {
				text = compactSource(format.RenderNode(d.Expr), dbgTextLimit)
			}
			sites = append(sites, Leftover{Kind: LeftoverDbg, Path: m.Path, Line: d.Line, Col: d.Col, Text: text})
		}
		slices.SortStableFunc(sites, func(a, b Leftover) int {
			if a.Line != b.Line {
				return a.Line - b.Line
			}
			return a.Col - b.Col
		})
		out = append(out, sites...)
	}
	return out
}

// dbgTextLimit is how many runes of a dbg's operand the refusal shows.
const dbgTextLimit = 40

// compactSource is src on one line, each run of whitespace one space, cut to
// limit runes with "..." after it when longer.
func compactSource(src string, limit int) string {
	one := strings.Join(strings.Fields(src), " ")
	if r := []rune(one); len(r) > limit {
		return string(r[:limit]) + "..."
	}
	return one
}
