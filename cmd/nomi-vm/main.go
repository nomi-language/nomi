// Command nomi-vm is the Nomi VM as its own binary: `nomi build` appends a
// program's linked IR image to it, and it reads the image from its own
// executable's tail and runs `main` as `nomi run` does (nomi/vmrunner). With
// nothing appended it runs the compiled program (.nomic) `nomi build --no-vm`
// wrote that it is handed: `nomi-vm app.nomic [args...]`, or framed at the
// start of stdin.
//
// A release ships it prebuilt; a `nomi` built from source builds it with Go
// and caches it per compiler, target and variant (internal/runners). The
// build tag nomi_compiler links std/compiler's hosts (compiler.go), which
// bring the front end with them; without it the runner links no front end.
package main

import "github.com/nomi-language/nomi/vmrunner"

// options are the runner's host options: std/compiler's hosts when built
// with the nomi_compiler tag, nothing otherwise.
var options []vmrunner.Option

func main() {
	vmrunner.Main(options...)
}
