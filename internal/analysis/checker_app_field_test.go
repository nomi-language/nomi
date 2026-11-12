package analysis

import "testing"

// A struct no entry boot returns is not an application type, so `Type.field`
// on it stays an owner-qualified access, which the error explains.
func TestAppFieldReadNeedsAnApplicationType(t *testing.T) {
	_, errs := checkSource("struct Config\n    port: Int\nend\nfn main()\n    _ = Config.port\nend")
	expectError(t, errs, "`Config.port` reads an application field only when an entry boot returns `Config`")
}
