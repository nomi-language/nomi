package vmhost

import (
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/cldrfull"
	"github.com/nomi-language/nomi/internal/compilerhosts"
	"github.com/nomi-language/nomi/internal/format"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/mf2"
	"github.com/nomi-language/nomi/internal/stdformat"
	"github.com/nomi-language/nomi/internal/stdlibbindings"
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
	im, usesCompiler, need, err := p.image(withExports)
	if err != nil {
		return nil, false, err
	}
	im.Locales = cldrfull.Select(need.selection(p.locales, p.source))
	image, err = ir.EncodeImage(im, ir.WithFileNames(p.imageName))
	return image, usesCompiler, err
}

// image is the image BuildImage writes, without its locale data, and the
// parts of that data the program reaches.
func (p *Program) image(withExports bool) (im ir.Image, usesCompiler bool, need localeNeed, err error) {
	if !withExports || !p.HasExports() {
		if err := p.RequireMain("build"); err != nil {
			return ir.Image{}, false, localeNeed{}, err
		}
	}
	if sites := p.leftovers(); len(sites) > 0 {
		return ir.Image{}, false, localeNeed{}, &LeftoversRemain{Sites: sites}
	}
	var roots []*ir.Func
	if p.HasMain() {
		mainFn := p.entryFunc("main")
		if mainFn == nil {
			return ir.Image{}, false, localeNeed{}, p.blockedName("main")
		}
		roots = vm.MainRoots(p.entry, mainFn)
	}
	var exports []ir.Export
	if withExports {
		for _, e := range p.res.Exports {
			if e.Func == nil {
				return ir.Image{}, false, localeNeed{}, p.blockedName(e.Name)
			}
			if !e.Lowered() {
				return ir.Image{}, false, localeNeed{}, &Blocked{Reasons: []string{fmt.Sprintf(
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
		return ir.Image{}, false, localeNeed{}, p.blocked(found)
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
	return ir.Image{
		Modules:  mods,
		Entry:    entry,
		HostKeys: p.res.HostKeys(),
		HasMain:  p.HasMain(),
		Root:     root,
		Exports:  exports,
	}, usesCompiler, localeNeeds(vm.ReachedCrossings(p.entry, p.res.IRModules(), roots, boots), mods), nil
}

func init() { cldrfull.Resolver = stdformat.ResolveSkeleton }

// localeNeed is what of the CLDR data an image carries: the parts its
// crossings read, and the skeletons it formats dates with when all of them
// are known (nil when none are, or some are made while it runs).
type localeNeed struct {
	parts     cldrdata.Parts
	skeletons []string
	// currencies are the ISO codes of every currency the program can
	// format, when all are known (nil when some are made while it runs).
	currencies []string
}

// selection is the bundle of need for locales, whose source locale is
// source ("" for a locale file's bundle, which names none).
func (need localeNeed) selection(locales []string, source string) cldrdata.Selection {
	return cldrdata.Selection{Locales: locales, Parts: need.parts, Skeletons: need.skeletons, Currencies: need.currencies,
		Source: source}
}

// localeNeeds is the CLDR data an image of mods, which reaches crossings,
// carries (stdlibbindings.LocaleParts). A program that reaches no crossing
// that reads locale data carries none.
//
// Messages read more than their crossing says: a message's text may use
// `:currency` or the date functions, so the parts those need are added
// when the program's message tables use them, and `:datetime`'s fields
// add their skeletons. A program that loads catalogs while it runs may
// format any of them, and carries all.
//
// A program whose skeletons are all literals (vm.SkeletonLiterals) or
// message fields carries each one's resolved pattern instead of the
// locales' available formats.
//
// Currency names, and the plural rules that pick a name's form, are
// carried only when the program can write one (vm.CurrencyNames, or a
// message whose `:currency` says `currencyDisplay=name`); and a program
// whose currencies are all literals (vm.CurrencyLiterals) carries the
// symbols and names of those currencies alone.
func localeNeeds(crossings []string, mods []*ir.Module) localeNeed {
	table := stdlibbindings.LocaleParts()
	reached := map[string]bool{}
	var need localeNeed
	for _, c := range crossings {
		parts := table[c]
		if c == moneyFormat {
			parts &^= currencyNames
		}
		need.parts |= parts
		reached[c] = true
	}
	funcs := vm.ModuleFuncs(mods)
	names := reached[moneyFormat] && vm.CurrencyNames(mods, funcs)
	dynamic := false
	var skeletons []string
	if reached["message_format.format_raw"] {
		messages, _ := vm.MessageTables(mods, funcs, stdMessage)
		parts, fields, named := messageParts(messages)
		need.parts |= parts
		names = names || named
		skeletons = append(skeletons, fields...)
		if reached["message_format.load_raw"] {
			need.parts |= cldrdata.PartCurrencies | cldrdata.PartCurrencyCodes | cldrdata.PartDates | cldrdata.PartSkeletons
			names = true
			dynamic = true
		}
	}
	if names {
		need.parts |= currencyNames | cldrdata.PartListedParents
	}
	if need.parts&(cldrdata.PartCurrencies|cldrdata.PartCurrencyNames) != 0 {
		if codes, made := vm.CurrencyLiterals(mods, funcs); !made {
			need.currencies = append([]string{}, codes...)
		}
	}
	if need.parts&cldrdata.PartSkeletons != 0 || len(skeletons) > 0 {
		literals, made := vm.SkeletonLiterals(mods, funcs)
		skeletons = append(skeletons, literals...)
		dynamic = dynamic || made
	}
	if !dynamic {
		need.parts &^= cldrdata.PartSkeletons
		need.skeletons = skeletons
		if need.skeletons == nil {
			need.skeletons = []string{}
		}
	}
	return need
}

// moneyFormat is the crossing behind `Money.format`, and currencyNames
// the parts it reads only to write a currency's name.
const (
	moneyFormat   = "money_format.money_format_raw"
	currencyNames = cldrdata.PartCurrencyNames | cldrdata.PartPlurals
)

// messageParts are the parts of the CLDR data the functions message
// tables' texts use need beyond a message's own crossing, the skeletons
// their `:datetime` field options name, and whether a `:currency` writes a
// currency's name: `currencyDisplay=name`, or a variable display.
func messageParts(tables []string) (cldrdata.Parts, []string, bool) {
	var parts cldrdata.Parts
	var skeletons []string
	names := false
	for _, text := range tables {
		t, err := mf2.DecodeTable(text)
		if err != nil {
			continue
		}
		for _, src := range t.Texts {
			m, errs := mf2.Parse(src)
			if len(errs) > 0 {
				continue
			}
			for _, f := range mf2.Functions(m) {
				switch f.Name {
				case "currency":
					parts |= cldrdata.PartCurrencies | cldrdata.PartCurrencyCodes
					if o, ok := f.Option("currencyDisplay"); ok && (o.Value.Variable || o.Value.Value == "name") {
						names = true
					}
				case "date", "time", "datetime":
					parts |= cldrdata.PartDates
					if skel := mf2.Skeleton(f); f.Name == "datetime" && skel != "" {
						skeletons = append(skeletons, skel)
					}
				}
			}
		}
	}
	return parts, skeletons, names
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

// compactSource is src, the formatter's rendering of an operand, on one
// line, cut to limit runes with "..." after it when longer. The formatter
// keeps a literal broken one element per line when its source breaks after
// the bracket, so src may be broken where its flat form fits; the lines are
// joined as the flat form reads: nothing after an opening bracket, nothing
// before a closing one and no trailing comma there, one space elsewhere.
func compactSource(src string, limit int) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			continue
		}
		if b.Len() > 0 {
			joined := b.String()
			switch {
			case strings.ContainsAny(line[:1], ")]}"):
				if strings.HasSuffix(joined, ",") {
					b.Reset()
					b.WriteString(strings.TrimSuffix(joined, ","))
				}
			case !strings.ContainsAny(joined[len(joined)-1:], "([{"):
				b.WriteByte(' ')
			}
		}
		b.WriteString(line)
	}
	one := b.String()
	if r := []rune(one); len(r) > limit {
		return string(r[:limit]) + "..."
	}
	return one
}
