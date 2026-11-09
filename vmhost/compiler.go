package vmhost

// compiler.run's engine: the VM. The five std/compiler hosts themselves are
// internal/compilerhosts'; every machine a Program opens binds them.

import (
	"bytes"
	"context"
	"strings"

	"github.com/nomi-language/nomi/internal/stdcompilerrun"
)

func init() {
	stdcompilerrun.SetEngine(runNested)
}

// runNested is the VM as stdcompilerrun's engine: lower the source, run it,
// and answer its stdout.
//
// The nested program is checked as the shared front end checks it
// (stdcompiler.RunFrontEnd): its imports resolve against root, and the
// project's files it imports are checked under root's nomi.toml, but the
// source itself is no file of that project, so entry_points does not place
// it. It gets no host tables of the caller's: a project file it imports
// that declares a `go` binding is refused as unmet.
func runNested(source, root string, virtualFiles, hostEnv map[string]string) (string, error) {
	opts := []Option{WithProjectRoot(root), func(c *config) { c.standalone = true }}
	if virtualFiles != nil {
		opts = append(opts, WithVirtualFiles(virtualFiles))
	}
	p, err := LoadSource("main", source, opts...)
	if err != nil {
		return "", &stdcompilerrun.Unrunnable{Reason: "compiler.run: the VM's front end refused a source " +
			"the shared front end accepted: " + err.Error()}
	}
	var out bytes.Buffer
	err = p.run(context.Background(), &out, nil, false, hostEnv)
	if b, blocked := IsBlocked(err); blocked {
		return "", &stdcompilerrun.Unrunnable{Reason: "compiler.run: the VM cannot run the nested program: " +
			strings.Join(b.Reasons, "; ")}
	}
	if err != nil {
		// A fault, including rt.MainDeadlineFault for a deadline that ran
		// out while main blocked, is the Err payload: what `nomi run`
		// reports.
		return "", err
	}
	return out.String(), nil
}
