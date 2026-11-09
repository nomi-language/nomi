package std

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/ast"
)

// The base modules' state is the stdlib built from its base modules'
// interfaces (stdBaseModules and what they import), with its facts
// installed and its base formed, as analysis.EncodeStdState writes it: a
// process that reads it skips the sweeps over the base modules' interfaces
// and the base (analysis package doc, "The stdlib's state").
//
// A binary built with the nomi_stdbundle tag embeds one beside its
// artifact (std/stdstate.bin, which internal/irbuild/stdbundlegen writes
// with StdState in a process of its own), and reads it when it reads its
// embedded artifact. Any other binary keeps it beside the artifact in the
// artifact cache (`<cache>/std/<hash>.state`). Only a process whose build
// of the base modules was its first build writes it, since the build
// installs the canonical std types for the process
// (analysis.EncodeStdState), so the process that makes an artifact, which
// analyzes the stdlib from source first, writes none, and the next
// process, which builds the base modules from the artifact, does. A state
// that does not read back is ignored, and the base modules are built: a
// cached one is then written again. NOMI_DEBUG_STD_BUNDLE=1 says why one
// was not read and when the embedded one was.
//
// A state names the artifact it was built from by the artifact's head and
// checksum (stdArtifact.sum), and the code that wrote it by
// analysis.StdStateFormat, digests of the generated codecs. The cache's is
// under the binary's build ID already; the format is what binds an
// embedded one, which a process of stdbundlegen wrote, to the binary
// embedding it.
//
// The layout: magic "NOMISTDS", the length of what follows (uint32), the
// artifact's head (thisStdArtifactHead), the artifact's checksum, the
// format (a string), the CRC-32 of the rest, then the rest, the encoded
// state. The length lets a test find an embedded state in a binary's data
// (vmhost's tour bundle test).
const stdStateMagic = "NOMISTDS"

// StdState builds the stdlib's base modules from artifact, which StdArtifact
// made, and answers their state, for a binary to embed beside the artifact
// (internal/irbuild/stdbundlegen). The build must be the process's first
// (analysis.EncodeStdState), so stdbundlegen calls it in a process of its
// own.
func StdState(artifact []byte) ([]byte, error) {
	loadMu.Lock()
	defer loadMu.Unlock()
	defer setupGC()()
	art, err := decodeStdArtifact(artifact)
	if err != nil {
		return nil, err
	}
	// As Shared builds the base modules from an artifact (lazyFromArtifact).
	artifactExports.Store(&art.exports)
	c := newStdCore(art)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ahead = c.decodeFactsAhead()
	lib, err := c.ensureLocked(stdBaseModules)
	if err != nil {
		return nil, err
	}
	if len(lib.Errors) > 0 {
		return nil, errors.New("the stdlib has errors, and a stdlib with errors is built whole")
	}
	return c.encodeState(lib)
}

// state is lib, a snapshot of a stdlib built from its interfaces, as
// analysis.EncodeStdState writes it, with its base formed, and the
// directory its files are in.
func (lib *StdLib) state() (analysis.StdState, string) {
	ifaces := make(map[string][]ast.Node, len(lib.Files))
	for name := range lib.Files {
		ifaces[name] = lib.deferred.iface[name]
	}
	st := analysis.StdState{Files: lib.Files, Nodes: lib.Nodes, Ifaces: ifaces, Base: lib.stdBase(), Errors: lib.Errors}
	return st, filepath.Dir(lib.Files["prelude"].FilePath)
}

// stdStatePath is where the state of this binary's artifact is cached, or
// "".
func stdStatePath() string {
	path := stdArtifactPath()
	if path == "" {
		return ""
	}
	return strings.TrimSuffix(path, ".iface") + ".state"
}

// stateKey is what a state of c's artifact, written by this binary's code,
// starts with after its magic and length.
func (c *stdCore) stateKey() []byte {
	key := binary.LittleEndian.AppendUint32(thisStdArtifactHead(), c.art.sum)
	return appendString(key, analysis.StdStateFormat())
}

