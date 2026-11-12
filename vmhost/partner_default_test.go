package vmhost_test

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A partner test calls its file's private function and leaves a defaulted
// parameter out. The call fills the default through the accessor the
// declaring file builds, which it built only for a `pub fn` before, so the
// case was blocked: "its body is not supported yet, so `fn enc default 2`
// cannot run". The second assertion passes the parameter, and both
// assertions fail if the default is filled wrongly, so the case ran.
func TestPartnerTest_LeavesAPrivateFunctionsDefaultOut(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"nomi.toml": "[module]\nname = \"gap\"\n",
		"gap.nomi": `fn enc(text: String, keep: Bool = False) -> String
    text |> Iter.map(fn c -> if keep and c == "/" then c else "x") |> String.join("")
end

pub fn use_it() -> String
    enc("a/b")
end
`,
		"gap_test.nomi": `import gap

test "default"
    assert gap.enc("a/b") == "xxx"
    assert gap.enc("a/b", keep: True) == "x/x"
end
`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := vmhost.Load(filepath.Join(dir, "gap_test.nomi"))
	if err != nil {
		t.Fatalf("the front end rejects this, so nothing below is tested: %v", err)
	}
	cases := p.Cases(io.Discard, vmhost.TestOptions{})
	if len(cases) != 1 {
		t.Fatalf("%d cases, want 1", len(cases))
	}
	if c := cases[0]; c.Err != nil || len(c.Blocked) > 0 {
		t.Fatalf("case %q: err %v, blocked %v", c.Name, c.Err, c.Blocked)
	}
}
