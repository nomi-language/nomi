package stdhttp_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/stdhttp"
)

// The signature tables are std/signatures.nomi's `KeyKind` and `Scheme`,
// variant by variant in declaration order, and the codes the module's
// private `kind_code`, `kind_of` and `scheme_code` give each variant are its
// position in both: each Scheme's `key_kind` in a program is the KeyKind
// at the position its row names, and a scheme of another kind names the
// scheme's own variant when it refuses a key.
func TestSignatureTablesMatchTheEnums(t *testing.T) {
	src, err := os.ReadFile("../../std/signatures.nomi")
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := parser.Parse(lexer.Lex(string(src)))
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string][]string{}
	for _, n := range nodes {
		if e, ok := n.(*ast.EnumDef); ok {
			for _, v := range e.Variants {
				declared[e.Name] = append(declared[e.Name], v.Name)
			}
		}
	}
	kinds, schemes, schemeKind := stdhttp.SignatureTables()
	if !slices.Equal(declared["KeyKind"], kinds) {
		t.Fatalf("std/signatures.nomi declares KeyKind as %v, and the table holds %v", declared["KeyKind"], kinds)
	}
	if encryptions := stdhttp.EncryptionTable(); !slices.Equal(declared["Encryption"], encryptions) {
		t.Fatalf("std/signatures.nomi declares Encryption as %v, and the table holds %v", declared["Encryption"], encryptions)
	}
	if !slices.Equal(declared["Scheme"], schemes) {
		t.Fatalf("std/signatures.nomi declares Scheme as %v, and the table holds %v", declared["Scheme"], schemes)
	}

	var prog strings.Builder
	prog.WriteString("import\n    std::io\n    std::signatures.{self, KeyKind, PrivateKey, Scheme}\nend\n\nfn main()\n")
	prog.WriteString("    key = PrivateKey.generate_ed25519()\n    message = String.to_bytes(\"m\")\n")
	var want []string
	for i, s := range schemes {
		kind := kinds[schemeKind[i]]
		prog.WriteString("    io.print(Scheme.key_kind(Scheme." + s + ") == KeyKind." + kind + ")\n")
		want = append(want, "True")
		if kind != "Ed25519" {
			prog.WriteString("    io.print(signatures.sign_with(key, Scheme." + s + ", message))\n")
			want = append(want, "Err(an Ed25519 key does not sign with "+s+")")
		}
	}
	prog.WriteString("end\n")
	got := strings.Split(strings.TrimSpace(run(t, prog.String())), "\n")
	if !slices.Equal(got, want) {
		t.Errorf("the program prints:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