// encodeState is the state of lib, c's base modules just built, as a
// file holds it.
func (c *stdCore) encodeState(lib *StdLib) ([]byte, error) {
	st, dir := lib.state()
	data, err := analysis.EncodeStdState(st, dir)
	if err != nil {
		return nil, err
	}
	key := c.stateKey()
	out := []byte(stdStateMagic)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(key)+4+len(data)))
	out = append(out, key...)
	out = binary.LittleEndian.AppendUint32(out, crc32.ChecksumIEEE(data))
	return append(out, data...), nil
}

// keepState writes lib's state, lib being c's base modules just built,
// unless this process may not write it (analysis.EncodeStdState) or the
// cache has one already.
func (c *stdCore) keepState(lib *StdLib) {
	path := stdStatePath()
	if path == "" {
		return
	}
	out, err := c.encodeState(lib)
	if err != nil {
		stdArtifactDebug("no state kept: %v", err)
		return
	}
	if err := artifactcache.WriteFile(path, out); err != nil {
		stdArtifactDebug("writing %s: %v", path, err)
		return
	}
	artifactcache.Prune(filepath.Dir(path), filepath.Base(path), stdArtifactUnused)
}

// fromState makes c's base modules from the embedded state when c's
// artifact is the embedded one, else from the cached state, and reports
// whether it did.
func (c *stdCore) fromState() bool {
	if c.art.embedded && len(embeddedStdState) > 0 {
		start := time.Now()
		err := c.readState(embeddedStdState)
		if err == nil {
			stdArtifactDebug("the base modules were read from the embedded state in %v", time.Since(start).Round(100*time.Microsecond))
			c.stateFrom = "embedded"
			return true
		}
		// A build that embedded a state another tree's code wrote.
		fmt.Fprintf(os.Stderr, "nomi: the std state this binary embeds is not its own, so it builds the base modules: %v\n", err)
	}
	path := stdStatePath()
	if path == "" {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	if err := c.readState(data); err != nil {
		stdArtifactDebug("%s not used: %v", path, err)
		return false
	}
	if info, err := os.Stat(path); err == nil {
		artifactcache.Touch(path, info)
	}
	c.stateFrom = path
	return true
}

// stdBuildDir is the directory a build of the stdlib in this process
// names its files under: the one the process finds the stdlib in, else,
// with none (the browser, a release with no std beside it), the relative
// one a build resolves `std/prelude` to with no project root
// (analysis.BuildStdDeclarations). A module built over the state is named
// under it too, so the state's paths must be.
func stdBuildDir() string {
	if dir, err := stdlibRoot(); err == nil {
		return dir
	}
	return filepath.Dir(analysis.ResolveModulePath("", []string{"std", "prelude"}))
}

func (c *stdCore) readState(data []byte) error {
	if len(data) < len(stdStateMagic)+4 || string(data[:len(stdStateMagic)]) != stdStateMagic {
		return errors.New("not a stdlib state")
	}
	n := binary.LittleEndian.Uint32(data[len(stdStateMagic):])
	data = data[len(stdStateMagic)+4:]
	if int(n) != len(data) {
		return errors.New("truncated")
	}
	key := c.stateKey()
	if !bytes.HasPrefix(data, key) {
		return errors.New("made from another artifact or by other code")
	}
	data = data[len(key):]
	if len(data) < 4 {
		return errors.New("truncated")
	}
	sum := binary.LittleEndian.Uint32(data)
	data = data[4:]
	if crc32.ChecksumIEEE(data) != sum {
		return errors.New("checksum mismatch")
	}
	dir := stdBuildDir()
	st, err := analysis.DecodeStdState(data, dir, c.load)
	if err != nil {
		return err
	}
	prelude := st.Files["prelude"]
	if prelude == nil || prelude.FilePath != filepath.Join(dir, "prelude.nomi") {
		return errors.New("its prelude is not where a build finds it")
	}
	lib := &StdLib{
		Modules:      make(map[string]*analysis.Scope, len(st.Files)),
		Files:        st.Files,
		Nodes:        st.Nodes,
		SyntaxErrors: c.art.syntax,
		Errors:       st.Errors,
		Base:         st.Base,
		Primitives:   prelude.ModuleScope,
		deferred:     c.deferred,
		core:         c,
		baseOnce:     new(sync.Once),
	}
	lib.baseOnce.Do(func() {})
	for name, fa := range st.Files {
		lib.Modules[name] = fa.ModuleScope
		c.deferred.iface[name] = st.Ifaces[name]
	}
	c.cur.Store(lib)
	return nil
}
