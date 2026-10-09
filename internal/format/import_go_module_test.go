package format

import "testing"

// A one-entry import block naming the module `go` comes out flat, and the
// flat form parses. Found by FuzzFormatKeepsMeaning.
func TestFormat_ImportOfModuleNamedGo(t *testing.T) {
	formatsKeepingMeaning(t, "import{go}", "import go\n")
	formatsKeepingMeaning(t, "import{go.hi}", "import go.hi\n")
}
