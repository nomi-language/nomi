package irbuild

import (
	"fmt"
	"strings"
	"testing"
)

// A derived FromJson decodes each field once and builds the value once, so
// its lowered body grows by the same number of blocks for each field a
// struct or a struct variant adds, defaulted or not: this fixture lowers to
// 92, 132 and 172 blocks for 2, 4 and 6 defaulted fields. A body that
// decoded the rest of the fields again in each of a field's branches grew
// about 3x per defaulted field (304, 2,464 and 21,904 blocks), and 12 never
// finished lowering.
func TestFromJson_DerivedBodyGrowsLinearlyInFields(t *testing.T) {
	blocks := func(n int) int {
		var fields, variant strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&fields, "  f%d: Int = %d\n", i, i)
			fmt.Fprintf(&variant, "    v%d: Int = %d,\n", i, i)
		}
		src := "import\n  std::json.FromJson\n  std::json.Json\nend\n\n" +
			"struct G\n  word: String\n" + fields.String() + "end\n\nderive FromJson for G\n\n" +
			"enum E\n  V {\n    word: String,\n" + variant.String() + "  }\n  W\nend\n\nderive FromJson for E\n\n" +
			"fn main()\n  dbg FromJson.from_json<G>(Json.Null)\n  dbg FromJson.from_json<E>(Json.Null)\nend\n"
		p, err := AnalyzeSource("main", src)
		if err != nil {
			t.Fatalf("the front end rejects the fixture with %d fields, so nothing below is tested: %v\n%s", n, err, src)
		}
		res, _, err := GenerateIR(p)
		if err != nil {
			t.Fatalf("%d fields: %v", n, err)
		}
		total, found := 0, 0
		for _, m := range res.IR {
			if !strings.HasSuffix(m.Name(), "main.nomi") {
				continue
			}
			for _, f := range m.Funcs() {
				if strings.Contains(f.Name(), "from_json") {
					found++
					total += len(f.Blocks())
				}
			}
		}
		if found != 2 {
			t.Fatalf("%d fields: found %d derived from_json functions, want 2 (G's and E's)", n, found)
		}
		return total
	}
	b4, b8, b12 := blocks(4), blocks(8), blocks(12)
	if b12-b8 != b8-b4 {
		t.Fatalf("derived from_json blocks for 4, 8 and 12 defaulted fields: %d, %d, %d; each 4 fields should add the same number", b4, b8, b12)
	}
	t.Logf("derived from_json blocks (struct and variant) for 4, 8 and 12 defaulted fields: %d, %d, %d", b4, b8, b12)
}
