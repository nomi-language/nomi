package vmhost

import (
	"strings"
	"testing"
)

// std/path reads text alone, so a backtick literal's handler may call it at
// compile time; std/fs reaches the file system, so a handler that calls it
// is refused, as one that prints is.
func TestBacktickLiteral_PathIsPureAndFilesAreEffects(t *testing.T) {
	handler := func(module, body string) string {
		return "import {\n    " + module + "\n    std/literals.{Fragment, Literal}\n}\n\n" +
			"struct Where {\n    p: String\n}\n\n" +
			"impl Literal for Where {\n" +
			"    fn from_fragments(fragments: List<Fragment<String>>) -> Result<Where, String> {\n" +
			"        n = Iter.count(fragments)\n" +
			body +
			"    }\n" +
			"}\n\n" +
			"fn main() {\n    _ = Where`x`\n}\n"
	}
	pure := handler("std/path", "        Ok(#Where{p: path.clean(\"a/./b/../c${n}\")})\n")
	if _, err := LoadSource("main", pure); err != nil {
		t.Fatalf("a handler over std/path is refused: %v", err)
	}
	effect := handler("std/fs", "        case fs.read_text(\"x${n}\") {\n            Ok(text) -> Ok(#Where{p: text})\n            Err(e) -> Err(\"${e}\")\n        }\n")
	ds := literalDiagnostics(t, effect)
	if len(ds) != 1 || !strings.Contains(ds[0].Message, "its handler has effects") || !strings.Contains(ds[0].Message, "fs_host.read_text") {
		t.Fatalf("a handler that reads a file: %v, want the effects error naming the crossing", ds)
	}
}
