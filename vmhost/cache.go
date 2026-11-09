package vmhost

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/progcache"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/std"
)

// Cache is one program's entry in the artifact cache (internal/progcache):
// the lowered program a `nomi run`, `check` or `test` of the same files made
// before, which the next one runs instead of checking and lowering them
// again.
//
// Only a program that checks and lowers cleanly is kept: no diagnostic, no
// warning, no body the builder declined, and nothing its main, boot or test cases reach
// that a machine could not run. So a hit says the program checks, and a
// program a hit runs reports nothing a fresh load would have reported
// differently. A program with Go bindings is never kept; its wrapper builds
// it.
type Cache struct {
	entry *progcache.Entry
	path  string
	// kind is what the Cache keeps, and files the program's file record
	// (filerecord.go) beside a load's, opened when first needed.
	kind  string
	files *progcache.Entry
}

// fileEntry opens c's file record from c's snapshot, for a load's cache.
func (c *Cache) fileEntry() *progcache.Entry {
	if c.files == nil && c.kind == loadKind {
		c.files = c.entry.Sibling(filesKind)
	}
	return c.files
}

// OpenCache opens the entry of the program whose entry file is path, a
// file on disk, and snapshots the files it is expected to read. It answers
// nil when the cache is off (artifactcache.ProgramRoot) or path is a stdlib
// module. Every method of a nil Cache answers as a miss.
func OpenCache(path string) *Cache { return OpenCacheFor(path, loadKind) }

// OpenCacheFor is OpenCache for what a caller builds from the program
// beside its lowering, kind naming it with every setting it depends on (`nomi
// build`'s image for a target): Kept reads it and Keep writes it.
func OpenCacheFor(path, kind string) *Cache {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil
	}
	if _, _, ok := frontend.StdlibFile(abs); ok {
		return nil
	}
	root := analysis.ProjectRoot(abs, "")
	e := progcache.Open(kind, abs, []progcache.Tree{{Dir: root}})
	if e == nil {
		return nil
	}
	return &Cache{entry: e, path: abs, kind: kind}
}

// loadKind names what a Cache keeps: one lowering of the program, which
// run, check and test share, since all three analyze a file in the mode its
// own tests decide (irbuild.AnalyzeFile).
const loadKind = "load"

// Checked reports whether the cache holds the program as its files are now,
// which a program is kept only when it checks cleanly. When it does, the
// program's advice (Program.Advice), kept with it, goes where opts'
// WithWarnings says, as Check writes a fresh program's.
func (c *Cache) Checked(opts ...Option) bool {
	if c == nil {
		return false
	}
	cfg := newConfig(opts)
	if data := c.entry.Payload(); data != nil {
		a, err := decodeArtifact(data)
		if err != nil || a.entry != c.path {
			return false
		}
		writeAdvice(cfg, a.advice)
		return true
	}
	p, ok := c.relink(cfg)
	if ok {
		writeAdvice(cfg, p.Advice())
	}
	return ok
}

// writeAdvice writes ds where cfg's WithWarnings says, when there are any.
func writeAdvice(cfg config, ds Diagnostics) {
	if f := cfg.warnings; f != nil && len(ds) > 0 {
		f(ds)
	}
}

// Program is the kept program, ready to run or test, or false when there
// is none, it does not decode, or it has no main and no tests. opts are
// Load's; a host table, virtual file or REPL session makes it a miss.
func (c *Cache) Program(opts ...Option) (*Program, bool) {
	if c == nil || irbuild.DeclinesPlanted() {
		return nil, false
	}
	data := c.entry.Payload()
	cfg := newConfig(opts)
	if !cfg.cacheable() {
		return nil, false
	}
	if data == nil {
		return c.relink(cfg)
	}
	a, err := decodeArtifact(data)
	if err != nil || a.entry != c.path || len(a.image) == 0 {
		return nil, false
	}
	im, err := ir.DecodeImage(a.image)
	if err != nil {
		return nil, false
	}
	prog := &irbuild.Program{
		Modules:  []irbuild.Module{{Path: a.entry, Name: strings.TrimSuffix(filepath.Base(a.entry), ".nomi")}},
		HasTests: a.hasTests,
		Root:     a.root,
	}
	if a.hasTests {
		src, err := os.ReadFile(a.entry)
		if err != nil {
			return nil, false
		}
		nodes, err := parser.Parse(lexer.Lex(string(src)))
		if err != nil {
			return nil, false
		}
		prog.Modules[0].Nodes = nodes
	}
	res := &irbuild.Result{HasMain: im.HasMain, IR: im.Modules}
	p := newProgram(prog, res, map[string]string{}, cfg)
	p.tablesOnce.Do(func() { p.tables = [2][]string{a.phrases, a.nouns} })
	p.advice = &a.advice
	if p.entry == nil || !p.runsClean() {
		return nil, false
	}
	return p, true
}

// Kept is what Keep wrote for the program as its files are now, or nil.
func (c *Cache) Kept() []byte {
	if c == nil || irbuild.DeclinesPlanted() {
		return nil
	}
	return c.entry.Payload()
}

