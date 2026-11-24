package stdhash_test

import (
	"context"
	"encoding/hex"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/stdhash"
	"github.com/nomi-language/nomi/rt"
	"github.com/nomi-language/nomi/vmhost"
)

// The table is std/hash.nomi's `Algorithm`, variant by variant in
// declaration order, and the code std/hash.nomi's private `code` passes for
// each variant is its position in both: a program's digest by each variant
// is the table's digest at that position.
func TestAlgorithmsMatchTheEnum(t *testing.T) {
	src, err := os.ReadFile("../../std/hash.nomi")
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := parser.Parse(lexer.Lex(string(src)))
	if err != nil {
		t.Fatal(err)
	}
	var declared []string
	for _, n := range nodes {
		if e, ok := n.(*ast.EnumDef); ok && e.Name == "Algorithm" {
			for _, v := range e.Variants {
				declared = append(declared, v.Name)
			}
		}
	}
	names := stdhash.AlgorithmNames()
	if !slices.Equal(declared, names) {
		t.Fatalf("std/hash.nomi declares Algorithm as %v, and the table holds %v", declared, names)
	}

	var prog strings.Builder
	prog.WriteString("import\n    std::hash.{self, Algorithm}\n    std::io\nend\n\nfn main()\n")
	for _, name := range names {
		prog.WriteString("    io.print(hash.digest_hex(Algorithm." + name + `, String.to_bytes("abc")))` + "\n")
	}
	prog.WriteString("end\n")
	p, err := vmhost.LoadSource("main", prog.String())
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	got := strings.Split(strings.TrimSpace(out.String()), "\n")
	for i, name := range names {
		want := hex.EncodeToString([]byte(stdhash.Digest(int64(i), rt.Bytes("abc"))))
		if i >= len(got) || got[i] != want {
			t.Errorf("Algorithm.%s digests \"abc\" as %v in Nomi, and code %d as %s", name, at(got, i), i, want)
		}
	}
}

func at(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return "nothing"
}
