//go:build nomi_compiler

package main

import (
	"github.com/nomi-language/nomi/vmrunner"
	"github.com/nomi-language/nomi/vmrunner/compiler"
)

func init() {
	options = append(options, vmrunner.WithCompiler(compiler.Table))
}