// Keep writes payload, which the caller made from p, a program Load loaded
// from c's entry file with no options, as what c's kind holds for p's files.
func (c *Cache) Keep(p *Program, payload []byte) {
	if c == nil || p == nil || !p.cfg.cacheable() || irbuild.DeclinesPlanted() || p.prog.Entry().Path != c.path {
		return
	}
	trees, files := p.inputs()
	c.entry.Store(trees, files, payload)
}

// WithCache makes Load and Check keep the program in c when it checks and
// lowers cleanly, so the next command over the same files reads it.
func WithCache(c *Cache) Option {
	return func(cfg *config) { cfg.cache = c }
}

// cacheable reports whether a program loaded with c is the program its files
// on disk make, which is all the cache knows of: no host tables, virtual
// files, manifest or root, and no REPL session.
func (c *config) cacheable() bool {
	return len(c.hosts) == 0 && c.virtualFiles == nil && c.virtualData == nil && c.manifest == nil &&
		c.projectRoot == "" && c.session == nil && !c.allowUnusedBindings
}

// keep writes p to its load's cache, when it has one and p is clean.
// checked says the caller already found p.Unsupported() nil.
func (p *Program) keep(checked bool) {
	c := p.cfg.cache
	if c == nil || !p.cfg.cacheable() || irbuild.DeclinesPlanted() {
		return
	}
	// The slowest part of the file record (keepFiles) starts first.
	p.res.PrepareKeep()
	if (!checked && p.Unsupported() != nil) || !p.runsClean() {
		return
	}
	// A program with warnings is not kept, so each run reports them, and
	// the pattern's base appearing is a change like any other.
	if len(p.Warnings()) > 0 {
		return
	}
	// A program with advice is, with its advice, which a check that finds
	// it kept writes as a fresh check does.
	a := artifact{entry: p.prog.Entry().Path, root: p.prog.Root, hasTests: p.prog.HasTests, advice: p.Advice()}
	a.phrases, a.nouns = p.phraseTables()
	if a.entry != c.path {
		return
	}
	if p.HasMain() || p.prog.HasTests {
		image, err := p.cacheImage()
		if err != nil {
			return
		}
		a.image = image
	}
	payload := a.encode()
	if payload == nil {
		return
	}
	// The file record is written beside the load's, on another goroutine:
	// its lowering's walk and its facts take longer than the load's write.
	files := c.fileEntry()
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.keepFiles(files)
	}()
	trees, inputs := p.inputs()
	c.entry.Store(trees, inputs, payload)
	<-done
}

// runsClean reports whether nothing the program's main, boot and test cases
// reach is something a machine cannot run, and every case its file declares
// was lowered: what a kept program must hold, so that running it reports
// nothing a fresh load would report with the diagnostics only a lowering has.
func (p *Program) runsClean() bool {
	if p.entry == nil {
		return !p.HasMain() && !p.prog.HasTests
	}
	m := p.machine(nilWriter{})
	if p.HasMain() {
		mainFn := p.entryFunc("main")
		if mainFn == nil {
			return false
		}
		var boots []*ir.Symbol
		if boot := p.entry.Boot(); boot != nil {
			boots = append(boots, boot)
		}
		if len(m.Unretained(vm.MainRoots(p.entry, mainFn), boots)) > 0 {
			return false
		}
	}
	if !p.prog.HasTests {
		return true
	}
	plan, duplicates := frontend.SelectTests(p.prog.Entry().Nodes, TestOptions{})
	if len(duplicates) > 0 {
		return false
	}
	retained := map[string]ir.TestCase{}
	for _, tc := range p.entry.Tests() {
		retained[tc.Name()] = tc
	}
	for _, tc := range plan {
		c, ok := retained[tc.FullName()]
		if !ok {
			return false
		}
		var boots []*ir.Symbol
		if b := c.Group().Boot; b != nil {
			boots = append(boots, b, c.Group().Startup)
		}
		if len(m.Unretained([]*ir.Func{c.Fn()}, boots)) > 0 {
			return false
		}
	}
	return true
}

// cacheImage is the program's IR as one image: for a program with tests,
// every module, since its cases may reach anything; else what main and the
// boot reach (vm.PruneImage), as `nomi build` keeps. Positions keep their
// paths, so a failure reads as a fresh load's.
func (p *Program) cacheImage() ([]byte, error) {
	mods := p.res.IRModules()
	entry := p.entry
	if !p.prog.HasTests {
		var boots []*ir.Symbol
		if boot := p.entry.Boot(); boot != nil {
			boots = append(boots, boot)
		}
		entry, mods = vm.PruneImage(p.entry, mods, vm.MainRoots(p.entry, p.entryFunc("main")), boots)
	}
	index := -1
	for i, m := range mods {
		if m == entry {
			index = i
		}
	}
	if index < 0 {
		return nil, errors.New("vmhost: the entry is not among the program's modules")
	}
	return ir.EncodeImage(ir.Image{Modules: mods, Entry: index, HostKeys: p.res.HostKeys(), HasMain: p.HasMain(), Root: p.prog.Root})
}

