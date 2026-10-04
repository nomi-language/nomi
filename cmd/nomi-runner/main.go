// Command nomi-runner is the runner `nomi build` appends a program's linked IR
// image to. It reads the image from its own executable's tail and runs `main`
// on the VM as `nomi run` does (nomi/vmrunner).
//
// `nomi build` builds it with the Go toolchain and caches it per compiler,
// target and variant (internal/ffirun's runner.go). The build tag
// nomi_compiler links std/compiler's hosts (compiler.go), which bring the
// front end with them; without it the runner links no front end.
package main

import "github.com/nomi-language/nomi/vmrunner"

// options are the runner's host options: std/compiler's hosts when built
// with the nomi_compiler tag, nothing otherwise.
var options []vmrunner.Option

func main() {
	vmrunner.Main(options...)
}
