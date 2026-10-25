package vmhost

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"sort"

	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/cldrfull"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/localefiles"
	"github.com/nomi-language/nomi/internal/mf2"
	"github.com/nomi-language/nomi/internal/stdlocale"
	"github.com/nomi-language/nomi/internal/vm"
)

// WebImage is what `nomi build --web` writes for a program: its image, and
// the locale files a page loads beside it.
type WebImage struct {
	Image []byte
	// Files are the locale files by their path in the folder,
	// `locales/<tag>.nomil`; none when the image carries every locale.
	Files map[string][]byte
	// Embedded are the locales the image carries whole: every listed one
	// when Files is empty.
	Embedded     []string
	UsesCompiler bool
}

// BuildWebImage is BuildExportsImage for a page: a program that lists its
// locales (`[i18n].locales`) carries in its image only those
// `[i18n].web_embedded` names (the source locale unless it says
// otherwise), with the root data, every listed locale's plural rules and
// grammatical features, and every message's source text. Each other listed
// locale is a file, `locales/<tag>.nomil`, holding its CLDR numbers and
// lists and its messages' and nouns' texts, with those of each ancestor on
// its chain that is not embedded either; `Locale.load` fetches it
// (stdmessageformat's LocaleFile). Every locale was checked by the
// compiler all the same: the split moves bytes, not checks.
//
// The image and its files share a build id, a digest of them all, which
// the image's locale data names, so a page refuses a file a browser kept
// from another build.
func (p *Program) BuildWebImage() (*WebImage, error) {
	im, usesCompiler, need, err := p.image(true)
	if err != nil {
		return nil, err
	}
	parts := need.parts
	out := &WebImage{UsesCompiler: usesCompiler, Embedded: p.locales}
	var remote []string
	for _, tag := range p.locales {
		if !slices.Contains(p.webEmbedded, tag) {
			remote = append(remote, tag)
		}
	}
	messages, nouns := vm.MessageTableConsts(im.Modules, vm.ModuleFuncs(im.Modules), stdMessage)
	// A program with no messages that formats nothing has nothing to split.
	if len(remote) == 0 || len(messages)+len(nouns) == 0 && parts&(cldrdata.PartNumbers|cldrdata.PartLists) == 0 {
		im.Locales = cldrfull.Select(need.selection(p.locales, p.source))
		out.Image, err = ir.EncodeImage(im, ir.WithFileNames(p.imageName))
		return out, err
	}
	out.Embedded = append([]string{}, p.webEmbedded...)

	// The tables keep their source text and the embedded locales'; the
	// rest go to the files, by locale.
	keep := func(tag, source string) bool { return tag == source || slices.Contains(p.webEmbedded, tag) }
	texts := map[string]*localefiles.Translations{}
	textsOf := func(tag string) *localefiles.Translations {
		t := texts[tag]
		if t == nil {
			t = &localefiles.Translations{Locale: tag, Messages: map[string]string{}, Nouns: map[string]localefiles.NounForms{}}
			texts[tag] = t
		}
		return t
	}
	replaced := map[*ir.Const]string{}
	for _, c := range messages {
		t, err := mf2.DecodeTable(c.Text())
		if err != nil {
			continue
		}
		kept := *t
		kept.Texts = map[string]string{}
		for tag, text := range t.Texts {
			if keep(tag, t.Source) {
				kept.Texts[tag] = text
			} else {
				textsOf(tag).Messages[t.Name] = text
			}
		}
		replaced[c] = mf2.EncodeTable(kept)
	}
	for _, c := range nouns {
		t, err := mf2.DecodeNoun(c.Text())
		if err != nil {
			continue
		}
		kept := *t
		kept.Genders, kept.Forms = map[string]string{}, map[string]map[string]string{}
		for tag, forms := range t.Forms {
			if keep(tag, t.Source) {
				kept.Forms[tag] = forms
				if g, ok := t.Genders[tag]; ok {
					kept.Genders[tag] = g
				}
			} else {
				textsOf(tag).Nouns[t.Name] = localefiles.NounForms{Gender: t.Genders[tag], Forms: forms}
			}
		}
		replaced[c] = mf2.EncodeNoun(kept)
	}

	// Each file installs its locale and the ancestors on its chain that
	// are not embedded either.
	full := cldrfull.Full()
	files := map[string]*localefiles.File{}
	for _, tag := range remote {
		f := &localefiles.File{}
		for _, t := range stdlocale.Fallbacks(full, tag) {
			if slices.Contains(remote, t) {
				f.Locales = append(f.Locales, t)
				f.Texts = append(f.Texts, textsOf(t))
			}
		}
		sel := need.selection(f.Locales, "")
		sel.File = true
		if blob := cldrfull.Select(sel); len(blob) > 0 {
			f.CLDR = blob
		}
		files["locales/"+tag+".nomil"] = f
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	// The build id digests the image as written without it, and the files.
	sel := need.selection(p.locales, p.source)
	sel.Parts |= cldrdata.PartListedParents
	sel.Embedded = p.webEmbedded
	bundle := cldrfull.Select(sel)
	im.Locales = bundle
	unnamed, err := ir.EncodeImage(im, ir.WithFileNames(p.imageName), ir.WithConstTexts(replaced))
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	h.Write(unnamed)
	for _, name := range names {
		h.Write([]byte(name))
		h.Write(files[name].Encode())
	}
	id := hex.EncodeToString(h.Sum(nil))[:16]
	im.Locales = cldrdata.WithBuild(bundle, id)
	if out.Image, err = ir.EncodeImage(im, ir.WithFileNames(p.imageName), ir.WithConstTexts(replaced)); err != nil {
		return nil, err
	}
	out.Files = map[string][]byte{}
	for _, name := range names {
		files[name].Build = id
		out.Files[name] = files[name].Encode()
	}
	return out, nil
}
