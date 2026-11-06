package main

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/nomi-language/nomi/vmhost"
)

// keptBuild is what `nomi build` keeps in the artifact cache for a program
// with no Go bindings: everything the build made from the program before it
// picks a runner, so a build of unchanged files for the same target only
// writes the runner and the image again.
type keptBuild struct {
	Image []byte
	// Sources are where the image's files region is read from when the
	// build writes it (vmhost.Program.EmbeddedSources).
	Sources      []vmhost.EmbedSource
	UsesCompiler bool
	// Reactor says the program is built as a wasip1 reactor, and NoNetwork
	// that it reaches no network crossing (ffirun.Variant).
	Reactor   bool
	NoNetwork bool
	// Decls and LocaleFiles are --web's nomi.d.ts and locale files.
	Decls       string
	LocaleFiles map[string][]byte
	// Notes is what the build wrote on stderr while it made the image (a
	// web build's note about files), which a hit writes again.
	Notes string
}

// buildKind names a build in the artifact cache by every setting the image
// depends on.
func buildKind(b buildArgs) string {
	return fmt.Sprintf("build %s/%s web=%t novm=%t allow=%s", b.target.GOOS, b.target.GOARCH, b.web, b.noVM,
		strings.Join(b.allowUnsupported, ","))
}

func (k keptBuild) encode() []byte {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(k); err != nil {
		return nil
	}
	return buf.Bytes()
}

// decodeKeptBuild is the build data kept, or false.
func decodeKeptBuild(data []byte) (keptBuild, bool) {
	var k keptBuild
	if data == nil || gob.NewDecoder(bytes.NewReader(data)).Decode(&k) != nil || len(k.Image) == 0 {
		return keptBuild{}, false
	}
	return k, true
}

// buildInProcess makes the image of the program at absPath, which has no Go
// bindings, for b's target, and keeps it in cache. A refusal is written to
// stderr and answered as errBuildRefused.
func buildInProcess(b buildArgs, absPath string, stderr io.Writer, cache *vmhost.Cache) (keptBuild, error) {
	var k keptBuild
	var notes bytes.Buffer
	stderr = io.MultiWriter(stderr, &notes)
	// A program some of whose files' bodies changed since it was kept is
	// relinked from its file records rather than checked and lowered whole.
	load := vmhost.OpenCache(absPath)
	p, relinked := load.Relinked()
	var err error
	if !relinked {
		if p, err = vmhost.Load(absPath, vmhost.WithCache(load)); err != nil {
			vmhost.WriteFailure(stderr, err)
			return k, errBuildRefused
		}
	}
	k.Reactor = b.target == wasip1 && p.HasExports()
	if !b.noVM {
		if refused := p.CheckTarget(buildCommand(b), b.target.GOOS, b.target.GOARCH, b.web || k.Reactor, b.allowUnsupported); refused != nil {
			refused.Write(stderr)
			return k, errBuildRefused
		}
		if b.web && p.Uses(vmhost.ClassFiles, true) {
			fmt.Fprintln(stderr, vmhost.WebFilesNote)
		}
	}
	buildImage := p.BuildImage
	// A .nomic carries the exported functions too, for a reactor to load
	// (`nomi build-vm --reactor`).
	if b.web || k.Reactor || b.noVM {
		buildImage = p.BuildExportsImage
	}
	if b.web {
		buildImage = func() ([]byte, bool, error) {
			web, err := p.BuildWebImage()
			if err != nil {
				return nil, false, err
			}
			k.LocaleFiles = web.Files
			return web.Image, web.UsesCompiler, nil
		}
	}
	k.Image, k.UsesCompiler, err = buildImage()
	if err != nil {
		if blocked, ok := vmhost.IsBlocked(err); ok {
			blocked.Write(stderr, vmhost.DisplayPath(absPath))
			return k, errBuildRefused
		}
		// An `embed` pattern whose base does not exist, at the pattern.
		var ds vmhost.Diagnostics
		if errors.As(err, &ds) {
			vmhost.WriteFailure(stderr, err)
			return k, errBuildRefused
		}
		return k, fmt.Errorf("nomi build: %w", err)
	}
	if k.Sources, err = p.EmbeddedSources(k.Image); err != nil {
		return k, fmt.Errorf("nomi build: %w", err)
	}
	if b.web {
		k.Decls = p.WebDeclarations()
	}
	if !b.noVM {
		k.NoNetwork = !p.UsesNetwork(b.web || k.Reactor)
	}
	k.Notes = notes.String()
	cache.Keep(p, k.encode())
	return k, nil
}
