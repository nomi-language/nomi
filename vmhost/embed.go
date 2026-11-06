package vmhost

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// EmbeddedFiles are the files the program's `embed` declarations hold,
// each once, sorted, by its slash-separated path from the program's root:
// the files it read when it was compiled. The tour counts each as a read
// of its data file, since an edit to one changes what the program prints.
func (p *Program) EmbeddedFiles() []string {
	seen := map[string]bool{}
	for _, m := range p.prog.Modules {
		for _, n := range m.Nodes {
			o, ok := n.(*ast.OnceBinding)
			if !ok {
				continue
			}
			info := analysis.EmbedOf(o)
			if info == nil {
				continue
			}
			dir := "."
			if info.Root != "" {
				if rel, err := filepath.Rel(p.prog.Root, info.Root); err == nil {
					dir = filepath.ToSlash(rel)
				}
			}
			for _, f := range info.Files {
				seen[path.Join(dir, f.Path)] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// Warnings are what the program compiles with and reports: each `embed`
// pattern whose base does not exist yet, in file and source order. The
// program runs with an empty directory in its place; a build refuses it
// (missingEmbeds). Load, Check and the REPL write them where WithWarnings
// says.
func (p *Program) Warnings() Diagnostics {
	var out Diagnostics
	for _, m := range p.prog.Modules {
		out = append(out, frontend.WarningDiagnostics(m.Path, p.sources[m.Path], analysis.EmbedWarnings(m.Nodes))...)
	}
	return out
}

// missingEmbeds are Warnings as errors, which a build answers: an image
// holds the files its program's patterns name when it is built, so a
// pattern whose base does not exist is refused.
func (p *Program) missingEmbeds() Diagnostics {
	var out Diagnostics
	for _, m := range p.prog.Modules {
		var errs []analysis.TypeError
		for _, n := range m.Nodes {
			if o, ok := n.(*ast.OnceBinding); ok {
				if info := analysis.EmbedOf(o); info != nil {
					errs = append(errs, info.MissingErrors()...)
				}
			}
		}
		if len(errs) > 0 {
			ds := frontend.WarningDiagnostics(m.Path, p.sources[m.Path], errs)
			for i := range ds {
				ds[i].Warning = false
			}
			out = append(out, ds...)
		}
	}
	return out
}

// writeWarnings hands p's warnings to its load's WithWarnings.
func (p *Program) writeWarnings() {
	if f := p.cfg.warnings; f != nil {
		if ds := p.Warnings(); len(ds) > 0 {
			f(ds)
		}
	}
}

// embeddedSource is where the program's machines read its embedded files,
// or nil for the disk (vm.DiskEmbedded): an in-memory program reads them
// from its data files.
func (p *Program) embeddedSource() rt.EmbeddedSource {
	if p.cfg.virtualData != nil {
		return dataEmbedded(p.cfg.virtualData)
	}
	return nil
}

// dataEmbedded reads an in-memory program's embedded files from its data
// files, by their paths from its root (analysis.MapEmbedFS's Source). The
// answer shares the data file's bytes, and is checked against the sum the
// program was compiled with, as the disk's is.
type dataEmbedded map[string]string

func (d dataEmbedded) ReadEmbedded(f rt.EmbeddedFile) (rt.Bytes, error) {
	text, ok := d[f.Source]
	if !ok {
		return "", fmt.Errorf("%s was removed after the program was compiled", f.Source)
	}
	if int64(len(text)) != f.Size || sha256Hex(text) != f.Sum {
		return "", fmt.Errorf("%s changed after the program was compiled. Run the program again", f.Source)
	}
	return rt.Bytes(text), nil
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// EmbedSource is where a build reads one embedded file's contents to write
// them into an image's files region (ir.Image.Files): the file on disk,
// and the size and SHA-256 the program was compiled with.
type EmbedSource struct {
	Sum  string
	Size int64
	Path string
}

// EmbeddedSources is where each file image's files region holds is read
// from: the source of the file the program's code names by that sum. image
// is one this program built (BuildImage), whose code names its files as
// the build trims them, so the sources come from the program's own
// lowering, which names them by their paths on disk.
func (p *Program) EmbeddedSources(image []byte) ([]EmbedSource, error) {
	im, err := ir.DecodeImage(image)
	if err != nil {
		return nil, err
	}
	if len(im.Files) == 0 {
		return nil, nil
	}
	bySum := map[string]ir.EmbeddedRef{}
	for _, f := range embeddedRefs(p.res.IRModules()) {
		bySum[f.Sum] = f
	}
	out := make([]EmbedSource, 0, len(im.Files))
	for _, f := range im.Files {
		ref, ok := bySum[f.Sum]
		if !ok || !filepath.IsAbs(ref.Source) {
			return nil, fmt.Errorf("the image holds an embedded file (sha256 %s) whose file on disk the program does not name", f.Sum)
		}
		out = append(out, EmbedSource{Sum: f.Sum, Size: f.Size, Path: ref.Source})
	}
	return out, nil
}

// embeddedRefs is every embedded file the functions of mods name.
func embeddedRefs(mods []*ir.Module) []ir.EmbeddedRef {
	var out []ir.EmbeddedRef
	seen := map[*ir.Func]bool{}
	var walk func(f *ir.Func)
	walk = func(f *ir.Func) {
		if f == nil || seen[f] {
			return
		}
		seen[f] = true
		for _, b := range f.Blocks() {
			for _, in := range b.Instrs() {
				switch n := in.(type) {
				case *ir.Const:
					if n.Kind() == ir.ConstEmbedded {
						out = append(out, n.Embedded())
					}
				case *ir.FuncValue:
					walk(n.Body())
				}
			}
		}
	}
	for _, m := range mods {
		if m == nil {
			continue
		}
		for _, f := range m.Funcs() {
			walk(f)
		}
		for _, c := range m.Cells() {
			walk(c.Initializer())
		}
	}
	return out
}

// FilesRegionSize is how long the files region of image is: what follows
// its code in an executable, a .nomic file or a module section.
func FilesRegionSize(image []byte) (int64, error) {
	im, err := ir.DecodeImage(image)
	if err != nil {
		return 0, err
	}
	return ir.FilesRegionSize(im.Files), nil
}

// WriteFilesRegion writes image's files region to w: each file its table
// lists, in order, read from its source a buffer at a time, so no more
// than a buffer of it is held. A file whose size or SHA-256 is not what
// the program was compiled with is an error naming it, and what was
// written before it is not a whole region.
func WriteFilesRegion(w io.Writer, image []byte, sources []EmbedSource) error {
	im, err := ir.DecodeImage(image)
	if err != nil {
		return err
	}
	bySum := map[string]EmbedSource{}
	for _, s := range sources {
		bySum[s.Sum] = s
	}
	for _, f := range im.Files {
		src, ok := bySum[f.Sum]
		if !ok {
			return fmt.Errorf("no source for the embedded file with sha256 %s", f.Sum)
		}
		if err := copyEmbedded(w, src); err != nil {
			return err
		}
	}
	return nil
}

// copyEmbedded copies src's file to w, checking it is the file the program
// was compiled with.
func copyEmbedded(w io.Writer, src EmbedSource) error {
	file, err := os.Open(src.Path)
	if err != nil {
		return fmt.Errorf("embedded file %v; it was removed or moved after the program was compiled", err)
	}
	defer file.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, h), io.LimitReader(file, src.Size))
	if err != nil {
		return err
	}
	// A file that grew is caught by reading one byte past its size.
	var extra [1]byte
	grew, _ := file.Read(extra[:])
	if n != src.Size || grew > 0 || hex.EncodeToString(h.Sum(nil)) != src.Sum {
		return fmt.Errorf("embedded file %s changed while the program was built; build it again", src.Path)
	}
	return nil
}

// EncodedEmbeddedSources is EmbeddedSources as the bytes an FFI project's
// wrapper hands `nomi build` beside the image it built, which
// DecodeEmbedSources reads back.
func (p *Program) EncodedEmbeddedSources(image []byte) ([]byte, error) {
	sources, err := p.EmbeddedSources(image)
	if err != nil {
		return nil, err
	}
	return json.Marshal(sources)
}

// DecodeEmbedSources reads what EncodedEmbeddedSources wrote.
func DecodeEmbedSources(data []byte) ([]EmbedSource, error) {
	var out []EmbedSource
	err := json.Unmarshal(data, &out)
	return out, err
}
