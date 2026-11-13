package std

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// stdArtifact is the stdlib's interfaces and facts: what Shared builds the
// stdlib from.
type stdArtifact struct {
	// ifaces holds each module's interface, encoded (analysis.FileInterface),
	// by module name, the prelude's included.
	ifaces map[string][]byte
	// syntax holds the parse errors of each module's source, by module name.
	syntax map[string][]parser.ParseError
	// imports holds the stdlib modules each module's source imports, at any
	// depth (analysis.StdModuleImports), by module name: what a stdlib
	// built a module at a time builds with it.
	imports map[string][]string
	// exports holds what each module exports to the import help
	// (analysis.StdModuleExport), by module name: what a diagnostic naming
	// the import a file lacks reads, which would otherwise parse every
	// module's source.
	exports map[string]analysis.StdModuleExport
	// facts is analysis.StdFacts, encoded; nil until worked out.
	facts []byte
	// sum is the CRC-32 of the artifact's body as it was read or last
	// written, which names it to the base modules' state built from it
	// (state.go).
	sum uint32
	// embedded is set on the artifact the binary embeds, whose base
	// modules' state the binary embeds too (state.go).
	embedded bool
}

// The artifact's layout:
//
//	magic "NOMISTDI"
//	uvarint stdArtifactVersion
//	string Digest(), bool FixturesLoaded()
//	uint32 the CRC-32 of the rest, then the rest:
//	uvarint the module count, and per module in name order its name, its
//	interface's bytes, its parse error count and each error's line, column
//	and message, its imports' count and each import's name, and whether it
//	exports anything public, its public types' count and each type's name;
//	then the facts' bytes.
const (
	stdArtifactMagic = "NOMISTDI"
	// stdArtifactVersion versions the layout above; the interfaces and the
	// facts carry versions of their own.
	stdArtifactVersion = 3
	// stdArtifactUnused is how long a cached artifact no process has used
	// is kept: each build of the compiler writes its own.
	stdArtifactUnused = 24 * time.Hour
)

// StdArtifact makes the stdlib's artifact in this process, for a binary to
// embed (internal/irbuild/stdbundlegen).
func StdArtifact() ([]byte, error) {
	loadMu.Lock()
	defer loadMu.Unlock()
	art := extractStdArtifact()
	if _, err := buildFromArtifact(art, load()); err != nil {
		return nil, err
	}
	return encodeStdArtifact(art), nil
}

// extractStdArtifact parses every embedded module and extracts its
// interface, answering the artifact without facts.
func extractStdArtifact() *stdArtifact {
	art := newStdArtifact()
	for _, name := range append(embeddedStdlibNames(), "prelude") {
		data, ok := ReadFile(name)
		if !ok {
			continue
		}
		nodes, errs := parser.ParseWithRecovery(lexer.Lex(string(data)))
		if len(errs) > 0 {
			art.syntax[name] = errs
		}
		art.ifaces[name] = analysis.ExtractInterface("std/"+name, "", nodes).Encode()
		art.imports[name] = analysis.StdModuleImports(name, nodes)
		if len(errs) == 0 && !strings.HasPrefix(name, "_fixtures/") {
			// A test-only fixture is no module a program imports; a
			// nested module (`storage/testing`) is.
			art.exports[name] = analysis.StdModuleExportOf(name, nodes)
		}
	}
	return art
}

func newStdArtifact() *stdArtifact {
	return &stdArtifact{ifaces: map[string][]byte{}, syntax: map[string][]parser.ParseError{}, imports: map[string][]string{}, exports: map[string]analysis.StdModuleExport{}}
}

// thisStdArtifactHead is the head of an artifact this process reads and
// writes: its version, the stdlib's digest and the fixtures setting.
func thisStdArtifactHead() []byte {
	w := []byte(stdArtifactMagic)
	w = binary.AppendUvarint(w, stdArtifactVersion)
	w = appendString(w, Digest())
	if FixturesLoaded() {
		w = append(w, 1)
	} else {
		w = append(w, 0)
	}
	return w
}

func appendString(b []byte, s string) []byte {
	b = binary.AppendUvarint(b, uint64(len(s)))
	return append(b, s...)
}

func encodeStdArtifact(art *stdArtifact) []byte {
	var body []byte
	names := make([]string, 0, len(art.ifaces))
	for name := range art.ifaces {
		names = append(names, name)
	}
	sort.Strings(names)
	body = binary.AppendUvarint(body, uint64(len(names)))
	for _, name := range names {
		body = appendString(body, name)
		body = appendString(body, string(art.ifaces[name]))
		errs := art.syntax[name]
		body = binary.AppendUvarint(body, uint64(len(errs)))
		for _, e := range errs {
			body = binary.AppendUvarint(body, uint64(e.Line))
			body = binary.AppendUvarint(body, uint64(e.Col))
			body = appendString(body, e.Message)
		}
		body = binary.AppendUvarint(body, uint64(len(art.imports[name])))
		for _, imp := range art.imports[name] {
			body = appendString(body, imp)
		}
		e, ok := art.exports[name]
		switch {
		case !ok:
			body = append(body, 0)
		case !e.Public:
			body = append(body, 1)
		default:
			body = append(body, 2)
		}
		body = binary.AppendUvarint(body, uint64(len(e.Types)))
		for _, t := range e.Types {
			body = appendString(body, t)
		}
	}
	body = appendString(body, string(art.facts))
	out := thisStdArtifactHead()
	art.sum = crc32.ChecksumIEEE(body)
	out = binary.LittleEndian.AppendUint32(out, art.sum)
	return append(out, body...)
}

