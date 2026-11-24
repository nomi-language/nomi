// Command hostrunnersgen stages what a release `nomi` carries beside the
// std bundle: it gzips a platform's two runners, the full nomi-vm and the
// lean nomi-vm-nonet, into a directory, and writes beside them a
// `go build -overlay` file that supplies them as the
// internal/runners/nomi-vm.gz and nomi-vm-nonet.gz a build with the
// nomi_hostrunners tag embeds (internal/runners' hostrunners_embed.go).
// With -vsix and -zed it also supplies the editor extensions built from
// editors/: the VS Code extension as internal/editors/nomi.vsix, which the
// nomi_vsix tag embeds, and the Zed extension's WebAssembly as
// internal/editors/extension.wasm, which the nomi_zedwasm tag embeds. A
// base overlay's replacements, such as the std bundle's
// (internal/irbuild/stdbundlegen), are carried into it, since `go build`
// takes one overlay. Run from the module root:
//
//	go run ./internal/runners/hostrunnersgen [-vsix nomi.vsix] [-zed extension.wasm] "$dir" "$nomi_vm" "$nomi_vm_nonet" ["$base_overlay"]
//	go build -tags nomi_hostrunners,nomi_vsix,nomi_zedwasm -overlay "$dir/overlay.json" ./cmd/nomi
//
// The runners must be the build's own platform's, from the same commit:
// `nomi build` uses them for that platform with no check
// (internal/runners/prebuilt.go). scripts/release.sh builds them just
// before the `nomi` that carries them, and the extensions once per
// release. The files are never written into the source tree, so a build
// with a tag and no overlay fails to compile.
//
// It prints each runner's size and its gzipped size.
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	vsix := flag.String("vsix", "", "the VS Code extension, staged as internal/editors/nomi.vsix")
	zed := flag.String("zed", "", "the Zed extension's WebAssembly, staged as internal/editors/extension.wasm")
	flag.Parse()
	args := flag.Args()
	if len(args) != 3 && len(args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: hostrunnersgen [-vsix <nomi.vsix>] [-zed <extension.wasm>] <dir> <nomi-vm> <nomi-vm-nonet> [<base overlay.json>]")
		os.Exit(2)
	}
	editors := map[string]string{"nomi.vsix": *vsix, "extension.wasm": *zed}
	if err := run(args[0], args[1], args[2], args[3:], editors); err != nil {
		fmt.Fprintf(os.Stderr, "hostrunnersgen: %v\n", err)
		os.Exit(1)
	}
}

// run stages the runners, and each of editors, internal/editors' file
// name to the file supplying it, that names one.
func run(dir, full, lean string, base []string, editors map[string]string) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	pkg := filepath.Join(root, "internal", "runners")
	if _, err := os.Stat(filepath.Join(pkg, "hostrunners_embed.go")); err != nil {
		return fmt.Errorf("run from the module root: %w", err)
	}
	if dir, err = filepath.Abs(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	overlay := map[string]map[string]string{"Replace": {}}
	if len(base) == 1 {
		data, err := os.ReadFile(base[0])
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &overlay); err != nil {
			return fmt.Errorf("%s: %w", base[0], err)
		}
	}
	for _, r := range []struct{ name, src string }{{"nomi-vm.gz", full}, {"nomi-vm-nonet.gz", lean}} {
		name, src := r.name, r.src
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		var buf bytes.Buffer
		zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if err != nil {
			return err
		}
		if _, err := zw.Write(data); err != nil {
			return err
		}
		if err := zw.Close(); err != nil {
			return err
		}
		out := filepath.Join(dir, name)
		if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
			return err
		}
		overlay["Replace"][filepath.Join(pkg, name)] = out
		fmt.Printf("%s: %d bytes, %d gzipped\n", filepath.Base(src), len(data), buf.Len())
	}
	for name, src := range editors {
		if src == "" {
			continue
		}
		if src, err = filepath.Abs(src); err != nil {
			return err
		}
		if _, err := os.Stat(src); err != nil {
			return err
		}
		overlay["Replace"][filepath.Join(root, "internal", "editors", name)] = src
	}
	data, err := json.Marshal(overlay)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "overlay.json"), data, 0o644)
}