// inputs are what the program was built from: its root's tree, each other
// module's root (a dependency's), each `embed` pattern's base (the
// directory or file its literal leading names spell) with every file in it, and each of its files and the catalogs its phrases read.
func (p *Program) inputs() ([]progcache.Tree, []string) {
	root := p.prog.Root
	if root == "" {
		root = filepath.Dir(p.prog.Entry().Path)
	}
	trees := []progcache.Tree{{Dir: root}}
	var files []string
	under := func(dir, path string) bool {
		rel, err := filepath.Rel(dir, path)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	for _, m := range p.prog.Modules {
		if !filepath.IsAbs(m.Path) {
			continue
		}
		if _, ok := std.ImageName(m.Path); ok {
			continue
		}
		files = append(files, m.Path)
		if !under(root, m.Path) {
			trees = append(trees, progcache.Tree{Dir: analysis.ProjectRoot(m.Path, "")})
		}
		for _, in := range analysis.ExpansionInputs(m.Nodes) {
			if filepath.IsAbs(in.Path) {
				files = append(files, in.Path)
			}
		}
		for _, n := range m.Nodes {
			if o, ok := n.(*ast.OnceBinding); ok {
				if info := analysis.EmbedOf(o); info != nil && filepath.IsAbs(info.Root) {
					for _, base := range info.Bases() {
						trees = append(trees, progcache.Tree{Dir: filepath.Join(info.Root, filepath.FromSlash(base)), All: true})
					}
					// Each embedded file is stamped too: a tree's walk skips
					// hidden names and node_modules, which an `all:`
					// pattern or a pattern naming the file embeds.
					for _, f := range info.Files {
						files = append(files, filepath.Join(info.Root, filepath.FromSlash(f.Path)))
					}
				}
			}
		}
	}
	return trees, files
}

// artifact is what a Cache keeps for a program.
type artifact struct {
	entry, root    string
	hasTests       bool
	phrases, nouns []string
	// advice is the program's advice (Program.Advice), as a fresh check
	// writes it.
	advice Diagnostics
	// image is the program's IR (cacheImage), or empty for a program with
	// neither main nor tests, which is kept only to say it checks.
	image []byte
}

const (
	artifactMagic   = "NOMILOAD"
	artifactVersion = 2
)

func (a artifact) encode() []byte {
	var b bytes.Buffer
	b.WriteString(artifactMagic)
	putUvarint(&b, artifactVersion)
	putString(&b, a.entry)
	putString(&b, a.root)
	if a.hasTests {
		b.WriteByte(1)
	} else {
		b.WriteByte(0)
	}
	for _, list := range [][]string{a.phrases, a.nouns} {
		putUvarint(&b, uint64(len(list)))
		for _, s := range list {
			putString(&b, s)
		}
	}
	var advice bytes.Buffer
	if len(a.advice) > 0 && gob.NewEncoder(&advice).Encode(a.advice) != nil {
		return nil
	}
	putString(&b, advice.String())
	putUvarint(&b, uint64(len(a.image)))
	b.Write(a.image)
	return b.Bytes()
}

var errBadArtifact = errors.New("vmhost: not a kept program")

func decodeArtifact(data []byte) (a artifact, err error) {
	if !bytes.HasPrefix(data, []byte(artifactMagic)) {
		return a, errBadArtifact
	}
	r := bytes.NewReader(data[len(artifactMagic):])
	uv := func() uint64 {
		v, e := binary.ReadUvarint(r)
		if e != nil && err == nil {
			err = errBadArtifact
		}
		return v
	}
	str := func() string {
		n := uv()
		if err != nil || n > uint64(r.Len()) {
			err = errBadArtifact
			return ""
		}
		buf := make([]byte, n)
		_, _ = r.Read(buf)
		return string(buf)
	}
	if uv() != artifactVersion {
		return a, errBadArtifact
	}
	a.entry, a.root = str(), str()
	flag, e := r.ReadByte()
	if e != nil {
		return a, errBadArtifact
	}
	a.hasTests = flag == 1
	for _, list := range []*[]string{&a.phrases, &a.nouns} {
		n := uv()
		for i := uint64(0); i < n && err == nil; i++ {
			*list = append(*list, str())
		}
	}
	if advice := str(); err == nil && advice != "" {
		if gob.NewDecoder(strings.NewReader(advice)).Decode(&a.advice) != nil {
			return a, errBadArtifact
		}
	}
	n := uv()
	if err != nil || n != uint64(r.Len()) {
		return a, errBadArtifact
	}
	a.image = data[len(data)-int(n):]
	return a, err
}

func putUvarint(b *bytes.Buffer, v uint64) {
	var buf [binary.MaxVarintLen64]byte
	b.Write(buf[:binary.PutUvarint(buf[:], v)])
}

func putString(b *bytes.Buffer, s string) {
	putUvarint(b, uint64(len(s)))
	b.WriteString(s)
}
