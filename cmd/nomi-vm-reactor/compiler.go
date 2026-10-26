//go:build nomi_compiler

package main

import (
	"github.com/nomi-language/nomi/vmrunner"
	"github.com/nomi-language/nomi/vmrunner/compiler"
	"github.com/nomi-language/nomi/vmrunner/reactor"
)

func init() {
	reactor.Use(vmrunner.WithCompiler(compiler.Table))
}
