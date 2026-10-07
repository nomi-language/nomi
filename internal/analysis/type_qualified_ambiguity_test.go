package analysis_test

import (
	"strings"
	"testing"
)

const thingFile = `pub struct Thing {
    n: Int
}

pub interface Alpha {
    fn go(x: self): Int
}

pub interface Beta {
    fn go(x: self): Int
}

impl Alpha for Thing {
    fn go(x: Thing): Int {
        x.n + 1
    }
}
`

// `Thing.go` is ambiguous when Thing implements two interfaces that each
// declare `go`, whether or not the calling file can name either interface:
// here the second impl is in a third file and the caller imports only Thing.
func TestTypeQualifiedCall_AmbiguousThroughAnImplTheCallerCannotSee(t *testing.T) {
	const want = "type 'Thing' impl 'Alpha' and 'Beta', which each declare 'go' — type-qualified 'Thing.go(...)' is ambiguous"
	entry := "import {\n    beta\n    thing.Thing\n}\n\nfn main() {\n    _ = beta.touch()\n    _ = Thing.go(Thing{n: 3})\n}\n"
	siblings := map[string]string{
		"thing": thingFile,
		"beta":  "import thing.{Beta, Thing}\n\nimpl Beta for Thing {\n    fn go(x: Thing): Int {\n        x.n + 2\n    }\n}\n\npub fn touch(): Int {\n    2\n}\n",
	}
	errs := checkTypedLiteralProject(t, entry, siblings)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, want) {
			found = true
		}
	}
	if !found {
		t.Fatalf("want %q, got %v", want, errs)
	}
}

// Another file's own `Thing`, implementing `Beta` with a `go`, is a different
// type, so it does not make this file's `Thing.go` ambiguous.
func TestTypeQualifiedCall_ASameNamedTypeElsewhereIsNoRival(t *testing.T) {
	entry := "import {\n    other\n    thing.Thing\n}\n\nfn main() {\n    _ = other.touch()\n    _ = Thing.go(Thing{n: 3})\n}\n"
	siblings := map[string]string{
		"thing": thingFile,
		"other": "pub struct Thing {\n    m: Int\n}\n\npub interface Beta {\n    fn go(x: self): Int\n}\n\nimpl Beta for Thing {\n    fn go(x: Thing): Int {\n        x.m\n    }\n}\n\npub fn touch(): Int {\n    Beta.go(Thing{m: 2})\n}\n",
	}
	for _, e := range checkTypedLiteralProject(t, entry, siblings) {
		if strings.Contains(e.Message, "is ambiguous") {
			t.Fatalf("another file's Thing was counted as this Thing's rival: %s", e.Error())
		}
	}
}
