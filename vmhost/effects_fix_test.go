package vmhost_test

import (
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A call over the host's list has no fix: only the host can allow it, and
// no file of the program holds its list.
func TestEffectFix_NoneForAHostsList(t *testing.T) {
	ce := refusedByHost(t, "import std::io\n\nfn main()\n    io.print(\"hi\")\nend\n", vmhost.WithEffects())
	for _, d := range ce.Diagnostics {
		if d.Code != vmhost.EffectNotAllowedCode {
			t.Errorf("the refusal is coded %q, not %q", d.Code, vmhost.EffectNotAllowedCode)
		}
		if d.Fix != nil {
			t.Errorf("a call over the host's list has a fix: %+v", d.Fix)
		}
	}
}
