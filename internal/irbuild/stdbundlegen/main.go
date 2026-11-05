// Command stdbundlegen lowers the standard library, writes it as a std
// bundle (internal/irbuild/stdbundle.go) into a directory with the stdlib's
// interfaces and facts (std/shared.go), and beside them a `go build
// -overlay` file that supplies them as the internal/irbuild/stdbundle.bin and
// std/stdiface.bin a build with the nomi_stdbundle tag embeds. Run from the
// module root:
//
//	go run ./internal/irbuild/stdbundlegen "$dir"
//	go build -tags nomi_stdbundle -overlay "$dir/overlay.json" ./cmd/nomi
//
// The files are never written into the source tree, so a build with the
// tag and no overlay fails to compile rather than embedding a stale bundle. The
// bundle must come from the sources the build compiles;
// scripts/build-tour-wasm.sh and scripts/release.sh write it just before
// their builds.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/std"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: stdbundlegen <dir>")
		os.Exit(2)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintf(os.Stderr, "stdbundlegen: %v\n", err)
		os.Exit(1)
	}
}

func run(dir string) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(root, "internal", "irbuild", "stdbundle_embed.go")); err != nil {
		return fmt.Errorf("run from the module root: %w", err)
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return err
	}
	data, err := irbuild.StdBundle()
	if err != nil {
		return err
	}
	bundle := filepath.Join(dir, "stdbundle.bin")
	if err := os.WriteFile(bundle, data, 0o644); err != nil {
		return err
	}
	ifaces, err := std.StdArtifact()
	if err != nil {
		return err
	}
	iface := filepath.Join(dir, "stdiface.bin")
	if err := os.WriteFile(iface, ifaces, 0o644); err != nil {
		return err
	}
	overlay, err := json.Marshal(map[string]map[string]string{
		"Replace": {
			filepath.Join(root, "internal", "irbuild", "stdbundle.bin"): bundle,
			filepath.Join(root, "std", "stdiface.bin"):                  iface,
		},
	})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "overlay.json"), overlay, 0o644)
}
