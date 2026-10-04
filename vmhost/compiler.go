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
func runNested(source, root string, virtualFiles, hostEnv map[string]string) (string, error) {
	opts := []Option{WithProjectRoot(root)}
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
