package analysis

import (
	"crypto/sha256"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"sort"
)

// Lowering the entry of a program opened from its record.
//
// The server lowers an open document in the background after each burst
// of edits (internal/lsp/lowering.go). A document of a program opened from
// its record offers no EntryProgram to lower from, and a lowering from its
// text checks and lowers the whole program it reaches: about 2.1 s for the
// entry of a 50k-line project, where its importers' leaves cost 25 ms.
// So the record also keeps the entry's lowering kept per file
// (storedRecord.Kept, irbuild.KeptLowering encoded), which the server's
// lowering of the entry made when it wrote the record (NoteLoweredClean's
// KeptLoweringFunc, answered by vmhost through internal/analyzedlowering).
// A lowering of the entry after an edit (StoredLowering) lowers the files
// whose texts changed since a kept lowering, each from its check against
// the others' interfaces, and links them with the kept objects of the
// rest, as `nomi check` relinks a kept program (vmhost's
// checklowering_stored.go). Another document's lowering lowers its text.

// KeptLoweringFunc answers the kept lowering the server's last lowering of
// the document at path made, encoded, whether it lowered the document's
// tests, and the SHA-256 of the text of each file it read, by path; ok is
// false when there is none.
type KeptLoweringFunc func(path string) (data []byte, tests bool, sums map[string][32]byte, ok bool)

// keptOfState answers the kept lowering keptOf has for rec's entry when it
// was made from the texts rec holds, or nil.
func keptOfState(keptOf KeptLoweringFunc, rec *storedRecord) ([]byte, bool) {
	if keptOf == nil {
		return nil, false
	}
	data, tests, sums, ok := keptOf(filepath.Join(rec.Root, filepath.FromSlash(rec.Entry)+".nomi"))
	if !ok || len(sums) != len(rec.Files) {
		return nil, false
	}
	for key, f := range rec.Files {
		if sums[filepath.Join(rec.Root, filepath.FromSlash(key)+".nomi")] != f.Sum {
			return nil, false
		}
	}
	return data, tests
}

// StoredLowering is what lowering the entry of a program opened from its
// record reads: its files, how each stands now and in the record as it
// was opened, the record's kept lowering, and the check of each file.
type StoredLowering struct {
	Root, EntryPath string
	// Files are the program's files, each after the files it imports.
	Files []StoredLoweringFile
	// Kept is the record's kept lowering of the files as the record holds
	// them (each file's Base), and KeptTests whether it lowered the
	// entry's tests; nil when the record has none.
	Kept      []byte
	KeptTests bool
	// Check answers the check of the file with key as the program is now,
	// reading the files bodies names with their bodies, or nil when the
	// program is no longer as it was when StoredLowering answered.
	Check func(key string, bodies map[string]bool) *FileCheck
}

// StoredLoweringFile is one file of a StoredLowering: the SHA-256 of its
// text and of its interface as written, now and in the record as opened.
type StoredLoweringFile struct {
	Key, Path        string
	Sum, Iface       [32]byte
	BaseSum, BaseIfc [32]byte
	// Signature and BaseSig fingerprint what the files that import the file
	// read of it: its interface hash and its unannotated `once` values. A
	// file whose signature changed is lowered again with its importers.
	Signature, BaseSig [32]byte
	// Impls and BaseImpls fingerprint the files the file's impl headers
	// name, and Headers and BaseHeaders are those headers: a changed impl
	// is seen by files that need not import it, which a lowering after it
	// lowers again too.
	Impls, BaseImpls     [32]byte
	Headers, BaseHeaders []ImplHeader
	// Imports are the module keys of the project files it imports.
	Imports []string
}

