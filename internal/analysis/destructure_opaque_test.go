package analysis_test

import (
	"strings"
	"testing"
)

// opaqueBoxModule declares an opaque struct, and destructures it in its own
// file, which may.
const opaqueBoxModule = `pub opaque struct Secret
    value: Int
end

pub fn make(v: Int) -> Secret
    Secret{value: v}
end

pub fn reveal(s: Secret) -> Int
    {value:} = s
    value
end
`

// TestDestructurePattern_AnOpaqueStructOutsideItsModuleIsRejected: a
// destructuring statement over an opaque struct from another file is the
// error a struct pattern over it is, and one in the declaring file is
// accepted.
func TestDestructurePattern_AnOpaqueStructOutsideItsModuleIsRejected(t *testing.T) {
	const want = "cannot destructure opaque type 'Secret' outside its defining module"
	for _, form := range []string{"{value:} = s", "Secret{value:} = s"} {
		_, errs := opaqueProject(t, map[string]string{
			"secret.nomi": opaqueBoxModule,
			"main.nomi": `import secret.{self, Secret}

fn peek(s: Secret) -> Int
    ` + form + `
    value
end
`,
		})
		found := false
		for _, e := range errs {
			if strings.Contains(e.Message, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s from another file: want %q, got %v", form, want, errs)
		}
	}
	_, errs := opaqueProject(t, map[string]string{
		"secret.nomi": opaqueBoxModule,
		"main.nomi": `import secret.{self}

fn main()
    dbg secret.reveal(secret.make(1))
end
`,
	})
	if len(errs) > 0 {
		t.Fatalf("a destructure in the opaque struct's own file is rejected: %v", errs)
	}
}

// opaqueTokenModule declares an opaque distinct type, and destructures it
// in its own file, which may.
const opaqueTokenModule = `pub opaque type Token Int

pub fn make(v: Int) -> Token
    Token(v)
end

pub fn reveal(t: Token) -> Int
    Token(n) = t
    n
end
`

// TestDestructurePattern_AnOpaqueDistinctOutsideItsModuleIsRejected: a
// destructuring statement over an opaque distinct type from another file is
// the error the pattern is in a `case` arm, and one in the declaring file is
// accepted.
func TestDestructurePattern_AnOpaqueDistinctOutsideItsModuleIsRejected(t *testing.T) {
	const want = "cannot destructure opaque type 'Token' outside its defining module"
	for _, body := range []string{"Token(n) = t\n    n", "case t\n        Token(n) -> n\n    end"} {
		_, errs := opaqueProject(t, map[string]string{
			"secret.nomi": opaqueTokenModule,
			"main.nomi": `import secret.{self, Token}

fn peek(t: Token) -> Int
    ` + body + `
end
`,
		})
		found := false
		for _, e := range errs {
			if strings.Contains(e.Message, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%q from another file: want %q, got %v", body, want, errs)
		}
	}
	_, errs := opaqueProject(t, map[string]string{
		"secret.nomi": opaqueTokenModule,
		"main.nomi": `import secret.{self}

fn main()
    dbg secret.reveal(secret.make(1))
end
`,
	})
	if len(errs) > 0 {
		t.Fatalf("a destructure in the opaque distinct type's own file is rejected: %v", errs)
	}
}