// artifactReader reads what encodeStdArtifact wrote, failing on the first
// malformed value.
type artifactReader struct {
	b   []byte
	err error
}

func (r *artifactReader) uv() uint64 {
	if r.err != nil {
		return 0
	}
	v, n := binary.Uvarint(r.b)
	if n <= 0 {
		r.err = errors.New("malformed std artifact")
		return 0
	}
	r.b = r.b[n:]
	return v
}

func (r *artifactReader) bytes() []byte {
	n := r.uv()
	if r.err != nil {
		return nil
	}
	if n > uint64(len(r.b)) {
		r.err = errors.New("malformed std artifact")
		return nil
	}
	v := r.b[:n:n]
	r.b = r.b[n:]
	return v
}

func decodeStdArtifact(data []byte) (*stdArtifact, error) {
	head := thisStdArtifactHead()
	if len(data) < len(head)+4 || string(data[:len(head)]) != string(head) {
		return nil, errors.New("made by another format, stdlib or fixtures setting")
	}
	data = data[len(head):]
	sum := binary.LittleEndian.Uint32(data)
	data = data[4:]
	if crc32.ChecksumIEEE(data) != sum {
		return nil, errors.New("checksum mismatch")
	}
	r := &artifactReader{b: data}
	art := newStdArtifact()
	art.sum = sum
	for n := r.uv(); n > 0 && r.err == nil; n-- {
		name := string(r.bytes())
		art.ifaces[name] = r.bytes()
		for m := r.uv(); m > 0 && r.err == nil; m-- {
			e := parser.ParseError{Line: int(r.uv()), Col: int(r.uv())}
			e.Message = string(r.bytes())
			art.syntax[name] = append(art.syntax[name], e)
		}
		for m := r.uv(); m > 0 && r.err == nil; m-- {
			art.imports[name] = append(art.imports[name], string(r.bytes()))
		}
		kind := r.uv()
		var e analysis.StdModuleExport
		for m := r.uv(); m > 0 && r.err == nil; m-- {
			e.Types = append(e.Types, string(r.bytes()))
		}
		if kind > 0 {
			e.Public = kind == 2
			art.exports[name] = e
		}
	}
	art.facts = r.bytes()
	if r.err == nil && len(r.b) != 0 {
		r.err = errors.New("malformed std artifact: trailing bytes")
	}
	if r.err != nil {
		return nil, r.err
	}
	return art, nil
}

// stdArtifactPath is where this binary's artifact is cached, or "" when
// there is no cache or the binary has no build ID to name it by.
func stdArtifactPath() string {
	root, id := artifactcache.Root(), artifactcache.CompilerID()
	if root == "" || id == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%t", id, stdArtifactVersion, FixturesLoaded())))
	return filepath.Join(root, "std", hex.EncodeToString(sum[:16])+".iface")
}

// readStdArtifact answers the embedded artifact or the cached one, and
// where it came from, or nil when there is neither or neither is this
// process's.
func readStdArtifact() (*stdArtifact, string) {
	if len(embeddedStdInterfaces) > 0 {
		art, err := decodeStdArtifact(embeddedStdInterfaces)
		if err == nil {
			art.embedded = true
			return art, "embedded"
		}
		fmt.Fprintf(os.Stderr, "nomi: the std interfaces this binary embeds are not its own, so it extracts them: %v\n", err)
	}
	path := stdArtifactPath()
	if path == "" {
		return nil, ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, ""
	}
	art, err := decodeStdArtifact(data)
	if err != nil {
		stdArtifactDebug("%s not used: %v", path, err)
		return nil, ""
	}
	if info, err := os.Stat(path); err == nil {
		artifactcache.Touch(path, info)
	}
	return art, path
}

// writeStdArtifact keeps art in the cache for the next process, and prunes
// the artifacts of binaries no process has used for a day.
func writeStdArtifact(art *stdArtifact) {
	path := stdArtifactPath()
	if path == "" {
		return
	}
	if err := artifactcache.WriteFile(path, encodeStdArtifact(art)); err != nil {
		stdArtifactDebug("writing %s: %v", path, err)
		return
	}
	artifactcache.Prune(filepath.Dir(path), filepath.Base(path), stdArtifactUnused)
}