// StoredLowering answers what lowering the open document at uri, whose
// text is content, reads, when it is the entry of a program opened from
// its record whose state holds every file's text, or nil.
func (dm *DocumentManager) StoredLowering(uri, content string) *StoredLowering {
	dm.lockBuild(false)
	defer dm.analyzeMu.Unlock()
	sp, key := dm.storedOf(uri)
	if sp == nil || key != sp.rec.Entry || !sp.clean() || sp.texts[key] != content || !dm.storedCurrent(sp, dm.openTexts(sp.rec.Root, "", "")) {
		return nil
	}
	rec, base := sp.rec, sp.base
	out := &StoredLowering{Root: rec.Root, EntryPath: filepath.Join(rec.Root, filepath.FromSlash(rec.Entry)+".nomi"),
		Kept: base.Kept, KeptTests: base.KeptTests}
	for _, k := range importOrderOf(sp.pi, mapKeys(rec.Files)) {
		f := rec.Files[k]
		b, ok := base.Files[k]
		if !ok {
			return nil
		}
		out.Files = append(out.Files, StoredLoweringFile{Key: k, Path: f.Iface.Path, Sum: f.Sum, Iface: sha256.Sum256(f.Iface.Encoded),
			BaseSum: b.Sum, BaseIfc: sha256.Sum256(b.Iface.Encoded),
			Signature: signatureOf(f.Iface), BaseSig: signatureOf(b.Iface), Imports: f.Iface.Imports,
			Impls: implsOf(f.Iface.Headers), BaseImpls: implsOf(b.Iface.Headers), Headers: f.Iface.Headers, BaseHeaders: b.Iface.Headers})
	}
	gen := sp.gen
	out.Check = func(k string, bodies map[string]bool) *FileCheck {
		dm.lockBuild(false)
		defer dm.analyzeMu.Unlock()
		if dm.stored[programKey{rec.Root, rec.Entry}] != sp || sp.gen != gen {
			return nil
		}
		if len(bodies) == 0 {
			if inst := sp.installed[k]; inst != nil && sp.current(inst, k) {
				return inst.c
			}
		}
		sp.pi.SetBodies(bodies)
		defer sp.pi.SetBodies(nil)
		dm.programCounts.built.Add(1)
		c := sp.pi.Check(k)
		if !c.clean() {
			return nil
		}
		return c
	}
	return out
}

// signatureOf fingerprints what the files that import a file read of its
// interface si: its hash, and its unannotated `once` values, whose types
// are theirs (InferredOncesChanged).
func signatureOf(si StoredInterface) [32]byte {
	h := sha256.New()
	h.Write([]byte(si.Hash))
	if fi, err := DecodeInterface(si.Encoded); err == nil {
		var b strings.Builder
		for _, n := range fi.Nodes {
			if o, ok := n.(*ast.OnceBinding); ok && o.TypeAnnotation == nil {
				b.WriteString(o.Name)
				b.WriteByte('=')
				fingerprintValue(&b, reflect.ValueOf(o.Value))
				b.WriteByte('\n')
			}
		}
		h.Write([]byte(b.String()))
	}
	return [32]byte(h.Sum(nil))
}

// implsOf fingerprints the files impl headers name, impl by impl, whatever
// their ordinals (sameImplHeaderFiles).
func implsOf(headers []ImplHeader) [32]byte {
	out := make([]string, len(headers))
	for i, h := range headers {
		out[i] = strings.Join(h.Files, "\x00")
	}
	sort.Strings(out)
	return sha256.Sum256([]byte(strings.Join(out, "\x01")))
}

// importOrderOf orders keys so that a file comes after the files among
// keys it imports, directly or through others, and otherwise as keys are.
func importOrderOf(pi *ProgramInterfaces, keys []string) []string {
	in := map[string]bool{}
	for _, k := range keys {
		in[k] = true
	}
	done := map[string]bool{}
	out := make([]string, 0, len(keys))
	var visit func(k string)
	visit = func(k string) {
		if done[k] {
			return
		}
		done[k] = true
		for _, imp := range pi.imports[k] {
			if in[imp] {
				visit(imp)
			}
		}
		out = append(out, k)
	}
	for _, k := range keys {
		visit(k)
	}
	return out
}

// mapKeys answers m's keys.
func mapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
