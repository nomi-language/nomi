//go:build nomi_debugprobe

package vmhost

import (
	"os"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// The Debug probe's target is a Debug body the front end synthesizes, a
// function whose position is in a band analysis.DebugProbeClass names,
// with "std-" added for a std module's. NOMI_DEBUG_PROBE names the file
// the probe appends its lines to, and NOMI_DEBUG_PROBE_TARGET the classes
// it targets, separated by commas ("derived" when unset).
func init() {
	path := os.Getenv("NOMI_DEBUG_PROBE")
	if path == "" {
		return
	}
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	targets := strings.Split(os.Getenv("NOMI_DEBUG_PROBE_TARGET"), ",")
	if os.Getenv("NOMI_DEBUG_PROBE_TARGET") == "" {
		targets = []string{"derived"}
	}
	vm.SetDebugProbe(func(f *ir.Func) string {
		if f.Sym() == nil {
			return ""
		}
		if impl, ok := f.Sym().Impl(); !ok || impl.Interface != "Debug" || impl.Func != "inspect" {
			return ""
		}
		pos := f.Pos()
		class := analysis.DebugProbeClass(pos.Line())
		if !slices.Contains(targets, class) {
			return ""
		}
		if pos.Stdlib() {
			class = "std-" + class
		}
		return class
	}, out)
}
