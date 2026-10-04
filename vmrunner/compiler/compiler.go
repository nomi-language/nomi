// Package compiler binds std/compiler's five hosts for a runner whose program
// crosses into them. They check, hover and run Nomi source, so this package
// links the whole front end and the IR builder (and nomi/vmhost, whose VM is
// compiler.run's engine); a runner imports it only when `nomi build` found a
// crossing into std/compiler.
package compiler

import (
	"github.com/nomi-language/nomi/internal/compilerhosts"
	"github.com/nomi-language/nomi/vmrunner"

	// compiler.run's engine: vmhost's init installs the VM.
	_ "github.com/nomi-language/nomi/vmhost"
)

// Table is std/compiler's hosts for a program whose imports resolve against
// root. Pass it to vmrunner.WithCompiler.
func Table(root string) vmrunner.HostTable {
	return compilerhosts.Table(root, nil)
}
