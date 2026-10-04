package analysis

import "testing"

// A struct no entry boot returns is not an application type, so `Type.field`
// on it stays an owner-qualified access, which the error explains.
func TestAppFieldReadNeedsAnApplicationType(t *testing.T) {
	_, errs := checkSource("struct Config { port: Int }\nfn main() { _ = Config.port }")
	expectError(t, errs, "`Config.port` reads an application field only when an entry boot returns `Config`")
}
